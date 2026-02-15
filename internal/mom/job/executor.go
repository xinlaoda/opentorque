package job

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// Executor handles starting and monitoring job processes.
type Executor struct {
	pbsHome    string
	spoolDir   string
	jobsDir    string
}

// NewExecutor creates a job executor.
func NewExecutor(pbsHome, spoolDir, jobsDir string) *Executor {
	return &Executor{
		pbsHome:  pbsHome,
		spoolDir: spoolDir,
		jobsDir:  jobsDir,
	}
}

// StartJob starts executing a job.
func (e *Executor) StartJob(j *Job) error {
	j.Mu.Lock()
	defer j.Mu.Unlock()

	// Write job script to disk
	scriptPath := filepath.Join(e.jobsDir, j.ID+".SC")
	if j.Script != "" {
		if err := os.WriteFile(scriptPath, []byte(j.Script), 0700); err != nil {
			return fmt.Errorf("write script: %w", err)
		}
		j.ScriptFile = scriptPath
	}

	if j.ScriptFile == "" {
		return fmt.Errorf("no script for job %s", j.ID)
	}

	// Store final output destinations, always spool locally during execution
	j.FinalStdout = stripHostPrefix(j.StdoutPath)
	j.FinalStderr = stripHostPrefix(j.StderrPath)
	j.StdoutPath = filepath.Join(e.spoolDir, j.ID+".OU")
	j.StderrPath = filepath.Join(e.spoolDir, j.ID+".ER")

	// Determine shell
	shell := j.Shell
	if shell == "" {
		shell = "/bin/sh"
		if runtime.GOOS == "windows" {
			shell = "cmd.exe"
		}
	}

	// Build environment
	env := e.buildEnvironment(j)

	// Create output files
	stdout, err := os.Create(j.StdoutPath)
	if err != nil {
		return fmt.Errorf("create stdout: %w", err)
	}
	stderr, err := os.Create(j.StderrPath)
	if err != nil {
		stdout.Close()
		return fmt.Errorf("create stderr: %w", err)
	}

	// Build command
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command(shell, "/c", j.ScriptFile)
	} else {
		cmd = exec.Command(shell, j.ScriptFile)
	}

	cmd.Env = env
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Dir = e.getWorkDir(j)

	// Set up process group for Unix (allows killing entire group)
	if runtime.GOOS != "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Setsid: true, // create new session
		}
	}

	// Look up user (run as job owner on Unix)
	// For security, we skip setuid here and rely on the script permissions
	// In production, this would use syscall.Setuid/Setgid

	log.Printf("[EXEC] Starting job %s: %s %s", j.ID, shell, j.ScriptFile)

	// Start the process
	if err := cmd.Start(); err != nil {
		stdout.Close()
		stderr.Close()
		return fmt.Errorf("start job %s: %w", j.ID, err)
	}

	j.Process = cmd.Process
	j.SessionID = cmd.Process.Pid // on Unix, Setsid makes PID == SID
	j.State = StateRunning
	j.Substate = SubstateRunning
	j.StartTime = time.Now()

	// Add task
	task := &Task{
		ID:       0,
		ParentID: j.ID,
		State:    TaskRunning,
		Pid:      cmd.Process.Pid,
		Sid:      cmd.Process.Pid,
		Started:  time.Now(),
	}
	j.Tasks = append(j.Tasks, task)

	log.Printf("[EXEC] Job %s started, pid=%d", j.ID, cmd.Process.Pid)

	// Monitor in background
	go e.waitForCompletion(j, cmd, stdout, stderr, task)

	return nil
}

// waitForCompletion waits for the job process to finish.
func (e *Executor) waitForCompletion(j *Job, cmd *exec.Cmd, stdout, stderr *os.File, task *Task) {
	err := cmd.Wait()
	stdout.Close()
	stderr.Close()

	j.Mu.Lock()
	defer j.Mu.Unlock()

	j.EndTime = time.Now()

	// Calculate wall time
	j.ResourceUse.WallTime = j.EndTime.Sub(j.StartTime)

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			j.ExitStatus = exitErr.ExitCode()
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				if status.Signaled() {
					j.ExitSignal = int(status.Signal())
				}
			}
		} else {
			j.ExitStatus = -1
		}
	} else {
		j.ExitStatus = 0
	}

	task.State = TaskExited
	task.ExitCode = j.ExitStatus
	task.Ended = time.Now()

	j.State = StateExiting
	j.Substate = SubstateExiting

	log.Printf("[EXEC] Job %s finished, exit=%d, walltime=%s",
		j.ID, j.ExitStatus, j.ResourceUse.WallTime)
}

