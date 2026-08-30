package server

import (
	"context"
	"os"
	"testing"
)

// TestPostgresStoreRoundTrip exercises the PostgreSQL store end-to-end. It is
// skipped unless PBS_PG_TEST_DSN is set (used on a machine with a local PG), so
// the normal dependency-free `go test ./...` still passes everywhere.
func TestPostgresStoreRoundTrip(t *testing.T) {
	dsn := os.Getenv("PBS_PG_TEST_DSN")
	if dsn == "" {
		t.Skip("set PBS_PG_TEST_DSN to run the PostgreSQL store test")
	}
	st, err := NewPostgresStore(dsn)
	if err != nil {
		t.Fatalf("NewPostgresStore: %v", err)
	}
	defer st.Close()

	if err := st.SaveServerDB([]byte("serverdb")); err != nil {
		t.Fatalf("SaveServerDB: %v", err)
	}
	if b, err := st.LoadServerDB(); err != nil || string(b) != "serverdb" {
		t.Fatalf("serverdb round-trip: %q err=%v", b, err)
	}

	if err := st.SaveQueue("batch", []byte("queue-data")); err != nil {
		t.Fatalf("SaveQueue: %v", err)
	}
	qs, err := st.LoadQueues()
	if err != nil || string(qs["batch"]) != "queue-data" {
		t.Fatalf("queue round-trip: %q err=%v", qs["batch"], err)
	}

	if err := st.SaveNodes([]byte("node1 np=2")); err != nil {
		t.Fatalf("SaveNodes: %v", err)
	}
	if b, err := st.LoadNodes(); err != nil || string(b) != "node1 np=2" {
		t.Fatalf("nodes round-trip: %q err=%v", b, err)
	}

	if err := st.SaveJob("1.srv", []byte("attrs")); err != nil {
		t.Fatalf("SaveJob: %v", err)
	}
	if err := st.SaveJobScript("1.srv", []byte("script")); err != nil {
		t.Fatalf("SaveJobScript: %v", err)
	}
	if err := st.SaveJob("1.srv", []byte("attrs2")); err != nil { // must not clobber script
		t.Fatalf("SaveJob #2: %v", err)
	}
	jobs, err := st.LoadJobs()
	if err != nil || string(jobs["1.srv"]) != "attrs2" {
		t.Fatalf("jobs round-trip: %q err=%v", jobs["1.srv"], err)
	}
	// verify the script column is still intact (attrs upsert didn't clobber it)
	var script []byte
	if err := st.pool.QueryRow(context.Background(), "SELECT script FROM ot_jobs WHERE id=$1", "1.srv").Scan(&script); err != nil {
		t.Fatalf("read script: %v", err)
	}
	if string(script) != "script" {
		t.Fatalf("script clobbered by attrs upsert: %q", script)
	}

	if err := st.DeleteJob("1.srv"); err != nil {
		t.Fatalf("DeleteJob: %v", err)
	}
	jobs, _ = st.LoadJobs()
	if _, ok := jobs["1.srv"]; ok {
		t.Fatal("job not deleted")
	}
}
