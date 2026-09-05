package cost

import (
	"strings"
	"testing"
)

func TestParseAccountingBasics(t *testing.T) {
	in := strings.Join([]string{
		`01/02/2026 10:00:00;Q;1.srv;user=al user@h group=g jobname=x queue=batch`,
		`01/02/2026 10:00:05;E;1.srv;user=al group=g jobname=x queue=batch ctime=10 qtime=10 etime=10 start=1000 end=4600 Exit_status=0 exec_host=n1/0 account=projA resources_used.ncpus=2`,
		`01/02/2026 10:00:10;E;2.srv;user=bo group=g jobname=y queue=batch start=5000 end=8600 Exit_status=0 exec_host=n1/1 account=projB resources_used.ncpus=1`,
		`unparseable`,
	}, "\n")
	usages, err := ParseAccounting(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(usages) != 2 {
		t.Fatalf("got %d usages, want 2\n%+v", len(usages), usages)
	}
	// job1: 2 ncpus x 3600s = 7200 core-sec on n1
	if usages[0].Account != "projA" || usages[0].Node != "n1" || usages[0].CoreSeconds != 7200 {
		t.Fatalf("usage0 = %+v, want projA/n1/7200", usages[0])
	}
	// job2: 1 ncpu x 3600s = 3600 core-sec on n1
	if usages[1].Account != "projB" || usages[1].Node != "n1" || usages[1].CoreSeconds != 3600 {
		t.Fatalf("usage1 = %+v, want projB/n1/3600", usages[1])
	}
}

func TestParseAccountingMultiNodeSplitsEvenly(t *testing.T) {
	in := `01/02/2026 10:00:00;E;9.srv;user=al group=g jobname=x queue=batch start=1 end=601 Exit_status=0 exec_host=n1/0+n2/0 account=projA resources_used.ncpus=4`
	usages, err := ParseAccounting(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(usages) != 2 {
		t.Fatalf("got %d usages, want 2", len(usages))
	}
	// total 4 ncpus x 600s = 2400 core-sec split across 2 nodes = 1200 each
	if usages[0].Node != "n1" || usages[0].CoreSeconds != 1200 {
		t.Fatalf("u0 = %+v, want n1/1200", usages[0])
	}
	if usages[1].Node != "n2" || usages[1].CoreSeconds != 1200 {
		t.Fatalf("u1 = %+v, want n2/1200", usages[1])
	}
}

func TestParseAccountingSkipsNonEnd(t *testing.T) {
	in := strings.Join([]string{
		`01/02/2026 10:00:00;S;1.srv;user=al group=g jobname=x queue=batch start=1000 exec_host=n1/0`,
		`01/02/2026 10:00:00;E;2.srv;user=al group=g jobname=x queue=batch start=1000 Exit_status=0`, // no end -> skipped
	}, "\n")
	usages, err := ParseAccounting(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(usages) != 0 {
		t.Fatalf("got %d usages, want 0 (non-E and missing-end skipped)", len(usages))
	}
}
