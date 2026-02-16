// Command pbsnodes displays and manages PBS compute node status.
//
// Usage:
//
//	pbsnodes [options] [node_name...]
//	pbsnodes -a          List all nodes
//	pbsnodes -l          List down/offline nodes
//	pbsnodes -o node     Mark node offline
//	pbsnodes -c node     Clear offline/down status
//	pbsnodes -r node     Reset node (clear offline)
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/xinlaoda/opentorque/internal/cli/client"
	"github.com/xinlaoda/opentorque/internal/cli/dis"
)

func main() {
	var (
		listAll     = flag.Bool("a", false, "List all nodes")
		listDown    = flag.Bool("l", false, "List down/offline nodes")
		markOffline = flag.Bool("o", false, "Mark node(s) offline")
		clearNode   = flag.Bool("c", false, "Clear offline/down status")
		resetNode   = flag.Bool("r", false, "Reset node (clear offline)")
		quiet       = flag.Bool("q", false, "Quiet mode")
		xmlOutput   = flag.Bool("x", false, "XML output format")
		note        = flag.String("N", "", "Set a note on the node")
		server      = flag.String("s", "", "Specify server name")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: pbsnodes [options] [node_name...]\n\nOptions:\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	_ = quiet
	_ = xmlOutput

	conn, err := client.Connect(*server)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pbsnodes: cannot connect to server: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	switch {
	case *markOffline:
		if flag.NArg() < 1 {
			fmt.Fprintf(os.Stderr, "pbsnodes: no node specified\n")
			os.Exit(1)
		}
		for _, node := range flag.Args() {
			attrs := []dis.SvrAttrl{{Name: "state", Value: "offline", Op: 1}}
			if err := conn.Manager(dis.MgrCmdSet, dis.MgrObjNode, node, attrs); err != nil {
				fmt.Fprintf(os.Stderr, "pbsnodes: %s: %v\n", node, err)
			}
		}

	case *clearNode, *resetNode:
		if flag.NArg() < 1 {
			fmt.Fprintf(os.Stderr, "pbsnodes: no node specified\n")
			os.Exit(1)
		}
		for _, node := range flag.Args() {
			attrs := []dis.SvrAttrl{{Name: "state", Value: "free", Op: 1}}
			if err := conn.Manager(dis.MgrCmdSet, dis.MgrObjNode, node, attrs); err != nil {
				fmt.Fprintf(os.Stderr, "pbsnodes: %s: %v\n", node, err)
			}
		}

	case *note != "":
		if flag.NArg() < 1 {
			fmt.Fprintf(os.Stderr, "pbsnodes: no node specified\n")
			os.Exit(1)
		}
		for _, node := range flag.Args() {
			attrs := []dis.SvrAttrl{{Name: "note", Value: *note, Op: 1}}
			if err := conn.Manager(dis.MgrCmdSet, dis.MgrObjNode, node, attrs); err != nil {
				fmt.Fprintf(os.Stderr, "pbsnodes: %s: %v\n", node, err)
			}
		}

	default:
		// Status display mode
		nodeID := ""
		if flag.NArg() > 0 && !*listAll {
			nodeID = flag.Arg(0)
		}
		objects, err := conn.StatusNode(nodeID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "pbsnodes: %v\n", err)
			os.Exit(1)
		}
		displayNodes(objects, *listDown)
	}
}

func displayNodes(objects []client.StatusObject, downOnly bool) {
	for _, obj := range objects {
		attrs := make(map[string]string)
		for _, a := range obj.Attrs {
			key := a.Name
			if a.HasResc && a.Resc != "" {
				key = a.Name + "." + a.Resc
			}
			attrs[key] = a.Value
		}

		state := attrs["state"]
		if downOnly {
			if !strings.Contains(state, "down") && !strings.Contains(state, "offline") {
				continue
			}
		}

		fmt.Println(obj.Name)
		for _, a := range obj.Attrs {
			if a.HasResc && a.Resc != "" {
				fmt.Printf("     %s.%s = %s\n", a.Name, a.Resc, a.Value)
			} else {
				fmt.Printf("     %s = %s\n", a.Name, a.Value)
			}
		}
		fmt.Println()
	}
}
