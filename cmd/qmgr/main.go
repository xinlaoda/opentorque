// Command qmgr is the PBS batch system manager for administrative operations.
// It supports creating, deleting, and configuring queues, servers, and nodes.
//
// Usage:
//
//	qmgr [-c command]       Execute a single command
//	qmgr                    Interactive mode
//
// Commands:
//
//	create queue <name> [queue_type=execution|route]
//	delete queue <name>
//	set queue <name> <attribute> = <value>
//	set server <attribute> = <value>
//	list queue <name>
//	list server
//	print server
//	active queue <name>
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/xinlaoda/opentorque/internal/cli/client"
	"github.com/xinlaoda/opentorque/internal/cli/dis"
)

func main() {
	var (
		command = flag.String("c", "", "Execute a single command")
		server  = flag.String("s", "", "Specify server name")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: qmgr [-c command] [-s server]\n\nOptions:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nCommands:\n")
		fmt.Fprintf(os.Stderr, "  create queue <name>\n")
		fmt.Fprintf(os.Stderr, "  delete queue <name>\n")
		fmt.Fprintf(os.Stderr, "  set queue <name> <attr> = <value>\n")
		fmt.Fprintf(os.Stderr, "  set server <attr> = <value>\n")
		fmt.Fprintf(os.Stderr, "  list queue <name>\n")
		fmt.Fprintf(os.Stderr, "  list server\n")
		fmt.Fprintf(os.Stderr, "  print server\n")
	}
	flag.Parse()

	conn, err := client.Connect(*server)
	if err != nil {
		fmt.Fprintf(os.Stderr, "qmgr: cannot connect to server: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	if *command != "" {
		// Single command mode
		if err := executeCommand(conn, *command); err != nil {
			fmt.Fprintf(os.Stderr, "qmgr: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Interactive mode
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Print("Qmgr: ")
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			fmt.Print("Qmgr: ")
			continue
		}
		if line == "quit" || line == "exit" || line == "q" {
			break
		}
		if err := executeCommand(conn, line); err != nil {
			fmt.Fprintf(os.Stderr, "qmgr: %v\n", err)
		}
		fmt.Print("Qmgr: ")
	}
}

// executeCommand parses and executes a single qmgr command.
func executeCommand(conn *client.Conn, cmdLine string) error {
	parts := tokenize(cmdLine)
	if len(parts) < 2 {
		return fmt.Errorf("invalid command: %s", cmdLine)
	}

	verb := strings.ToLower(parts[0])
	objTypeStr := strings.ToLower(parts[1])

	// Map verb to manager command
	var mgrCmd int
	switch verb {
	case "create", "c":
		mgrCmd = dis.MgrCmdCreate
	case "delete", "d":
		mgrCmd = dis.MgrCmdDelete
	case "set", "s":
		mgrCmd = dis.MgrCmdSet
	case "unset", "u":
		mgrCmd = dis.MgrCmdUnset
	case "list", "l":
		mgrCmd = dis.MgrCmdList
	case "print", "p":
		mgrCmd = dis.MgrCmdPrint
	default:
		return fmt.Errorf("unknown command: %s", verb)
	}

	// Map object type
	var objType int
	switch objTypeStr {
	case "server", "s":
		objType = dis.MgrObjServer
	case "queue", "que", "q":
		objType = dis.MgrObjQueue
	case "node", "n":
		objType = dis.MgrObjNode
	default:
		return fmt.Errorf("unknown object type: %s", objTypeStr)
	}

	// Object name (for queue/node operations)
	objName := ""
	attrStart := 2
	if objType != dis.MgrObjServer && len(parts) > 2 {
		objName = parts[2]
		attrStart = 3
	}

	// For list/print commands, display status instead of using Manager request
	if mgrCmd == dis.MgrCmdList || mgrCmd == dis.MgrCmdPrint {
		return listObject(conn, objType, objName)
	}

	// Parse attributes (key = value pairs)
	attrs := parseAttrs(parts[attrStart:])

	return conn.Manager(mgrCmd, objType, objName, attrs)
}

// listObject handles "list" and "print" commands by querying status.
func listObject(conn *client.Conn, objType int, name string) error {
	var objects []client.StatusObject
	var err error

	switch objType {
	case dis.MgrObjServer:
		objects, err = conn.StatusServer()
	case dis.MgrObjQueue:
		objects, err = conn.StatusQueue(name)
	case dis.MgrObjNode:
		objects, err = conn.StatusNode(name)
	default:
		return fmt.Errorf("unsupported list for object type %d", objType)
	}
	if err != nil {
		return err
	}

	for _, obj := range objects {
		objTypeLabel := "Server"
		switch objType {
		case dis.MgrObjQueue:
			objTypeLabel = "Queue"
		case dis.MgrObjNode:
			objTypeLabel = "Node"
		}
		fmt.Printf("%s: %s\n", objTypeLabel, obj.Name)
		for _, attr := range obj.Attrs {
			if attr.HasResc && attr.Resc != "" {
				fmt.Printf("    %s.%s = %s\n", attr.Name, attr.Resc, attr.Value)
			} else {
				fmt.Printf("    %s = %s\n", attr.Name, attr.Value)
			}
		}
	}
	return nil
}

// parseAttrs parses "attr = value" tokens into svrattrl list.
func parseAttrs(tokens []string) []dis.SvrAttrl {
	// Join remaining tokens and split by commas for multiple attrs
	remaining := strings.Join(tokens, " ")
	if remaining == "" {
		return nil
	}

	var attrs []dis.SvrAttrl
	for _, part := range strings.Split(remaining, ",") {
		part = strings.TrimSpace(part)
		eqIdx := strings.Index(part, "=")
		if eqIdx < 0 {
			continue
		}
		key := strings.TrimSpace(part[:eqIdx])
		val := strings.TrimSpace(part[eqIdx+1:])

		attr := dis.SvrAttrl{Name: key, Value: val, Op: 1} // SET
		// Check for resource sub-attribute (e.g., resources_max.walltime)
		if dotIdx := strings.Index(key, "."); dotIdx > 0 {
			attr.Name = key[:dotIdx]
			attr.HasResc = true
			attr.Resc = key[dotIdx+1:]
		}
		attrs = append(attrs, attr)
	}
	return attrs
}

// tokenize splits a command line into tokens, respecting quoted strings.
func tokenize(s string) []string {
	var tokens []string
	var current strings.Builder
	inQuote := false
	quoteChar := byte(0)

	for i := 0; i < len(s); i++ {
		c := s[i]
		if inQuote {
			if c == quoteChar {
				inQuote = false
			} else {
				current.WriteByte(c)
			}
		} else if c == '\'' || c == '"' {
			inQuote = true
			quoteChar = c
		} else if c == ' ' || c == '\t' {
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		} else {
			current.WriteByte(c)
		}
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}