// KillJob kills a running job.
func (e *Executor) KillJob(j *Job, signal syscall.Signal) error {
	j.Mu.RLock()
	proc := j.Process
	sid := j.SessionID
	j.Mu.RUnlock()

	if proc == nil {
		return fmt.Errorf("job %s has no process", j.ID)
	}

	log.Printf("[EXEC] Killing job %s (signal=%d, sid=%d)", j.ID, signal, sid)

	if runtime.GOOS != "windows" {
		// Kill entire process group/session
		return syscall.Kill(-sid, signal)
	}
	// On Windows, just kill the process
	return proc.Kill()
}

// buildEnvironment creates the environment variables for a job.
func (e *Executor) buildEnvironment(j *Job) []string {
	env := os.Environ()

	// Add PBS standard variables
	pbsVars := map[string]string{
		"PBS_JOBID":      j.ID,
		"PBS_JOBNAME":    j.Name,
		"PBS_QUEUE":      j.Queue,
		"PBS_O_HOME":     os.Getenv("HOME"),
		"PBS_O_LOGNAME":  j.User,
		"PBS_O_SHELL":    j.Shell,
		"PBS_O_WORKDIR":  os.Getenv("HOME"),
		"PBS_ENVIRONMENT": "PBS_BATCH",
		"PBS_NODEFILE":   filepath.Join(e.pbsHome, "aux", j.ID),
		"PBS_O_QUEUE":    j.Queue,
	}

	if j.ExecHost != "" {
		pbsVars["PBS_NODELIST"] = j.ExecHost
	}

	for k, v := range pbsVars {
		env = append(env, k+"="+v)
	}

	// Add user-specified variables
	for k, v := range j.VariableList {
		env = append(env, k+"="+v)
	}

	return env
}

// getWorkDir returns the working directory for a job.
func (e *Executor) getWorkDir(j *Job) string {
	if wd, ok := j.VariableList["PBS_O_WORKDIR"]; ok {
		return wd
	}
	if home, ok := j.VariableList["HOME"]; ok {
		return home
	}
	return os.Getenv("HOME")
}

// CleanupJob removes job spool files from disk.
func (e *Executor) CleanupJob(j *Job) {
	files := []string{
		filepath.Join(e.jobsDir, j.ID+".SC"),
		filepath.Join(e.jobsDir, j.ID+".JB"),
		filepath.Join(e.spoolDir, j.ID+".OU"),
		filepath.Join(e.spoolDir, j.ID+".ER"),
	}
	for _, f := range files {
		if f != "" {
			os.Remove(f)
		}
	}
	// Remove node file
	os.Remove(filepath.Join(e.pbsHome, "aux", j.ID))

	log.Printf("[EXEC] Cleaned up job %s files", j.ID)
}

// DeliverOutput copies spool stdout/stderr to the final output paths.
func (e *Executor) DeliverOutput(j *Job) {
	j.Mu.RLock()
	stdout := j.StdoutPath
	stderr := j.StderrPath
	finalOut := j.FinalStdout
	finalErr := j.FinalStderr
	j.Mu.RUnlock()

	if finalOut != "" && stdout != finalOut {
		if err := copyFile(stdout, finalOut); err != nil {
			log.Printf("[EXEC] Failed to deliver stdout for %s: %v", j.ID, err)
		}
	}
	if finalErr != "" && stderr != finalErr {
		if err := copyFile(stderr, finalErr); err != nil {
			log.Printf("[EXEC] Failed to deliver stderr for %s: %v", j.ID, err)
		}
	}
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		if os.IsNotExist(err) {
			data = []byte{}
		} else {
			return err
		}
	}
	return os.WriteFile(dst, data, 0644)
}

// CreateNodeFile writes the PBS node file for a job.
func (e *Executor) CreateNodeFile(j *Job) error {
	nodefile := filepath.Join(e.pbsHome, "aux", j.ID)
	os.MkdirAll(filepath.Dir(nodefile), 0755)

	host := j.ExecHost
	if host == "" {
		hostname, _ := os.Hostname()
		host = hostname
	}

	// Parse exec_host format: "host1/0+host2/1"
	var nodes []string
	for _, part := range strings.Split(host, "+") {
		h := strings.Split(part, "/")[0]
		nodes = append(nodes, h)
	}

	content := strings.Join(nodes, "\n") + "\n"
	return os.WriteFile(nodefile, []byte(content), 0644)
}

// stripHostPrefix removes the "hostname:" prefix from PBS paths like "DevBox:/path/to/file"
func stripHostPrefix(path string) string {
if idx := strings.Index(path, ":"); idx >= 0 {
rest := path[idx+1:]
if len(rest) > 0 && rest[0] == '/' {
return rest
}
}
return path
}
