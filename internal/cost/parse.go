package cost

// Accounting-record parser. Reads OpenTorque accounting E (End) lines of the
// form produced by internal/acct:
//
//	MM/DD/YYYY HH:MM:SS;E;JOB_ID;user=... start=1234 end=5678 exec_host=node/0 account=projA resources_used.ncpus=2
//
// and turns each into a Usage (account, node, core-seconds = ncpus x (end-start)).
// Node is taken from exec_host (the part before "/"); a multi-node exec_host
// ("n1/0+n2/0") spreads the job's core-seconds evenly across its nodes.

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// ParseAccounting reads E (end) accounting records and returns the usage they
// imply. Lines that are not E records, or that lack start/end, are skipped.
func ParseAccounting(r io.Reader) ([]Usage, error) {
	var out []Usage
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		parts := strings.Split(line, ";")
		if len(parts) < 4 || parts[1] != "E" {
			continue
		}
		kv := parseKeyValues(parts[3])
		startS, ok1 := kv["start"]
		endS, ok2 := kv["end"]
		if !ok1 || !ok2 {
			continue
		}
		start, err1 := strconv.ParseFloat(startS, 64)
		end, err2 := strconv.ParseFloat(endS, 64)
		if err1 != nil || err2 != nil || end <= start {
			continue
		}
		n := 1.0
		if v, ok := kv["resources_used.ncpus"]; ok {
			if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
				n = f
			}
		} else if v, ok := kv["Resource_List.ncpus"]; ok {
			if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
				n = f
			}
		}
		coreSec := n * (end - start)
		acct := kv["account"]
		nodes := nodesFromExecHost(kv["exec_host"])
		per := coreSec / float64(len(nodes))
		for _, node := range nodes {
			out = append(out, Usage{Account: acct, Node: node, CoreSeconds: per})
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("cost: read accounting: %w", err)
	}
	return out, nil
}

// nodesFromExecHost turns "n1/0+n2/0" into ["n1","n2"].
func nodesFromExecHost(execHost string) []string {
	if execHost == "" {
		return []string{""}
	}
	var nodes []string
	for _, tok := range strings.Split(execHost, "+") {
		tok = strings.TrimSpace(tok)
		if idx := strings.Index(tok, "/"); idx >= 0 {
			tok = tok[:idx]
		}
		if tok != "" {
			nodes = append(nodes, tok)
		}
	}
	if len(nodes) == 0 {
		return []string{""}
	}
	return nodes
}

// parseKeyValues splits "user=x group=y exec_host=n/0" into a map. Values are
// expected to be space-free (account, exec_host, ncpus, start/end all are).
func parseKeyValues(msg string) map[string]string {
	m := make(map[string]string)
	for _, tok := range strings.Fields(msg) {
		if i := strings.Index(tok, "="); i > 0 {
			m[tok[:i]] = tok[i+1:]
		}
	}
	return m
}