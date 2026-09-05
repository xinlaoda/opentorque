// Command pbs_cost reports per-account cloud cost by apportioning each node's
// real (VM) bill to the accounts that used it, by share of used core-seconds.
//
//	Usage:
//	  pbs_cost -home /var/spool/torque -date 20260904 \
//	    -uptime-hours 'nodeA=24,nodeB=24' -node-sku 'nodeA=Standard_D2s_v3' \
//	    -price 'Standard_D2s_v3=0.096' -default-price 0.1
//	  # or give exact node bills directly:
//	  pbs_cost -home /var/spool/torque -date 20260904 -bill-usd 'nodeA=2.30,nodeB=2.30'
//
// The report shows, per account, used core-seconds and the apportioned cost in
// USD, plus any "overhead" (billed nodes with no usage).
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/xinlaoda/opentorque/internal/cost"
)

func main() {
	var (
		home        = flag.String("home", "/var/spool/torque", "PBS home (accounting at <home>/server_priv/accounting)")
		acctDir     = flag.String("accounting", "", "override accounting directory")
		date        = flag.String("date", time.Now().Format("20060102"), "accounting date YYYYMMDD")
		billUSD     = flag.String("bill-usd", "", "exact node bills: 'node=USD,node2=USD'")
		price       = flag.String("price", "", "SKU price map: 'sku=USD_per_hour,...'")
		nodeSKU     = flag.String("node-sku", "", "node SKU map: 'node=sku,...'")
		uptimeHours = flag.String("uptime-hours", "", "node billed hours: 'node=H,...'")
		defPrice    = flag.Float64("default-price", 0.1, "default USD per VM-hour when a node's SKU price is unknown")
	)
	flag.Parse()

	dir := *acctDir
	if dir == "" {
		dir = filepath.Join(*home, "server_priv", "accounting")
	}
	path := filepath.Join(dir, *date)
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pbs_cost: open %s: %v\n", path, err)
		os.Exit(2)
	}
	defer f.Close()

	usages, err := cost.ParseAccounting(f)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pbs_cost: %v\n", err)
		os.Exit(2)
	}

	bills, overheadNotes := buildBills(nodeSet(usages), *billUSD, *price, *nodeSKU, *uptimeHours, *defPrice)
	al := cost.Allocate(usages, bills)

	printReport(al, usages, overheadNotes)
}

// nodeSet returns the set of node names appearing in usage plus any named in
// the bill inputs, so a billed-but-idle node is still shown as overhead.
func nodeSet(usages []cost.Usage) map[string]bool {
	set := map[string]bool{}
	for _, u := range usages {
		set[u.Node] = true
	}
	return set
}

func buildBills(nodeSet map[string]bool, billUSD, price, nodeSKU, uptimeHours string, defPrice float64) ([]cost.Bill, []string) {
	exact := parseFloatMap(billUSD)
	prices := parseFloatMap(price)
	skus := parseStringMap(nodeSKU)
	uptime := parseFloatMap(uptimeHours)

	var bills []cost.Bill
	var notes []string
	// union of nodes we know about
	names := map[string]bool{}
	for _, n := range []map[string]bool{nodeSet} {
		for k := range n {
			names[k] = true
		}
	}
	for _, m := range []map[string]float64{exact, uptime} {
		for k := range m {
			names[k] = true
		}
	}
	for k := range skus {
		names[k] = true
	}

	for node := range names {
		if node == "" {
			continue
		}
		if v, ok := exact[node]; ok {
			bills = append(bills, cost.Bill{Node: node, USD: v})
			continue
		}
		h := uptime[node]
		p := prices[skus[node]]
		if p == 0 {
			p = defPrice
			if _, seen := skus[node]; !seen {
				notes = append(notes, fmt.Sprintf("note: node %s has no SKU; used default price $%.3f/vh", node, defPrice))
			}
		}
		usd := h * p
		bills = append(bills, cost.Bill{Node: node, USD: usd})
	}
	return bills, notes
}

func printReport(al cost.Allocation, usages []cost.Usage, notes []string) {
	fmt.Printf("%-20s %14s %12s\n", "account", "core-seconds", "cost(USD)")
	fmt.Println(strings.Repeat("-", 48))
	accts := make([]string, 0, len(al.Accounts))
	for a := range al.Accounts {
		if al.Accounts[a] > 0 {
			accts = append(accts, a)
		}
	}
	sort.Strings(accts)
	for _, a := range accts {
		fmt.Printf("%-20s %14.0f %12.4f\n", a, al.CoreSeconds[a], al.Accounts[a])
	}
	if al.Overhead > 0 {
		fmt.Printf("%-20s %14s %12.4f\n", "(overhead)", "", al.Overhead)
	}
	total := 0.0
	for _, v := range al.Accounts {
		total += v
	}
	total += al.Overhead
	fmt.Println(strings.Repeat("-", 48))
	fmt.Printf("%-20s %14s %12.4f\n", "TOTAL", "", total)
	for _, n := range notes {
		fmt.Println(n)
	}
}

// parseFloatMap parses "a=1.5,b=2" into {a:1.5, b:2}.
func parseFloatMap(s string) map[string]float64 {
	m := map[string]float64{}
	for _, tok := range strings.Split(s, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		kv := strings.SplitN(tok, "=", 2)
		if len(kv) != 2 {
			continue
		}
		if v, err := strconv.ParseFloat(strings.TrimSpace(kv[1]), 64); err == nil {
			m[strings.TrimSpace(kv[0])] = v
		}
	}
	return m
}

// parseStringMap parses "a=x,b=y" into {a:x, b:y}.
func parseStringMap(s string) map[string]string {
	m := map[string]string{}
	for _, tok := range strings.Split(s, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		kv := strings.SplitN(tok, "=", 2)
		if len(kv) == 2 {
			m[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
		}
	}
	return m
}
