// Command qsub submits a batch job to the PBS server.
//
// Usage:
//
//	qsub [options] [script_file]
//	echo "command" | qsub [options]
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/user"
	"strconv"
	"strings"
	"time"

	"github.com/xinlaoda/opentorque/internal/cli/client"
	"github.com/xinlaoda/opentorque/internal/cli/dis"
)

func main() {
	var (
		queue      = flag.String("q", "", "Destination queue")
		name       = flag.String("N", "", "Job name")
		resources  = flag.String("l", "", "Resource list (e.g., nodes=1:ppn=4,walltime=01:00:00)")
		outPath    = flag.String("o", "", "Path for stdout")
		errPath    = flag.String("e", "", "Path for stderr")
		joinOutput = flag.String("j", "", "Join stdout/stderr (oe or eo)")
		mailOpts   = flag.String("m", "", "Mail events (a=abort, b=begin, e=end, n=none)")
		mailUser   = flag.String("M", "", "Mail recipient list")
		workDir    = flag.String("d", "", "Working directory for the job")
		varList    = flag.String("v", "", "Environment variable list")
		exportAll  = flag.Bool("V", false, "Export all environment variables")
		serverName = flag.String("s", "", "Specify server name")

		// Newly added options matching C TORQUE
		execTime   = flag.String("a", "", "Deferred execution time (YYYYMMDDHHmm[.SS])")
		account    = flag.String("A", "", "Account string for job accounting")
		checkpoint = flag.String("c", "", "Checkpoint options (none, enabled, interval, shutdown, periodic)")
		dirPrefix  = flag.String("C", "", "Directive prefix in script (default #PBS)")
		rootDir    = flag.String("D", "", "Root directory path (chroot)")
		faultTol   = flag.Bool("f", false, "Fault tolerant job")
		scriptArgs = flag.String("F", "", "Arguments passed to job script")
		holdJob    = flag.Bool("h", false, "Place user hold on job at submission")
		keepFiles  = flag.String("k", "", "Keep stdout/stderr on exec host (n, o, e, oe, eo)")
		priority   = flag.Int("p", 0, "Job priority (-1024 to 1023)")
		rerunnable = flag.String("r", "", "Job is rerunnable (y or n)")
		shell      = flag.String("S", "", "Shell to use for the job script")
		arrayReq   = flag.String("t", "", "Job array range (e.g., 0-9, 1-100%5)")
		userList   = flag.String("u", "", "User list for job ownership")
		extraAttrs = flag.String("W", "", "Additional attributes (key=value,key=value)")
		quiet      = flag.Bool("z", false, "Do not print job ID after submission")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: qsub [options] [script_file]\n\nOptions:\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	_ = dirPrefix // consumed during script parsing if needed

	// Read job script from file or stdin
	var script string
	if flag.NArg() > 0 {
		data, err := os.ReadFile(flag.Arg(0))
		if err != nil {
			fmt.Fprintf(os.Stderr, "qsub: cannot read script file: %v\n", err)
			os.Exit(1)
		}
		script = string(data)
		if *name == "" {
			*name = flag.Arg(0)
		}
	} else {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "qsub: cannot read stdin: %v\n", err)
			os.Exit(1)
		}
		script = string(data)
		if *name == "" {
			*name = "STDIN"
		}
	}

	// Validate priority range
	if *priority < -1024 || *priority > 1023 {
		fmt.Fprintf(os.Stderr, "qsub: priority must be between -1024 and 1023\n")
		os.Exit(1)
	}

	// Validate execution time format
	if *execTime != "" {
		if _, err := parseExecTime(*execTime); err != nil {
			fmt.Fprintf(os.Stderr, "qsub: invalid execution time: %v\n", err)
			os.Exit(1)
		}
	}

	// Append script arguments to script if provided (-F)
	if *scriptArgs != "" {
		// Inject arguments as positional parameters at script top
		script = "set -- " + *scriptArgs + "\n" + script
	}

	// Build job attributes
	attrs := buildAttrs(*name, *resources, *outPath, *errPath, *joinOutput,
		*mailOpts, *mailUser, *workDir, *varList, *exportAll,
		*account, *checkpoint, *keepFiles, *shell, *rerunnable,
		*execTime, *priority, *holdJob, *faultTol, *arrayReq,
		*userList, *rootDir, *extraAttrs)

	conn, err := client.Connect(*serverName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "qsub: cannot connect to server: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	dest := *queue
	jobID, err := conn.SubmitJob(dest, attrs, script)
	if err != nil {
		fmt.Fprintf(os.Stderr, "qsub: %v\n", err)
		os.Exit(1)
	}

	if !*quiet {
		fmt.Println(jobID)
	}
}

// parseExecTime parses TORQUE date_time format: [[[[CC]YY]MM]DD]hhmm[.SS]
func parseExecTime(s string) (time.Time, error) {
	// Try full format first: YYYYMMDDHHmm.SS
	for _, layout := range []string{
		"200601021504.05",
		"200601021504",
		"01021504.05",
		"01021504",
		"021504",
		"1504",
	} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			// If year is zero, use current year
			if t.Year() == 0 {
				now := time.Now()
				t = t.AddDate(now.Year(), 0, 0)
			}
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("expected format [[[[CC]YY]MM]DD]hhmm[.SS], got %q", s)
}

