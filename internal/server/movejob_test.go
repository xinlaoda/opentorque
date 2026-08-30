package server

import (
	"bytes"
	"net"
	"testing"

	"github.com/xinlaoda/opentorque/internal/config"
	"github.com/xinlaoda/opentorque/internal/dis"
	"github.com/xinlaoda/opentorque/internal/job"
	"github.com/xinlaoda/opentorque/internal/queue"
)

// newMoveServer builds a minimal Server with in-memory managers and a temp
// jobs dir so handleMoveJob can run without touching the real deployment.
func newMoveServer(t *testing.T) (*Server, *job.Manager, *queue.Manager) {
	t.Helper()
	cfg := &config.Config{
		JobsDir:    t.TempDir(),
		ServerName: "srv",
	}
	jm := job.NewManager("srv", 1)
	qm := queue.NewManager()
	s := &Server{
		cfg:      cfg,
		jobMgr:   jm,
		queueMgr: qm,
		store:    NewStore(cfg),
	}
	return s, jm, qm
}

// runMove executes handleMoveJob for the given jobID/dest on a net.Pipe pair
// and returns the reply code written to the client (0 on success).
func runMove(s *Server, jobID, dest string) int {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	var body bytes.Buffer
	bodyW := dis.NewWriter(&body)
	bodyW.WriteString(jobID)
	bodyW.WriteString(dest)
	bodyW.Flush()
	r := dis.NewReader(bytes.NewReader(body.Bytes()))

	done := make(chan bool, 1)
	go func() {
		done <- s.handleMoveJob(serverConn, r, &dis.RequestHeader{}, "127.0.0.1:1")
	}()

	replyR := dis.NewReader(clientConn)
	code, _, _, _, err := dis.ReadReply(replyR)
	if err != nil {
		return -1
	}
	<-done
	return code
}

func TestMoveJobQueued(t *testing.T) {
	s, jm, qm := newMoveServer(t)
	qA := queue.NewQueue("qa", queue.TypeExecution)
	qB := queue.NewQueue("qb", queue.TypeExecution)
	qm.AddQueue(qA)
	qm.AddQueue(qB)

	j := job.NewJob("1.srv", "qa", "srv")
	j.SetState(job.StateQueued, job.SubstateQueued)
	jm.AddJob(j)
	qA.IncrJobCount(job.StateQueued) // simulate admission count

	if code := runMove(s, "1.srv", "qb"); code != 0 {
		t.Fatalf("queued move reply code = %d, want 0", code)
	}
	if j.Queue != "qb" {
		t.Fatalf("job queue = %q, want qb", j.Queue)
	}
	if j.State != job.StateQueued {
		t.Fatalf("job state changed to %d, want Queued", j.State)
	}
	if qA.TotalJobs != 0 || qA.StateJobs[job.StateQueued] != 0 {
		t.Fatalf("source queue not decremented: %+v", qA)
	}
	if qB.TotalJobs != 1 || qB.StateJobs[job.StateQueued] != 1 {
		t.Fatalf("dest queue not incremented: %+v", qB)
	}
}

func TestMoveJobHeld(t *testing.T) {
	s, jm, qm := newMoveServer(t)
	qA := queue.NewQueue("qa", queue.TypeExecution)
	qB := queue.NewQueue("qb", queue.TypeExecution)
	qm.AddQueue(qA)
	qm.AddQueue(qB)

	j := job.NewJob("2.srv", "qa", "srv")
	j.SetState(job.StateHeld, job.SubstateHeld)
	jm.AddJob(j)
	qA.IncrJobCount(job.StateHeld)

	if code := runMove(s, "2.srv", "qb"); code != 0 {
		t.Fatalf("held move reply code = %d, want 0", code)
	}
	if j.Queue != "qb" || j.State != job.StateHeld {
		t.Fatalf("held job moved incorrectly: queue=%s state=%d", j.Queue, j.State)
	}
	if qA.StateJobs[job.StateHeld] != 0 || qB.StateJobs[job.StateHeld] != 1 {
		t.Fatalf("held counters wrong: qA=%+v qB=%+v", qA, qB)
	}
}

func TestMoveJobRunningRejected(t *testing.T) {
	s, jm, qm := newMoveServer(t)
	qA := queue.NewQueue("qa", queue.TypeExecution)
	qB := queue.NewQueue("qb", queue.TypeExecution)
	qm.AddQueue(qA)
	qm.AddQueue(qB)

	j := job.NewJob("3.srv", "qa", "srv")
	j.SetState(job.StateRunning, job.SubstateRunning)
	jm.AddJob(j)
	qA.IncrJobCount(job.StateRunning)

	if code := runMove(s, "3.srv", "qb"); code == 0 {
		t.Fatalf("running move should be rejected, got success code")
	}
	if j.Queue != "qa" || j.State != job.StateRunning {
		t.Fatalf("running job must remain untouched: queue=%s state=%d", j.Queue, j.State)
	}
	if qA.TotalJobs != 1 || qB.TotalJobs != 0 {
		t.Fatalf("counters must be unchanged for rejected move: qA=%+v qB=%+v", qA, qB)
	}
}

func TestMoveJobUnknownQueue(t *testing.T) {
	s, jm, qm := newMoveServer(t)
	qA := queue.NewQueue("qa", queue.TypeExecution)
	qm.AddQueue(qA)

	j := job.NewJob("4.srv", "qa", "srv")
	j.SetState(job.StateQueued, job.SubstateQueued)
	jm.AddJob(j)

	if code := runMove(s, "4.srv", "nope"); code == 0 {
		t.Fatalf("move to unknown queue should be rejected")
	}
	if j.Queue != "qa" {
		t.Fatalf("job queue changed on rejected move: %q", j.Queue)
	}
}

func TestMoveJobUnknownJob(t *testing.T) {
	s, _, qm := newMoveServer(t)
	qB := queue.NewQueue("qb", queue.TypeExecution)
	qm.AddQueue(qB)
	if code := runMove(s, "999.srv", "qb"); code == 0 {
		t.Fatalf("move of unknown job should be rejected")
	}
}

func TestMoveJobSameQueue(t *testing.T) {
	s, jm, qm := newMoveServer(t)
	qA := queue.NewQueue("qa", queue.TypeExecution)
	qm.AddQueue(qA)

	j := job.NewJob("5.srv", "qa", "srv")
	j.SetState(job.StateQueued, job.SubstateQueued)
	jm.AddJob(j)
	qA.IncrJobCount(job.StateQueued)

	if code := runMove(s, "5.srv", "qa"); code != 0 {
		t.Fatalf("same-queue move reply code = %d, want 0", code)
	}
	if qA.TotalJobs != 1 {
		t.Fatalf("same-queue move must not change counters: %+v", qA)
	}
}
