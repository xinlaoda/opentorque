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
	"strings"

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
		mailOpts   = flag.String("m", "", "Mail notification options (a=abort, b=begin, e=end, n=none)")
		mailUser   = flag.String("M", "", "Mail recipient list")
		workDir    = flag.String("d", "", "Working directory for the job")
		varList    = flag.String("v", "", "Environment variable list")
		exportAll  = flag.Bool("V", false, "Export all environment variables")
		server     = flag.String("s", "", "Specify server name")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: qsub [options] [script_file]\n\nOptions:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

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

	// Build job attributes
	attrs := buildAttrs(*name, *resources, *outPath, *errPath, *joinOutput,
		*mailOpts, *mailUser, *workDir, *varList, *exportAll)

	conn, err := client.Connect(*server)
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

	fmt.Println(jobID)
}

// buildAttrs constructs the svrattrl list from command-line options.
func buildAttrs(name, resources, outPath, errPath, join, mail, mailUser, workDir, varList string, exportAll bool) []dis.SvrAttrl {
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
	if workDir != "" {
		attrs = append(attrs, dis.SvrAttrl{
			Name:    "Variable_List",
			HasResc: false,
			Value:   "PBS_O_WORKDIR=" + workDir,
			Op:      op,
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
		if shell := os.Getenv("SHELL"); shell != "" {
			vars = append(vars, "PBS_O_SHELL="+shell)
		}
		attrs = append(attrs, dis.SvrAttrl{Name: "Variable_List", Value: strings.Join(vars, ","), Op: op})
	}

	return attrs
}