// buildAttrs constructs the svrattrl list from command-line options.
func buildAttrs(name, resources, outPath, errPath, join, mail, mailUser,
	workDir, varList string, exportAll bool,
	account, checkpoint, keepFiles, shell, rerunnable, execTime string,
	priority int, holdJob, faultTol bool,
	arrayReq, userList, rootDir, extraAttrs string) []dis.SvrAttrl {

	var attrs []dis.SvrAttrl
	op := 1 // SET

	if name != "" {
		attrs = append(attrs, dis.SvrAttrl{Name: "Job_Name", Value: name, Op: op})
	}

	// Parse resource list (comma-separated key=value pairs)
	if resources != "" {
		for _, res := range strings.Split(resources, ",") {
			parts := strings.SplitN(strings.TrimSpace(res), "=", 2)
			if len(parts) == 2 {
				attrs = append(attrs, dis.SvrAttrl{
					Name:    "Resource_List",
					HasResc: true,
					Resc:    parts[0],
					Value:   parts[1],
					Op:      op,
				})
			}
		}
	}

	if outPath != "" {
		attrs = append(attrs, dis.SvrAttrl{Name: "Output_Path", Value: outPath, Op: op})
	}
	if errPath != "" {
		attrs = append(attrs, dis.SvrAttrl{Name: "Error_Path", Value: errPath, Op: op})
	}
	if join != "" {
		attrs = append(attrs, dis.SvrAttrl{Name: "Join_Path", Value: join, Op: op})
	}
	if mail != "" {
		attrs = append(attrs, dis.SvrAttrl{Name: "Mail_Points", Value: mail, Op: op})
	}
	if mailUser != "" {
		attrs = append(attrs, dis.SvrAttrl{Name: "Mail_Users", Value: mailUser, Op: op})
	}
	if account != "" {
		attrs = append(attrs, dis.SvrAttrl{Name: "Account_Name", Value: account, Op: op})
	}
	if checkpoint != "" {
		attrs = append(attrs, dis.SvrAttrl{Name: "Checkpoint", Value: checkpoint, Op: op})
	}
	if keepFiles != "" {
		attrs = append(attrs, dis.SvrAttrl{Name: "Keep_Files", Value: keepFiles, Op: op})
	}
	if shell != "" {
		attrs = append(attrs, dis.SvrAttrl{Name: "Shell_Path_List", Value: shell, Op: op})
	}
	if rerunnable != "" {
		attrs = append(attrs, dis.SvrAttrl{Name: "Rerunable", Value: rerunnable, Op: op})
	}
	if execTime != "" {
		t, _ := parseExecTime(execTime)
		attrs = append(attrs, dis.SvrAttrl{Name: "Execution_Time", Value: strconv.FormatInt(t.Unix(), 10), Op: op})
	}
	if priority != 0 {
		attrs = append(attrs, dis.SvrAttrl{Name: "Priority", Value: strconv.Itoa(priority), Op: op})
	}
	if holdJob {
		attrs = append(attrs, dis.SvrAttrl{Name: "Hold_Types", Value: "u", Op: op})
	}
	if faultTol {
		attrs = append(attrs, dis.SvrAttrl{Name: "fault_tolerant", Value: "True", Op: op})
	}
	if arrayReq != "" {
		attrs = append(attrs, dis.SvrAttrl{Name: "job_array_request", Value: arrayReq, Op: op})
	}
	if userList != "" {
		attrs = append(attrs, dis.SvrAttrl{Name: "User_List", Value: userList, Op: op})
	}
	if rootDir != "" {
		attrs = append(attrs, dis.SvrAttrl{Name: "init_root_dir", Value: rootDir, Op: op})
	}

	// Parse -W additional attributes (key=value,key=value)
	if extraAttrs != "" {
		for _, kv := range strings.Split(extraAttrs, ",") {
			parts := strings.SplitN(strings.TrimSpace(kv), "=", 2)
			if len(parts) == 2 {
				key := strings.TrimSpace(parts[0])
				val := strings.TrimSpace(parts[1])
				switch key {
				case "depend":
					attrs = append(attrs, dis.SvrAttrl{Name: "depend", Value: val, Op: op})
				case "stagein":
					attrs = append(attrs, dis.SvrAttrl{Name: "stagein", Value: val, Op: op})
				case "stageout":
					attrs = append(attrs, dis.SvrAttrl{Name: "stageout", Value: val, Op: op})
				case "group_list":
					attrs = append(attrs, dis.SvrAttrl{Name: "group_list", Value: val, Op: op})
				default:
					attrs = append(attrs, dis.SvrAttrl{Name: key, Value: val, Op: op})
				}
			}
		}
	}

	// Build variable list with working directory handling
	if workDir != "" {
		attrs = append(attrs, dis.SvrAttrl{
			Name:  "Variable_List",
			Value: "PBS_O_WORKDIR=" + workDir,
			Op:    op,
		})
	}

	// Build variable list: always include PBS_O_HOME, PBS_O_WORKDIR, PBS_O_LOGNAME
	{
		var vars []string
		if varList != "" {
			vars = append(vars, varList)
		}
		if exportAll {
			for _, env := range os.Environ() {
				vars = append(vars, env)
			}
		}
		u, _ := user.Current()
		if u != nil {
			vars = append(vars, "PBS_O_HOME="+u.HomeDir)
			vars = append(vars, "PBS_O_LOGNAME="+u.Username)
		}
		wd, _ := os.Getwd()
		if wd != "" {
			vars = append(vars, "PBS_O_WORKDIR="+wd)
		}
		if sh := os.Getenv("SHELL"); sh != "" {
			vars = append(vars, "PBS_O_SHELL="+sh)
		}
		if hostName, _ := os.Hostname(); hostName != "" {
			vars = append(vars, "PBS_O_HOST="+hostName)
		}
		if path := os.Getenv("PATH"); path != "" {
			vars = append(vars, "PBS_O_PATH="+path)
		}
		attrs = append(attrs, dis.SvrAttrl{Name: "Variable_List", Value: strings.Join(vars, ","), Op: op})
	}

	return attrs
}
