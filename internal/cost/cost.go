// Package cost implements cloud-native cost attribution / chargeback.
//
// Model: a cloud VM is billed for the whole time it is powered on (boot +
// execution + idle + drain), while a single (often shared) node runs jobs from
// one or many accounts. So we do NOT cost by "job walltime x unit price" (that
// undercounts the bill). Instead each node is a **cost pool**: the node's real
// billed USD is apportioned to the accounts that used it by their share of the
// node's used core-seconds. Idle/boot/drain time is thereby implicitly charged
// pro-rata to whoever used the node, and a node billed with zero usage goes to
// an "overhead" bucket. This reconciles with the cloud provider's bill at the
// node/VM granularity.
package cost

// Usage is a single job's resource consumption attributed to an account on a
// node, in core-seconds (ncpus x seconds).
type Usage struct {
	Account     string
	Node        string
	CoreSeconds float64
}

// Bill is a node's total billed cost (USD) for the reporting period.
type Bill struct {
	Node string
	USD  float64
}

// Allocation is the result of apportioning node bills to accounts.
type Allocation struct {
	// Accounts maps account -> apportioned USD.
	Accounts map[string]float64
	// Overhead is the total USD billed on nodes with no usage (pure idle /
	// wrongly-scaled empty nodes). It is attributable to the pool/queue that
	// brought the node up, not to any account.
	Overhead float64
	// ByNode maps node -> account -> apportioned USD.
	ByNode map[string]map[string]float64
	// CoreSeconds maps account -> total used core-seconds (usage, not cost).
	CoreSeconds map[string]float64
}

// Allocate apportions node bills to accounts by each account's share of the
// node's used core-seconds. Every dollar of a node's bill is allocated (idle
// is an implicit pro-rata tax) unless the node had no usage at all, in which
// case it lands in Overhead.
func Allocate(usages []Usage, bills []Bill) Allocation {
	used := make(map[string]map[string]float64) // node -> account -> core-seconds
	nodeTotal := make(map[string]float64)
	acctCore := make(map[string]float64)
	for _, u := range usages {
		if used[u.Node] == nil {
			used[u.Node] = make(map[string]float64)
		}
		used[u.Node][u.Account] += u.CoreSeconds
		nodeTotal[u.Node] += u.CoreSeconds
		acctCore[acctKey(u.Account)] += u.CoreSeconds
	}

	res := Allocation{
		Accounts:    make(map[string]float64),
		ByNode:      make(map[string]map[string]float64),
		CoreSeconds: acctCore,
	}
	for _, b := range bills {
		total := nodeTotal[b.Node]
		if total <= 0 {
			res.Overhead += b.USD
			continue
		}
		accts := used[b.Node]
		if res.ByNode[b.Node] == nil {
			res.ByNode[b.Node] = make(map[string]float64)
		}
		for acct, cs := range accts {
			amt := b.USD * (cs / total)
			res.Accounts[acctKey(acct)] += amt
			res.ByNode[b.Node][acct] += amt
		}
	}
	return res
}

func acctKey(a string) string {
	if a == "" {
		return "(unknown)"
	}
	return a
}
