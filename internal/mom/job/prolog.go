package job

import (
	"log"
	"os"
	"os/exec"
	"runtime"
	"time"
)

// RunProlog runs the system and user prologue scripts.
func (e *Executor) RunProlog(j *Job, prologPath, userPrologPath string, timeout int) error {
	// Run system prolog first
	if prologPath != "" {
		if _, err := os.Stat(prologPath); err == nil {
			log.Printf("[PROLOG] Running system prologue for job %s: %s", j.ID, prologPath)
			if err := e.runPelogScript(j, prologPath, timeout); err != nil {
				return err
			}
		}
	}

	// Run user prolog
	if userPrologPath != "" {
		if _, err := os.Stat(userPrologPath); err == nil {
			log.Printf("[PROLOG] Running user prologue for job %s: %s", j.ID, userPrologPath)
			if err := e.runPelogScript(j, userPrologPath, timeout); err != nil {
				return err
			}
		}
	}

	return nil
}

// RunEpilog runs the user and system epilogue scripts.
func (e *Executor) RunEpilog(j *Job, epilogPath, userEpilogPath string, timeout int) error {
	// Run user epilog first
	if userEpilogPath != "" {
		if _, err := os.Stat(userEpilogPath); err == nil {
			log.Printf("[EPILOG] Running user epilogue for job %s: %s", j.ID, userEpilogPath)
			if err := e.runPelogScript(j, userEpilogPath, timeout); err != nil {
				log.Printf("[EPILOG] User epilogue failed for job %s: %v", j.ID, err)
				// Continue even if user epilog fails
			}
		}
	}

	// Run system epilog
	if epilogPath != "" {
		if _, err := os.Stat(epilogPath); err == nil {
			log.Printf("[EPILOG] Running system epilogue for job %s: %s", j.ID, epilogPath)
			if err := e.runPelogScript(j, epilogPath, timeout); err != nil {
				log.Printf("[EPILOG] System epilogue failed for job %s: %v", j.ID, err)
			}
		}
	}

	return nil
}

// runPelogScript executes a prologue/epilogue script with timeout.
func (e *Executor) runPelogScript(j *Job, scriptPath string, timeout int) error {
	if timeout <= 0 {
		timeout = 300
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd.exe", "/c", scriptPath)
	} else {
		cmd = exec.Command(scriptPath)
	}

	// Set PBS environment variables
	cmd.Env = append(os.Environ(),
		"PBS_JOBID="+j.ID,
		"PBS_JOBNAME="+j.Name,
		"PBS_QUEUE="+j.Queue,
		"PBS_O_LOGNAME="+j.User,
	)

	// Arguments: jobid username group jobname [resource limits] [queue]
	j.Mu.RLock()
	cmd.Args = append(cmd.Args, j.ID, j.User, j.Group, j.Name)
	j.Mu.RUnlock()

	// Set timeout
	done := make(chan error, 1)
	go func() {
		done <- cmd.Run()
	}()

	select {
	case err := <-done:
		return err
	case <-time.After(time.Duration(timeout) * time.Second):
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		return &PelogTimeoutError{Script: scriptPath, Timeout: timeout}
	}
}

// PelogTimeoutError is returned when a prolog/epilog script times out.
type PelogTimeoutError struct {
	Script  string
	Timeout int
}

func (e *PelogTimeoutError) Error() string {
	return "prolog/epilog script " + e.Script + " timed out after " + string(rune(e.Timeout)) + "s"
}
