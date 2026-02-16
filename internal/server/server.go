// Package server implements the main pbs_server daemon logic.
// It coordinates job management, node tracking, scheduling, and client request handling.
package server

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xinlaoda/opentorque/internal/acct"
	"github.com/xinlaoda/opentorque/internal/auth"
	"github.com/xinlaoda/opentorque/internal/config"
	"github.com/xinlaoda/opentorque/internal/dis"
	"github.com/xinlaoda/opentorque/internal/job"
	"github.com/xinlaoda/opentorque/internal/node"
	"github.com/xinlaoda/opentorque/internal/queue"
)

// Config is the public configuration for creating a new server.
type Config struct {
	PBSHome   string
	Port      int
	StartType string
	Debug     bool
}

// Server is the main pbs_server daemon.
type Server struct {
	cfg       *config.Config
	jobMgr    *job.Manager
	queueMgr  *queue.Manager
	nodeMgr   *node.Manager
	listener  net.Listener

	// Server state
	mu         sync.RWMutex
	state      int // SV_STATE_*
	startTime  time.Time

	// Connection authentication map: "ip:port" -> username.
	// Populated by trqauthd's AuthenUser request, consumed by the client
	// connection when it sends Connect.
	authMap sync.Map

	// Shared HMAC key for token-based authentication (cross-platform).
	// Loaded from auth_key file at startup; used by AuthToken handler.
	authKey []byte

	// Accounting logger (TORQUE-compatible records in server_priv/accounting/)
	acctLog *acct.Logger

	// Scheduling
	schedTicker *time.Ticker
	nodeTicker  *time.Ticker

	// Shutdown
	done chan struct{}
}

// Server state constants
const (
	SvStateInit   = 0
	SvStateRun    = 1
	SvStateShutim = 2 // Shutting down immediately
	SvStateDown   = 3
)

// New creates a new Server instance with the given configuration.
func New(cfg *Config) (*Server, error) {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "localhost"
	}
	// Use short hostname (without domain) as server name
	if idx := strings.Index(hostname, "."); idx > 0 {
		hostname = hostname[:idx]
	}

	icfg := config.NewConfig(cfg.PBSHome)
	icfg.Port = cfg.Port
	icfg.StartType = cfg.StartType
	icfg.Debug = cfg.Debug
	icfg.ServerName = hostname

	s := &Server{
		cfg:      icfg,
		jobMgr:   job.NewManager(hostname, 0),
		queueMgr: queue.NewManager(),
		nodeMgr:  node.NewManager(),
		state:    SvStateInit,
		done:     make(chan struct{}),
	}

	// Initialize accounting logger
	al, err := acct.NewLogger(icfg.AcctDir)
	if err != nil {
		log.Printf("[SERVER] Warning: cannot open accounting log: %v", err)
	} else {
		s.acctLog = al
	}

	return s, nil
}

// Start initializes the server: recovers state, begins listening, starts background tasks.
func (s *Server) Start() error {
	s.startTime = time.Now()

	// Ensure required directories exist
	if err := s.ensureDirectories(); err != nil {
		return fmt.Errorf("ensure directories: %w", err)
	}

	// Recover persisted state (jobs, queues, nodes, server config)
	if err := s.recoverState(); err != nil {
		log.Printf("[SERVER] Warning: state recovery failed: %v", err)
	}

	// Load or generate authentication key for token-based auth
	s.loadOrGenerateAuthKey()

	// Start TCP listener on server port
	addr := fmt.Sprintf(":%d", s.cfg.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}
	s.listener = ln
	log.Printf("[SERVER] Listening on %s (batch protocol)", addr)

	s.mu.Lock()
	s.state = SvStateRun
	s.mu.Unlock()

	// Start the connection acceptance goroutine
	go s.acceptLoop()

	// Start background tasks (scheduler, node checks, cleanup)
	s.startBackgroundTasks()

	log.Printf("[SERVER] pbs_server is ready (name=%s, port=%d)", s.cfg.ServerName, s.cfg.Port)
	return nil
}

// Shutdown performs an orderly server shutdown: saves state and closes connections.
func (s *Server) Shutdown() {
	s.mu.Lock()
	s.state = SvStateShutim
	s.mu.Unlock()

	log.Printf("[SERVER] Shutting down...")
	close(s.done)

	if s.schedTicker != nil {
		s.schedTicker.Stop()
	}
	if s.nodeTicker != nil {
		s.nodeTicker.Stop()
	}
	if s.listener != nil {
		s.listener.Close()
	}

	// Save all state to disk before exiting
	s.saveState()
	if s.acctLog != nil {
		s.acctLog.Close()
	}
	log.Printf("[SERVER] Shutdown complete")
}

// buildJobInfo creates an acct.JobInfo from a job (caller must hold at least RLock).
func (s *Server) buildJobInfo(j *job.Job) *acct.JobInfo {
	info := &acct.JobInfo{
		User:         j.Owner,
		Group:        j.EGroup,
		JobName:      j.Name,
		Queue:        j.Queue,
		CreateTime:   j.CreateTime.Unix(),
		QueueTime:    j.QueueTime.Unix(),
		EligTime:     j.QueueTime.Unix(), // eligible time defaults to queue time
		ExitStatus:   j.ExitStatus,
		ExecHost:     j.ExecHost,
		SessionID:    j.SessionID,
		ResourceReq:  copyMap(j.ResourceReq),
		ResourceUsed: copyMap(j.ResourceUsed),
	}
	if !j.StartTime.IsZero() {
		info.StartTime = j.StartTime.Unix()
		info.EligTime = j.StartTime.Unix()
	}
	if !j.CompTime.IsZero() {
		info.EndTime = j.CompTime.Unix()
	}
	if v, ok := j.Attrs["Account_Name"]; ok {
		info.Account = v
	}
	return info
}

// copyMap returns a shallow copy of a string map, or nil if empty.
func copyMap(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// ensureDirectories creates required directories if they don't exist.
func (s *Server) ensureDirectories() error {
	dirs := []string{
		s.cfg.ServerPriv,
		s.cfg.JobsDir,
		s.cfg.QueuesDir,
		s.cfg.LogDir,
		s.cfg.AcctDir,
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0750); err != nil {
			return err
		}
	}
	return nil
}

// acceptLoop accepts incoming TCP connections and dispatches them for processing.
func (s *Server) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.done:
				return // Server shutting down
			default:
				log.Printf("[SERVER] Accept error: %v", err)
				continue
			}
		}
		// Process each connection in a separate goroutine
		go s.handleConnection(conn)
	}
}

// handleConnection processes a single client connection.
// It reads one or more batch requests from the connection and dispatches them.
// A single connection can carry multiple sequential requests (e.g., QueueJob→JobScript→Commit).
func (s *Server) handleConnection(conn net.Conn) {
	defer conn.Close()

	remoteAddr := conn.RemoteAddr().String()
	reader := dis.NewReader(conn)

	// Determine if connection is from a privileged port (< 1024) for authentication
	isPrivileged := false
	if tcpAddr, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
		isPrivileged = tcpAddr.Port < 1024
	}

	log.Printf("[SERVER] Connection from %s (privileged=%v)", remoteAddr, isPrivileged)

	for {
		// Set read deadline for each request
		conn.SetDeadline(time.Now().Add(300 * time.Second))

		// Read ONLY the protocol type first to determine message format.
		// IS protocol (type=4) has a different header than batch protocol (type=2).
		protoType, err := reader.ReadUint()
		if err != nil {
			if err != io.EOF && !strings.Contains(err.Error(), "closed") &&
				!strings.Contains(err.Error(), "reset") &&
				!strings.Contains(err.Error(), "timeout") {
				log.Printf("[SERVER] Read proto error from %s: %v", remoteAddr, err)
			}
			return
		}

		// Handle IS protocol messages from MOMs (protocol type 4)
		if int(protoType) == dis.ISProtocolType {
			s.handleISMessage(conn, reader, remoteAddr)
			return
		}

		// For batch protocol, read remaining header fields: version, reqtype, user
		if int(protoType) != dis.PbsBatchProtType {
			log.Printf("[SERVER] Unknown protocol %d from %s", protoType, remoteAddr)
			return
		}

		ver, err := reader.ReadUint()
		if err != nil {
			return
		}
		reqType, err := reader.ReadUint()
		if err != nil {
			return
		}
		user, err := reader.ReadString()
		if err != nil {
			return
		}
		header := &dis.RequestHeader{
			Protocol: int(protoType),
			Version:  int(ver),
			ReqType:  int(reqType),
			User:     user,
		}

		// Dispatch the request to the appropriate handler
		keepGoing := s.dispatchRequest(conn, reader, header, remoteAddr, isPrivileged)

		// Read request extension (present after every request body).
		// Skip for types that handle extension internally or don't have one.
		if header.ReqType != dis.BatchReqConnect &&
			header.ReqType != dis.BatchReqDisconnect &&
			header.ReqType != dis.BatchReqAuthenUser &&
			header.ReqType != dis.BatchReqAltAuthenUser &&
			header.ReqType != dis.BatchReqAuthToken &&
			header.ReqType != dis.BatchReqRerun {
			if _, err := dis.ReadReqExtend(reader); err != nil {
				if err != io.EOF && !strings.Contains(err.Error(), "EOF") {
					log.Printf("[SERVER] Read extension error: %v", err)
				}
				return
			}
		}

		if !keepGoing {
			return
		}
	}
}

// dispatchRequest routes a batch request to its handler based on request type.
// Returns true if the connection should continue reading more requests.
func (s *Server) dispatchRequest(conn net.Conn, r *dis.Reader, hdr *dis.RequestHeader, remote string, privileged bool) bool {
	switch hdr.ReqType {
	case dis.BatchReqConnect:
		// Connect request: simple handshake, send OK
		return s.handleConnect(conn, r, hdr, remote)

	case dis.BatchReqQueueJob, dis.BatchReqQueueJob2:
		return s.handleQueueJob(conn, r, hdr, remote)

	case dis.BatchReqJobCred:
		return s.handleJobCred(conn, r, hdr, remote)

	case dis.BatchReqJobScript, dis.BatchReqJobScript2:
		return s.handleJobScript(conn, r, hdr, remote, hdr.ReqType == dis.BatchReqJobScript2)

	case dis.BatchReqRdytoCommit:
		return s.handleRdytoCommit(conn, r, hdr, remote)

	case dis.BatchReqCommit, dis.BatchReqCommit2:
		return s.handleCommit(conn, r, hdr, remote)

	case dis.BatchReqDeleteJob:
		return s.handleDeleteJob(conn, r, hdr, remote)

	case dis.BatchReqHoldJob:
		return s.handleHoldJob(conn, r, hdr, remote)

	case dis.BatchReqModifyJob:
		return s.handleModifyJob(conn, r, hdr, remote)

	case dis.BatchReqStatusJob:
		return s.handleStatusJob(conn, r, hdr, remote)

	case dis.BatchReqStatusQueue:
		return s.handleStatusQueue(conn, r, hdr, remote)

	case dis.BatchReqStatusServer:
		return s.handleStatusServer(conn, r, hdr, remote)

	case dis.BatchReqStatusNode:
		return s.handleStatusNode(conn, r, hdr, remote)

	case dis.BatchReqManager:
		return s.handleManager(conn, r, hdr, remote)

	case dis.BatchReqShutdown:
		return s.handleShutdown(conn, r, hdr, remote)

	case dis.BatchReqSignalJob:
		return s.handleSignalJob(conn, r, hdr, remote)

	case dis.BatchReqJobObit:
		return s.handleJobObit(conn, r, hdr, remote, privileged)

	case dis.BatchReqRunJob:
		return s.handleRunJob(conn, r, hdr, remote)

	case dis.BatchReqDisconnect:
		// Disconnect: clean close
		return false

	case dis.BatchReqAuthenUser:
		// AuthenUser from trqauthd: body is uint(client_port), then extension.
		// trqauthd connects from a privileged port to tell the server which
		// client connection (identified by source port) belongs to which user.
		return s.handleAuthenUser(conn, r, hdr, remote)

	case dis.BatchReqAltAuthenUser:
		// AltAuthenUser (munge): body is uint(port) + string(credential), then extension.
		clientPort, _ := r.ReadUint()
		cred, _ := r.ReadString()
		dis.ReadReqExtend(r)
		log.Printf("[SERVER] AltAuthenUser from %s, port=%d, cred_len=%d", remote, clientPort, len(cred))
		dis.SendOkReply(conn)
		return true

	case dis.BatchReqAuthToken:
		// Token-based authentication: cross-platform replacement for trqauthd.
		// Body: uint(timestamp) string(hmac_signature), then extension.
		return s.handleAuthToken(conn, r, hdr, remote)

	case dis.BatchReqLocateJob:
		return s.handleLocateJob(conn, r, hdr, remote)

	case dis.BatchReqSelectJobs:
		return s.handleSelectJobs(conn, r, hdr, remote)

	case dis.BatchReqReleaseJob:
		return s.handleReleaseJob(conn, r, hdr, remote)

	case dis.BatchReqMoveJob:
		return s.handleMoveJob(conn, r, hdr, remote)

	case dis.BatchReqRerun:
		return s.handleRerunJob(conn, r, hdr, remote)

	case dis.BatchReqOrderJob:
		return s.handleOrderJob(conn, r, hdr, remote)

	case dis.BatchReqMessJob:
		return s.handleMessJob(conn, r, hdr, remote)

	case dis.BatchReqCheckpointJob:
		return s.handleCheckpointJob(conn, r, hdr, remote)

	default:
		log.Printf("[SERVER] Unhandled request type %d from %s", hdr.ReqType, remote)
		dis.SendErrorReply(conn, dis.PbseUnkReq, 0)
		return true
	}
}

// --- Request Handlers ---

// handleConnect processes a Connect request (initial handshake).
func (s *Server) handleConnect(conn net.Conn, r *dis.Reader, hdr *dis.RequestHeader, remote string) bool {
	dis.SendOkReply(conn)
	return true
}

// handleAuthenUser processes an AuthenUser request from trqauthd.
// trqauthd connects from a privileged port and identifies which client
// connection (by source port) should be authenticated as which user.
// Body: uint(client_port), then extension string.
func (s *Server) handleAuthenUser(conn net.Conn, r *dis.Reader, hdr *dis.RequestHeader, remote string) bool {
	// Read body: the client's source port
	clientPort, err := r.ReadUint()
	if err != nil {
		log.Printf("[SERVER] AuthenUser: error reading client port: %v", err)
		dis.SendErrorReply(conn, dis.PbseBadReq, 0)
		return false
	}

	// Read extension
	dis.ReadReqExtend(r)

	// Extract trqauthd's IP to construct the client connection key.
	// The client is on the same host as trqauthd.
	trqHost, _, _ := net.SplitHostPort(remote)

	// Store authentication: the client at trqHost:clientPort is user hdr.User
	authKey := fmt.Sprintf("%s:%d", trqHost, clientPort)
	s.authMap.Store(authKey, hdr.User)
	log.Printf("[SERVER] AuthenUser: authenticated %s for connection %s", hdr.User, authKey)

	dis.SendOkReply(conn)
	return true
}

// handleAuthToken processes a token-based authentication request.
// This is the cross-platform replacement for trqauthd. The client sends
// an HMAC-SHA256 signature of "user|timestamp" using a shared secret key.
// Body: uint(timestamp) string(hmac_hex), then extension.
func (s *Server) handleAuthToken(conn net.Conn, r *dis.Reader, hdr *dis.RequestHeader, remote string) bool {
	timestamp, err := r.ReadUint()
	if err != nil {
		log.Printf("[SERVER] AuthToken: error reading timestamp: %v", err)
		dis.SendErrorReply(conn, dis.PbseBadCred, 0)
		return false
	}

	token, err := r.ReadString()
	if err != nil {
		log.Printf("[SERVER] AuthToken: error reading token: %v", err)
		dis.SendErrorReply(conn, dis.PbseBadCred, 0)
		return false
	}

	dis.ReadReqExtend(r)

	// Reject if no auth key is loaded
	if s.authKey == nil {
		log.Printf("[SERVER] AuthToken: no auth key configured, rejecting %s", remote)
		dis.SendErrorReply(conn, dis.PbseBadCred, 0)
		return false
	}

	// Verify the HMAC token against the shared key
	if err := auth.VerifyToken(hdr.User, int64(timestamp), token, s.authKey); err != nil {
		log.Printf("[SERVER] AuthToken: rejected %s from %s: %v", hdr.User, remote, err)
		dis.SendErrorReply(conn, dis.PbseBadCred, 0)
		return false
	}

	// Authentication successful — mark this connection as authenticated
	s.authMap.Store(remote, hdr.User)
	log.Printf("[SERVER] AuthToken: authenticated %s from %s", hdr.User, remote)

	dis.SendOkReply(conn)
	return true
}

// loadOrGenerateAuthKey loads the shared auth key file, creating one if absent.
func (s *Server) loadOrGenerateAuthKey() {
	key, err := auth.LoadKey(s.cfg.PBSHome)
	if err != nil {
		// Key file doesn't exist, generate a new one
		log.Printf("[SERVER] Auth key not found, generating new key")
		key, err = auth.GenerateKeyFile(s.cfg.PBSHome)
		if err != nil {
			log.Printf("[SERVER] Warning: could not generate auth key: %v", err)
			log.Printf("[SERVER] Token-based auth disabled; trqauthd/privileged-port auth still works")
			return
		}
	}
	s.authKey = key
	log.Printf("[SERVER] Token-based authentication enabled (key loaded)")
}

// handleQueueJob processes a QueueJob request from a client (e.g., qsub).
// This is the first step in job submission. The server creates a job object,
// assigns it an ID, and returns the job ID to the client.
func (s *Server) handleQueueJob(conn net.Conn, r *dis.Reader, hdr *dis.RequestHeader, remote string) bool {
	_, dest, attrs, err := dis.ReadQueueJobBody(r)
	if err != nil {
		log.Printf("[SERVER] Error reading QueueJob body: %v", err)
		dis.SendErrorReply(conn, dis.PbseBadReq, 0)
		return false
	}

	// Determine target queue (use default if not specified)
	queueName := dest
	if queueName == "" {
		if dq := s.queueMgr.DefaultQueue(); dq != nil {
			queueName = dq.Name
		} else {
			log.Printf("[SERVER] No default queue available")
			dis.SendErrorReply(conn, dis.PbseQueNoDeflt, 0)
			return false
		}
	}

	// Verify queue exists and is enabled
	q := s.queueMgr.GetQueue(queueName)
	if q == nil {
		log.Printf("[SERVER] Queue %s not found", queueName)
		dis.SendErrorReply(conn, dis.PbseUnkQue, 0)
		return false
	}

	// Assign a new job ID
	jobID := s.jobMgr.NextJobID()

	// Create the job object and populate attributes from the request
	j := job.NewJob(jobID, queueName, s.cfg.ServerName)
	j.Owner = hdr.User
	s.applyJobAttrs(j, attrs)

	// If owner not set from attrs, derive from request user
	if j.Owner == "" {
		j.Owner = hdr.User
	}

	// Enforce ACL and resource limits before accepting the job
	if err := s.enforceSubmitLimits(j, hdr.User); err != nil {
		log.Printf("[SERVER] Job rejected: %v", err)
		dis.SendErrorReply(conn, dis.PbsePerm, 0)
		return false
	}

	// Store the job (transit state) pending further steps
	s.jobMgr.AddJob(j)

	log.Printf("[SERVER] QueueJob: id=%s queue=%s owner=%s from %s", jobID, queueName, j.Owner, remote)

	// Reply with the assigned job ID (Queue choice)
	dis.SendJobIDReply(conn, dis.ReplyChoiceQueue, jobID)
	return true
}

// handleJobCred processes a JobCred request (credential transfer for job).
// We accept and acknowledge; actual credential validation is minimal in this implementation.
func (s *Server) handleJobCred(conn net.Conn, r *dis.Reader, hdr *dis.RequestHeader, remote string) bool {
	_, _, err := dis.ReadJobCredBody(r)
	if err != nil {
		log.Printf("[SERVER] Error reading JobCred: %v", err)
		dis.SendErrorReply(conn, dis.PbseBadReq, 0)
		return false
	}
	dis.SendOkReply(conn)
	return true
}

// handleJobScript processes a JobScript request (receives the job script content).
// When autoCommit is true (JobScript2 variant), the job is automatically committed
// to Queued state after receiving the script, skipping RdytoCommit and Commit steps.
func (s *Server) handleJobScript(conn net.Conn, r *dis.Reader, hdr *dis.RequestHeader, remote string, autoCommit bool) bool {
	jobID, data, err := dis.ReadJobScriptBody(r)
	if err != nil {
		log.Printf("[SERVER] Error reading JobScript: %v", err)
		dis.SendErrorReply(conn, dis.PbseBadReq, 0)
		return false
	}

	j := s.jobMgr.GetJob(jobID)
	if j == nil {
		log.Printf("[SERVER] JobScript for unknown job %s", jobID)
		dis.SendErrorReply(conn, dis.PbseUnkjobid, 0)
		return false
	}

	j.Mu.Lock()
	j.Script = string(data)
	j.Mu.Unlock()

	// Save script to disk for persistence
	scriptPath := filepath.Join(s.cfg.JobsDir, jobID+".SC")
	os.WriteFile(scriptPath, data, 0700)

	log.Printf("[SERVER] JobScript for %s (%d bytes)", jobID, len(data))

	// Auto-commit for "2" protocol variant (QueueJob2/JobScript2/Commit2)
	if autoCommit {
		s.setDefaultOutputPaths(j)

		// Transition via manager to update state counters
		s.jobMgr.UpdateJobState(jobID, job.StateQueued, job.SubstateQueued)

		// Apply hold or deferred execution after initial state transition
		j.Mu.RLock()
		holdTypes := j.HoldTypes
		execTime := j.ExecutionTime
		queueName := j.Queue
		j.Mu.RUnlock()

		if holdTypes != "" && holdTypes != "n" {
			s.jobMgr.UpdateJobState(jobID, job.StateHeld, job.SubstateHeld)
		} else if !execTime.IsZero() && execTime.After(time.Now()) {
			s.jobMgr.UpdateJobState(jobID, job.StateWaiting, job.SubstateWaiting)
		}

		if q := s.queueMgr.GetQueue(queueName); q != nil {
			q.IncrJobCount(job.StateQueued)
		}
		s.saveJob(j)
		log.Printf("[SERVER] Auto-committed job %s to queue %s", jobID, j.Queue)

		// Write Q (queued) accounting record
		if s.acctLog != nil {
			j.Mu.RLock()
			info := s.buildJobInfo(j)
			j.Mu.RUnlock()
			s.acctLog.RecordQueued(jobID, info)
		}
	}

	dis.SendOkReply(conn)
	return true
}

// handleRdytoCommit processes a RdytoCommit request.
// Some older clients send this before Commit; we just acknowledge with the job ID.
func (s *Server) handleRdytoCommit(conn net.Conn, r *dis.Reader, hdr *dis.RequestHeader, remote string) bool {
	jobID, err := dis.ReadCommitBody(r)
	if err != nil {
		dis.SendErrorReply(conn, dis.PbseBadReq, 0)
		return false
	}
	dis.SendJobIDReply(conn, dis.ReplyChoiceRdytoCom, jobID)
	return true
}

// handleCommit finalizes a job submission: transitions job to Queued state and persists.
// After this, the job is eligible for scheduling and execution.
func (s *Server) handleCommit(conn net.Conn, r *dis.Reader, hdr *dis.RequestHeader, remote string) bool {
	jobID, err := dis.ReadCommitBody(r)
	if err != nil {
		dis.SendErrorReply(conn, dis.PbseBadReq, 0)
		return false
	}

	j := s.jobMgr.GetJob(jobID)
	if j == nil {
		dis.SendErrorReply(conn, dis.PbseUnkjobid, 0)
		return false
	}

	// Set default output paths if not specified by user
	s.setDefaultOutputPaths(j)

	// Transition job to Queued state via manager (updates state counters)
	s.jobMgr.UpdateJobState(jobID, job.StateQueued, job.SubstateQueued)

	// Apply hold or deferred execution after initial state transition
	j.Mu.RLock()
	holdTypes := j.HoldTypes
	execTime := j.ExecutionTime
	queueName := j.Queue
	j.Mu.RUnlock()

	if holdTypes != "" && holdTypes != "n" {
		s.jobMgr.UpdateJobState(jobID, job.StateHeld, job.SubstateHeld)
	} else if !execTime.IsZero() && execTime.After(time.Now()) {
		s.jobMgr.UpdateJobState(jobID, job.StateWaiting, job.SubstateWaiting)
	}

	// Update queue counters
	if q := s.queueMgr.GetQueue(queueName); q != nil {
		q.IncrJobCount(job.StateQueued)
	}

	// Persist the job
	s.saveJob(j)

	log.Printf("[SERVER] Commit job %s to queue %s", jobID, queueName)

	// Write Q (queued) accounting record
	if s.acctLog != nil {
		j.Mu.RLock()
		info := s.buildJobInfo(j)
		j.Mu.RUnlock()
		s.acctLog.RecordQueued(jobID, info)
	}

	dis.SendJobIDReply(conn, dis.ReplyChoiceCommit, jobID)
	return true
}

// setDefaultOutputPaths assigns default stdout/stderr paths using TORQUE convention:
// <hostname>:<home_or_workdir>/<jobname>.o<seqnum> / .e<seqnum>
func (s *Server) setDefaultOutputPaths(j *job.Job) {
	j.Mu.Lock()
	defer j.Mu.Unlock()

	if j.StdoutPath != "" && j.StderrPath != "" {
		return
	}

	// Extract sequence number from job ID (e.g., "5.DevBox" -> "5")
	seqStr := j.ID
	if idx := strings.Index(j.ID, "."); idx > 0 {
		seqStr = j.ID[:idx]
	}

	// Determine output directory: PBS_O_HOME or PBS_O_WORKDIR
	dir := j.VariableList["PBS_O_HOME"]
	if dir == "" {
		dir = j.VariableList["PBS_O_WORKDIR"]
	}
	if dir == "" {
		dir = "/tmp"
	}

	hostname, _ := os.Hostname()
	if j.StdoutPath == "" {
		j.StdoutPath = hostname + ":" + filepath.Join(dir, j.Name+".o"+seqStr)
	}
	if j.StderrPath == "" {
		j.StderrPath = hostname + ":" + filepath.Join(dir, j.Name+".e"+seqStr)
	}
}

// handleDeleteJob processes a qdel request to cancel/delete a job.
func (s *Server) handleDeleteJob(conn net.Conn, r *dis.Reader, hdr *dis.RequestHeader, remote string) bool {
	jobID, err := dis.ReadDeleteJobBody(r)
	if err != nil {
		dis.SendErrorReply(conn, dis.PbseBadReq, 0)
		return false
	}

	j := s.jobMgr.GetJob(jobID)
	if j == nil {
		dis.SendErrorReply(conn, dis.PbseUnkjobid, 0)
		return true
	}

	j.Mu.Lock()
	state := j.State
	queueName := j.Queue
	j.Mu.Unlock()

	// Write D (deleted) accounting record before removing the job
	if s.acctLog != nil {
		j.Mu.RLock()
		info := s.buildJobInfo(j)
		j.Mu.RUnlock()
		s.acctLog.RecordDeleted(jobID, info)
	}

	// If running, we would need to signal the MOM to kill it
	if state == job.StateRunning {
		// Send kill signal to MOM (simplified: just mark as exiting)
		log.Printf("[SERVER] Delete running job %s (will send kill to MOM)", jobID)
		s.sendDeleteToMOM(j)
	}

	// Remove from queue counts
	if q := s.queueMgr.GetQueue(queueName); q != nil {
		q.DecrJobCount(state)
	}

	// Mark complete and remove
	s.jobMgr.UpdateJobState(jobID, job.StateComplete, job.SubstateComplete)
	s.removeJobFiles(jobID)
	s.jobMgr.RemoveJob(jobID)

	log.Printf("[SERVER] Deleted job %s (was state=%d)", jobID, state)
	dis.SendOkReply(conn)
	return true
}

// handleHoldJob processes a qhold request.
func (s *Server) handleHoldJob(conn net.Conn, r *dis.Reader, hdr *dis.RequestHeader, remote string) bool {
	jobID, _, err := dis.ReadHoldJobBody(r)
	if err != nil {
		dis.SendErrorReply(conn, dis.PbseBadReq, 0)
		return false
	}

	j := s.jobMgr.GetJob(jobID)
	if j == nil {
		dis.SendErrorReply(conn, dis.PbseUnkjobid, 0)
		return true
	}

	j.Mu.Lock()
	oldState := j.State
	if oldState == job.StateQueued || oldState == job.StateWaiting {
		j.SetState(job.StateHeld, job.SubstateHeld)
	}
	queueName := j.Queue
	j.Mu.Unlock()

	if oldState == job.StateQueued {
		if q := s.queueMgr.GetQueue(queueName); q != nil {
			q.TransferJobState(job.StateQueued, job.StateHeld)
		}
	}

	log.Printf("[SERVER] Hold job %s", jobID)
	dis.SendOkReply(conn)
	return true
}

// handleReleaseJob processes a qrls request.
func (s *Server) handleReleaseJob(conn net.Conn, r *dis.Reader, hdr *dis.RequestHeader, remote string) bool {
	jobID, _, err := dis.ReadHoldJobBody(r) // Same format as HoldJob
	if err != nil {
		dis.SendErrorReply(conn, dis.PbseBadReq, 0)
		return false
	}

	j := s.jobMgr.GetJob(jobID)
	if j == nil {
		dis.SendErrorReply(conn, dis.PbseUnkjobid, 0)
		return true
	}

	j.Mu.Lock()
	j.HoldTypes = "" // Clear hold types on release
	if j.State == job.StateHeld {
		j.SetState(job.StateQueued, job.SubstateQueued)
		if q := s.queueMgr.GetQueue(j.Queue); q != nil {
			q.TransferJobState(job.StateHeld, job.StateQueued)
		}
	}
	j.Mu.Unlock()

	log.Printf("[SERVER] Release job %s", jobID)
	dis.SendOkReply(conn)
	return true
}

// handleModifyJob processes a qalter request to modify job attributes.
func (s *Server) handleModifyJob(conn net.Conn, r *dis.Reader, hdr *dis.RequestHeader, remote string) bool {
	jobID, attrs, err := dis.ReadModifyJobBody(r)
	if err != nil {
		dis.SendErrorReply(conn, dis.PbseBadReq, 0)
		return false
	}

	j := s.jobMgr.GetJob(jobID)
	if j == nil {
		dis.SendErrorReply(conn, dis.PbseUnkjobid, 0)
		return true
	}

	j.Mu.Lock()
	s.applyJobAttrs(j, attrs)
	j.Modified = true
	j.Mu.Unlock()

	s.saveJob(j)

	log.Printf("[SERVER] Modified job %s (%d attrs)", jobID, len(attrs))
	dis.SendOkReply(conn)
	return true
}

// handleSignalJob processes a qsig request to send a signal to a running job.
func (s *Server) handleSignalJob(conn net.Conn, r *dis.Reader, hdr *dis.RequestHeader, remote string) bool {
	jobID, sig, err := dis.ReadSignalJobBody(r)
	if err != nil {
		dis.SendErrorReply(conn, dis.PbseBadReq, 0)
		return false
	}

	j := s.jobMgr.GetJob(jobID)
	if j == nil {
		dis.SendErrorReply(conn, dis.PbseUnkjobid, 0)
		return true
	}

	log.Printf("[SERVER] Signal job %s with %s", jobID, sig)
	// In a full implementation, relay signal to MOM
	dis.SendOkReply(conn)
	return true
}

// handleLocateJob returns the location (server) of a job.
func (s *Server) handleLocateJob(conn net.Conn, r *dis.Reader, hdr *dis.RequestHeader, remote string) bool {
	jobID, err := dis.ReadLocateJobBody(r)
	if err != nil {
		dis.SendErrorReply(conn, dis.PbseBadReq, 0)
		return false
	}

	j := s.jobMgr.GetJob(jobID)
	if j == nil {
		dis.SendErrorReply(conn, dis.PbseUnkjobid, 0)
		return true
	}

	dis.SendTextReply(conn, s.cfg.ServerName)
	return true
}

// handleSelectJobs returns a list of job IDs matching selection criteria.
func (s *Server) handleSelectJobs(conn net.Conn, r *dis.Reader, hdr *dis.RequestHeader, remote string) bool {
	_, _, err := dis.ReadStatusBody(r)
	if err != nil {
		dis.SendErrorReply(conn, dis.PbseBadReq, 0)
		return false
	}

	// Return all job IDs as text
	var ids []string
	for _, j := range s.jobMgr.AllJobs() {
		j.Mu.RLock()
		ids = append(ids, j.ID)
		j.Mu.RUnlock()
	}
	dis.SendTextReply(conn, strings.Join(ids, "\n"))
	return true
}

// handleRunJob processes a request to run a job on a specific node.
// When called by an external scheduler, the destination specifies the target node.
// Format: string(jobid) string(destination) [uint(resv_port)]
func (s *Server) handleRunJob(conn net.Conn, r *dis.Reader, hdr *dis.RequestHeader, remote string) bool {
	jobID, dest, err := dis.ReadRunJobBody(r)
	if err != nil {
		dis.SendErrorReply(conn, dis.PbseBadReq, 0)
		return false
	}

	// Extension is read by the main connection loop (handleConnection)

	j := s.jobMgr.GetJob(jobID)
	if j == nil {
		dis.SendErrorReply(conn, dis.PbseUnkjobid, 0)
		return true
	}

	j.Mu.RLock()
	if j.State != job.StateQueued {
		j.Mu.RUnlock()
		dis.SendErrorReply(conn, dis.PbseBadReq, 0)
		return true
	}
	j.Mu.RUnlock()

	// Enforce server-wide and per-user/group run limits
	if !s.enforceRunLimits(j) {
		log.Printf("[SERVER] RunJob %s rejected: run limits exceeded", jobID)
		dis.SendErrorReply(conn, dis.PbsePerm, 0)
		return true
	}

	// Check job dependencies before allowing run
	if !s.checkDependencies(j) {
		log.Printf("[SERVER] RunJob %s rejected: dependencies not satisfied", jobID)
		dis.SendErrorReply(conn, dis.PbseBadReq, 0)
		return true
	}

	// If a destination node is specified, dispatch to that node directly
	if dest != "" {
		nodeName := strings.Split(dest, "/")[0] // strip "/0" suffix if present
		n := s.nodeMgr.GetNode(nodeName)
		if n == nil {
			log.Printf("[SERVER] RunJob %s: unknown node %s", jobID, dest)
			dis.SendErrorReply(conn, dis.PbseUnkNode, 0)
			return true
		}

		// Reserve slot on specified node and dispatch
		n.Mu.Lock()
		n.AssignJob(j.ID, 1)
		execHost := fmt.Sprintf("%s/0", n.Name)
		momPort := n.MomPort
		n.Mu.Unlock()

		j.Mu.Lock()
		j.ExecHost = execHost
		j.ExecPort = momPort
		oldState := j.State
		j.SetState(job.StateRunning, job.SubstateRunning)
		queueName := j.Queue
		j.Mu.Unlock()

		if q := s.queueMgr.GetQueue(queueName); q != nil {
			q.TransferJobState(oldState, job.StateRunning)
		}
		s.saveJob(j)
		log.Printf("[SERVER] RunJob %s dispatched to %s by external scheduler", jobID, dest)

		// Write S (started) accounting record
		if s.acctLog != nil {
			j.Mu.RLock()
			info := s.buildJobInfo(j)
			j.Mu.RUnlock()
			s.acctLog.RecordStarted(jobID, info)
		}

		go s.dispatchJobToMOM(j, n)
	} else {
		// No destination — use built-in placement
		go s.scheduleJob(j)
	}

	dis.SendOkReply(conn)
	return true
}

// handleStatusJob returns status information for one or all jobs (qstat).
func (s *Server) handleStatusJob(conn net.Conn, r *dis.Reader, hdr *dis.RequestHeader, remote string) bool {
	id, _, err := dis.ReadStatusBody(r)
	if err != nil {
		dis.SendErrorReply(conn, dis.PbseBadReq, 0)
		return false
	}

	var objects []dis.StatusObject
	if id != "" {
		// Status for a specific job
		j := s.jobMgr.GetJob(id)
		if j == nil {
			dis.SendErrorReply(conn, dis.PbseUnkjobid, 0)
			return true
		}
		objects = append(objects, s.formatJobStatus(j))
	} else {
		// Status for all jobs, sorted by ID
		jobs := s.jobMgr.AllJobs()
		sort.Slice(jobs, func(i, k int) bool { return jobs[i].ID < jobs[k].ID })
		for _, j := range jobs {
			objects = append(objects, s.formatJobStatus(j))
		}
	}

	dis.WriteStatusReply(conn, objects)
	return true
}

// handleStatusQueue returns status for one or all queues.
func (s *Server) handleStatusQueue(conn net.Conn, r *dis.Reader, hdr *dis.RequestHeader, remote string) bool {
	id, _, err := dis.ReadStatusBody(r)
	if err != nil {
		dis.SendErrorReply(conn, dis.PbseBadReq, 0)
		return false
	}

	var objects []dis.StatusObject
	if id != "" {
		q := s.queueMgr.GetQueue(id)
		if q == nil {
			dis.SendErrorReply(conn, dis.PbseUnkQue, 0)
			return true
		}
		objects = append(objects, s.formatQueueStatus(q))
	} else {
		for _, q := range s.queueMgr.AllQueues() {
			objects = append(objects, s.formatQueueStatus(q))
		}
	}

	dis.WriteStatusReply(conn, objects)
	return true
}

// handleStatusServer returns server-level status information.
func (s *Server) handleStatusServer(conn net.Conn, r *dis.Reader, hdr *dis.RequestHeader, remote string) bool {
	_, _, err := dis.ReadStatusBody(r)
	if err != nil {
		dis.SendErrorReply(conn, dis.PbseBadReq, 0)
		return false
	}

	obj := s.formatServerStatus()
	dis.WriteStatusReply(conn, []dis.StatusObject{obj})
	return true
}

// handleStatusNode returns status for one or all compute nodes (pbsnodes).
func (s *Server) handleStatusNode(conn net.Conn, r *dis.Reader, hdr *dis.RequestHeader, remote string) bool {
	id, _, err := dis.ReadStatusBody(r)
	if err != nil {
		dis.SendErrorReply(conn, dis.PbseBadReq, 0)
		return false
	}

	var objects []dis.StatusObject
	if id != "" {
		n := s.nodeMgr.GetNode(id)
		if n == nil {
			dis.SendErrorReply(conn, dis.PbseUnkNode, 0)
			return true
		}
		objects = append(objects, s.formatNodeStatus(n))
	} else {
		for _, n := range s.nodeMgr.AllNodes() {
			objects = append(objects, s.formatNodeStatus(n))
		}
	}

	dis.WriteStatusReply(conn, objects)
	return true
}

// handleManager processes qmgr administrative commands.
// Supports creating/deleting queues and nodes, and setting server/queue/node attributes.
func (s *Server) handleManager(conn net.Conn, r *dis.Reader, hdr *dis.RequestHeader, remote string) bool {
	command, objType, objName, attrs, err := dis.ReadManagerBody(r)
	if err != nil {
		dis.SendErrorReply(conn, dis.PbseBadReq, 0)
		return false
	}

	log.Printf("[SERVER] Manager: cmd=%d objType=%d name=%s attrs=%d from %s",
		command, objType, objName, len(attrs), remote)

	var result error
	switch {
	case objType == dis.MgrObjQueue && command == dis.MgrCmdCreate:
		result = s.mgrCreateQueue(objName, attrs)
	case objType == dis.MgrObjQueue && command == dis.MgrCmdDelete:
		result = s.mgrDeleteQueue(objName)
	case objType == dis.MgrObjQueue && command == dis.MgrCmdSet:
		result = s.mgrSetQueue(objName, attrs)
	case objType == dis.MgrObjQueue && command == dis.MgrCmdUnset:
		result = s.mgrUnsetQueue(objName, attrs)
	case objType == dis.MgrObjNode && command == dis.MgrCmdCreate:
		result = s.mgrCreateNode(objName, attrs)
	case objType == dis.MgrObjNode && command == dis.MgrCmdDelete:
		result = s.mgrDeleteNode(objName)
	case objType == dis.MgrObjNode && command == dis.MgrCmdSet:
		result = s.mgrSetNode(objName, attrs)
	case objType == dis.MgrObjServer && command == dis.MgrCmdSet:
		result = s.mgrSetServer(attrs)
	case objType == dis.MgrObjServer && command == dis.MgrCmdUnset:
		result = s.mgrUnsetServer(attrs)
	default:
		log.Printf("[SERVER] Unsupported manager cmd=%d objType=%d", command, objType)
		result = fmt.Errorf("unsupported manager operation")
	}

	if result != nil {
		log.Printf("[SERVER] Manager error: %v", result)
		dis.SendErrorReply(conn, dis.PbseBadReq, 0)
	} else {
		dis.SendOkReply(conn)
		// Persist server config changes immediately
		if objType == dis.MgrObjServer {
			s.saveServerDB()
		}
	}
	return true
}

// handleShutdown processes a server shutdown request.
func (s *Server) handleShutdown(conn net.Conn, r *dis.Reader, hdr *dis.RequestHeader, remote string) bool {
	log.Printf("[SERVER] Shutdown requested from %s", remote)
	dis.SendOkReply(conn)
	go func() {
		time.Sleep(500 * time.Millisecond)
		s.Shutdown()
		os.Exit(0)
	}()
	return false
}

// handleMoveJob processes a request to move a job to a different queue (qmove).
// Body: string(jobid) string(destination)
func (s *Server) handleMoveJob(conn net.Conn, r *dis.Reader, hdr *dis.RequestHeader, remote string) bool {
	jobID, dest, err := dis.ReadMoveJobBody(r)
	if err != nil {
		dis.SendErrorReply(conn, dis.PbseBadReq, 0)
		return false
	}

	j := s.jobMgr.GetJob(jobID)
	if j == nil {
		dis.SendErrorReply(conn, dis.PbseUnkjobid, 0)
		return true
	}

	// Destination is a queue name (optionally with @server)
	queueName := strings.Split(dest, "@")[0]
	newQ := s.queueMgr.GetQueue(queueName)
	if newQ == nil {
		dis.SendErrorReply(conn, dis.PbseUnkQue, 0)
		return true
	}

	j.Mu.Lock()
	oldQueue := j.Queue
	oldState := j.State
	if oldState != job.StateQueued {
		j.Mu.Unlock()
		dis.SendErrorReply(conn, dis.PbseBadReq, 0)
		return true
	}
	j.Queue = queueName
	j.Mu.Unlock()

	// Update queue counters
	if oq := s.queueMgr.GetQueue(oldQueue); oq != nil {
		oq.TransferJobState(oldState, -1) // remove from old queue count
	}
	newQ.TransferJobState(-1, oldState) // add to new queue count

	s.saveJob(j)
	log.Printf("[SERVER] MoveJob %s from %s to %s", jobID, oldQueue, queueName)
	dis.SendOkReply(conn)
	return true
}

// handleRerunJob processes a request to requeue a running job (qrerun).
// Body: string(jobid) string(extension)
// Extension is read here (excluded from main loop extension read).
func (s *Server) handleRerunJob(conn net.Conn, r *dis.Reader, hdr *dis.RequestHeader, remote string) bool {
	jobID, err := r.ReadString()
	if err != nil {
		dis.SendErrorReply(conn, dis.PbseBadReq, 0)
		return false
	}
	// Read extension (force flag)
	ext, _ := r.ReadString()
	_ = ext // "RERUNFORCE" or ""

	j := s.jobMgr.GetJob(jobID)
	if j == nil {
		dis.SendErrorReply(conn, dis.PbseUnkjobid, 0)
		return true
	}

	j.Mu.Lock()
	oldState := j.State
	if oldState != job.StateRunning && oldState != job.StateQueued {
		j.Mu.Unlock()
		dis.SendErrorReply(conn, dis.PbseBadReq, 0)
		return true
	}

	// Release node resources if job was running
	if oldState == job.StateRunning && j.ExecHost != "" {
		nodeName := strings.Split(j.ExecHost, "/")[0]
		if n := s.nodeMgr.GetNode(nodeName); n != nil {
			n.Mu.Lock()
			n.ReleaseJob(j.ID, 1)
			n.Mu.Unlock()
		}
	}

	j.ExecHost = ""
	j.ExecPort = 0
	queueName := j.Queue
	j.SetState(job.StateQueued, job.SubstateQueued)
	j.Mu.Unlock()

	if q := s.queueMgr.GetQueue(queueName); q != nil {
		q.TransferJobState(oldState, job.StateQueued)
	}

	s.saveJob(j)
	log.Printf("[SERVER] RerunJob %s requeued", jobID)
	dis.SendOkReply(conn)
	return true
}

// handleOrderJob processes a request to swap the order of two jobs (qorder).
// Body: string(jobid1) string(jobid2)
func (s *Server) handleOrderJob(conn net.Conn, r *dis.Reader, hdr *dis.RequestHeader, remote string) bool {
	jobID1, err := r.ReadString()
	if err != nil {
		dis.SendErrorReply(conn, dis.PbseBadReq, 0)
		return false
	}
	jobID2, err := r.ReadString()
	if err != nil {
		dis.SendErrorReply(conn, dis.PbseBadReq, 0)
		return false
	}

	j1 := s.jobMgr.GetJob(jobID1)
	j2 := s.jobMgr.GetJob(jobID2)
	if j1 == nil || j2 == nil {
		dis.SendErrorReply(conn, dis.PbseUnkjobid, 0)
		return true
	}

	// Swap queue positions by swapping creation timestamps
	j1.Mu.Lock()
	j2.Mu.Lock()
	j1.CreateTime, j2.CreateTime = j2.CreateTime, j1.CreateTime
	j2.Mu.Unlock()
	j1.Mu.Unlock()

	s.saveJob(j1)
	s.saveJob(j2)
	log.Printf("[SERVER] OrderJob swapped %s and %s", jobID1, jobID2)
	dis.SendOkReply(conn)
	return true
}

// handleMessJob processes a request to send a message to a job's output (qmsg).
// Body: string(jobid) uint(file_option) string(message)
func (s *Server) handleMessJob(conn net.Conn, r *dis.Reader, hdr *dis.RequestHeader, remote string) bool {
	jobID, err := r.ReadString()
	if err != nil {
		dis.SendErrorReply(conn, dis.PbseBadReq, 0)
		return false
	}
	_, err = r.ReadUint() // file_option (MSG_ERR=1, MSG_OUT=2)
	if err != nil {
		dis.SendErrorReply(conn, dis.PbseBadReq, 0)
		return false
	}
	message, err := r.ReadString()
	if err != nil {
		dis.SendErrorReply(conn, dis.PbseBadReq, 0)
		return false
	}

	j := s.jobMgr.GetJob(jobID)
	if j == nil {
		dis.SendErrorReply(conn, dis.PbseUnkjobid, 0)
		return true
	}

	log.Printf("[SERVER] MessJob %s: %s", jobID, message)
	dis.SendOkReply(conn)
	return true
}

// handleCheckpointJob processes a checkpoint request for a running job (qchkpt).
// Body: string(jobid)
func (s *Server) handleCheckpointJob(conn net.Conn, r *dis.Reader, hdr *dis.RequestHeader, remote string) bool {
	jobID, err := r.ReadString()
	if err != nil {
		dis.SendErrorReply(conn, dis.PbseBadReq, 0)
		return false
	}

	j := s.jobMgr.GetJob(jobID)
	if j == nil {
		dis.SendErrorReply(conn, dis.PbseUnkjobid, 0)
		return true
	}

	j.Mu.RLock()
	state := j.State
	j.Mu.RUnlock()

	if state != job.StateRunning {
		dis.SendErrorReply(conn, dis.PbseBadReq, 0)
		return true
	}

	log.Printf("[SERVER] CheckpointJob %s requested", jobID)
	dis.SendOkReply(conn)
	return true
}

// handleJobObit processes a job obituary from a MOM daemon.
// This indicates a job has completed execution on the compute node.
func (s *Server) handleJobObit(conn net.Conn, r *dis.Reader, hdr *dis.RequestHeader, remote string, privileged bool) bool {
	jobID, exitStatus, attrs, err := dis.ReadJobObit(r)
	if err != nil {
		log.Printf("[SERVER] Error reading JobObit: %v", err)
		dis.SendErrorReply(conn, dis.PbseBadReq, 0)
		return false
	}

	// Verify the connection is from a trusted source.
	// Accept privileged port connections (legacy) or token-authenticated connections.
	_, tokenAuthed := s.authMap.Load(remote)
	if !privileged && !tokenAuthed {
		log.Printf("[SERVER] JobObit from unauthenticated source, rejecting")
		dis.SendErrorReply(conn, dis.PbseBadCred, 0)
		return false
	}

	j := s.jobMgr.GetJob(jobID)
	if j == nil {
		log.Printf("[SERVER] JobObit for unknown job %s", jobID)
		dis.SendOkReply(conn)
		return true
	}

	j.Mu.Lock()
	oldState := j.State
	j.ExitStatus = exitStatus
	j.SetState(job.StateComplete, job.SubstateComplete)

	// Apply resource usage from obit attributes
	for _, a := range attrs {
		if a.Name == "resources_used" && a.HasResc {
			j.ResourceUsed[a.Resc] = a.Value
		}
	}
	queueName := j.Queue
	execHost := j.ExecHost
	j.Mu.Unlock()

	// Release node resources
	if execHost != "" {
		s.releaseNodeResources(execHost, jobID)
	}

	// Update queue state counters
	if q := s.queueMgr.GetQueue(queueName); q != nil {
		q.TransferJobState(oldState, job.StateComplete)
	}

	// Persist the completed job
	s.saveJob(j)

	log.Printf("[SERVER] JobObit: %s exit=%d (was state=%d)", jobID, exitStatus, oldState)

	// Write E (ended) accounting record with full resource usage
	if s.acctLog != nil {
		j.Mu.RLock()
		j.CompTime = time.Now()
		info := s.buildJobInfo(j)
		j.Mu.RUnlock()
		if exitStatus < 0 || exitStatus > 128 {
			s.acctLog.RecordAborted(jobID, info)
		} else {
			s.acctLog.RecordEnded(jobID, info)
		}
	}

	// Resolve dependencies for jobs waiting on this completed job
	s.resolveDependencies(jobID, exitStatus)

	dis.SendOkReply(conn)
	return true
}

// handleISMessage processes IS (Information Status) protocol messages from MOMs.
// These messages carry node status updates, heartbeats, and GPU status.
func (s *Server) handleISMessage(conn net.Conn, r *dis.Reader, remote string) {
	// Protocol type (4) was already consumed. ReadISMessage reads version, command, ports, items.
	msg, err := dis.ReadISMessage(r)
	if err != nil {
		log.Printf("[SERVER] Error reading IS message from %s: %v", remote, err)
		return
	}

	// Extract hostname from the status items
	hostname := ""
	for _, item := range msg.Items {
		if strings.HasPrefix(item, "node=") {
			hostname = item[5:]
			break
		}
	}

	if hostname == "" {
		// Try to resolve from remote address
		host, _, _ := net.SplitHostPort(remote)
		if names, err := net.LookupAddr(host); err == nil && len(names) > 0 {
			hostname = strings.TrimSuffix(names[0], ".")
		} else {
			hostname = host
		}
	}

	// Find or auto-create the node
	n := s.nodeMgr.GetNode(hostname)
	if n == nil {
		log.Printf("[SERVER] IS message from unknown node %s, ignoring", hostname)
		return
	}

	// Apply status update to the node
	n.Mu.Lock()
	n.MomPort = msg.MomPort
	n.RmPort = msg.RmPort
	n.UpdateFromStatus(msg.Items)
	n.Mu.Unlock()

	log.Printf("[SERVER] IS status from %s: %d items, port=%d", hostname, len(msg.Items), msg.MomPort)

	// Send IS acknowledgement (protocol=4, version=3, command=IS_NULL=0)
	w := dis.NewWriter(conn)
	w.WriteInt(int64(dis.ISProtocolType))
	w.WriteInt(3)
	w.WriteInt(0) // IS_NULL acknowledgement
	w.Flush()
}

// --- Manager (qmgr) Sub-Handlers ---

// mgrCreateQueue creates a new execution queue.
func (s *Server) mgrCreateQueue(name string, attrs []dis.SvrAttrl) error {
	if s.queueMgr.GetQueue(name) != nil {
		return fmt.Errorf("queue %s already exists", name)
	}
	q := queue.NewQueue(name, queue.TypeExecution)
	s.applyQueueAttrs(q, attrs)
	s.queueMgr.AddQueue(q)
	s.saveQueue(q)
	return nil
}

func (s *Server) mgrDeleteQueue(name string) error {
	if !s.queueMgr.RemoveQueue(name) {
		return fmt.Errorf("queue %s not found", name)
	}
	// Remove queue file from disk
	os.Remove(filepath.Join(s.cfg.QueuesDir, name))
	return nil
}

func (s *Server) mgrSetQueue(name string, attrs []dis.SvrAttrl) error {
	q := s.queueMgr.GetQueue(name)
	if q == nil {
		return fmt.Errorf("queue %s not found", name)
	}
	s.applyQueueAttrs(q, attrs)
	s.saveQueue(q)
	return nil
}

func (s *Server) mgrUnsetQueue(name string, attrs []dis.SvrAttrl) error {
	q := s.queueMgr.GetQueue(name)
	if q == nil {
		return fmt.Errorf("queue %s not found", name)
	}
	// Unset attrs would reset them to defaults - simplified here
	q.Mu.Lock()
	for _, a := range attrs {
		delete(q.Attrs, a.Name)
	}
	q.Mu.Unlock()
	s.saveQueue(q)
	return nil
}

// mgrCreateNode adds a new compute node to the cluster.
func (s *Server) mgrCreateNode(name string, attrs []dis.SvrAttrl) error {
	// Parse np (number of processors) from attributes
	np := 1
	for _, a := range attrs {
		if a.Name == "np" || (a.Name == "resources_available" && a.Resc == "np") {
			fmt.Sscanf(a.Value, "%d", &np)
		}
	}
	n := s.nodeMgr.AddNode(name, np)
	s.applyNodeAttrs(n, attrs)
	s.saveNodes()
	return nil
}

func (s *Server) mgrDeleteNode(name string) error {
	if !s.nodeMgr.RemoveNode(name) {
		return fmt.Errorf("node %s not found", name)
	}
	s.saveNodes()
	return nil
}

func (s *Server) mgrSetNode(name string, attrs []dis.SvrAttrl) error {
	n := s.nodeMgr.GetNode(name)
	if n == nil {
		return fmt.Errorf("node %s not found", name)
	}
	s.applyNodeAttrs(n, attrs)
	s.saveNodes()
	return nil
}

func (s *Server) mgrSetServer(attrs []dis.SvrAttrl) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range attrs {
		switch a.Name {
		// Core
		case "scheduling":
			s.cfg.Scheduling = parseBool(a.Value)
		case "default_queue":
			s.cfg.DefaultQueue = a.Value
		// Scheduling & timing
		case "scheduler_iteration":
			fmt.Sscanf(a.Value, "%d", &s.cfg.SchedulerIteration)
		case "node_check_rate":
			fmt.Sscanf(a.Value, "%d", &s.cfg.NodeCheckRate)
		case "tcp_timeout":
			fmt.Sscanf(a.Value, "%d", &s.cfg.TCPTimeout)
		case "keep_completed":
			fmt.Sscanf(a.Value, "%d", &s.cfg.KeepCompleted)
		case "job_stat_rate":
			fmt.Sscanf(a.Value, "%d", &s.cfg.JobStatRate)
		case "poll_jobs":
			s.cfg.PollJobs = parseBool(a.Value)
		case "ping_rate":
			fmt.Sscanf(a.Value, "%d", &s.cfg.PingRate)
		case "job_start_timeout":
			fmt.Sscanf(a.Value, "%d", &s.cfg.JobStartTimeout)
		case "job_force_cancel_time":
			fmt.Sscanf(a.Value, "%d", &s.cfg.JobForceCancelTime)
		case "job_sync_timeout":
			fmt.Sscanf(a.Value, "%d", &s.cfg.JobSyncTimeout)
		// Logging
		case "log_events":
			fmt.Sscanf(a.Value, "%d", &s.cfg.LogLevel)
		case "log_file_max_size":
			fmt.Sscanf(a.Value, "%d", &s.cfg.LogFileMaxSize)
		case "log_file_roll_depth":
			fmt.Sscanf(a.Value, "%d", &s.cfg.LogFileRollDepth)
		case "log_keep_days":
			fmt.Sscanf(a.Value, "%d", &s.cfg.LogKeepDays)
		case "record_job_info":
			s.cfg.RecordJobInfo = parseBool(a.Value)
		case "record_job_script":
			s.cfg.RecordJobScript = parseBool(a.Value)
		case "job_log_file_max_size":
			fmt.Sscanf(a.Value, "%d", &s.cfg.JobLogFileMaxSize)
		case "job_log_file_roll_depth":
			fmt.Sscanf(a.Value, "%d", &s.cfg.JobLogFileRollDepth)
		case "job_log_keep_days":
			fmt.Sscanf(a.Value, "%d", &s.cfg.JobLogKeepDays)
		// ACL
		case "managers":
			s.cfg.Managers = mergeACLValue(s.cfg.Managers, a.Value, a.Op)
		case "operators":
			s.cfg.Operators = mergeACLValue(s.cfg.Operators, a.Value, a.Op)
		case "acl_host_enable":
			s.cfg.ACLHostEnable = parseBool(a.Value)
		case "acl_hosts":
			s.cfg.ACLHosts = mergeACLValue(s.cfg.ACLHosts, a.Value, a.Op)
		case "acl_user_enable":
			s.cfg.ACLUserEnable = parseBool(a.Value)
		case "acl_users":
			s.cfg.ACLUsers = mergeACLValue(s.cfg.ACLUsers, a.Value, a.Op)
		case "acl_roots":
			s.cfg.ACLRoots = mergeACLValue(s.cfg.ACLRoots, a.Value, a.Op)
		case "acl_logic_or":
			s.cfg.ACLLogicOr = parseBool(a.Value)
		case "acl_group_sloppy":
			s.cfg.ACLGroupSloppy = parseBool(a.Value)
		case "acl_user_hosts":
			s.cfg.ACLUserHosts = mergeACLValue(s.cfg.ACLUserHosts, a.Value, a.Op)
		case "acl_group_hosts":
			s.cfg.ACLGroupHosts = mergeACLValue(s.cfg.ACLGroupHosts, a.Value, a.Op)
		// Resource limits
		case "max_running":
			fmt.Sscanf(a.Value, "%d", &s.cfg.MaxRunning)
		case "max_user_run":
			fmt.Sscanf(a.Value, "%d", &s.cfg.MaxUserRun)
		case "max_group_run":
			fmt.Sscanf(a.Value, "%d", &s.cfg.MaxGroupRun)
		case "max_user_queuable":
			fmt.Sscanf(a.Value, "%d", &s.cfg.MaxUserQueuable)
		case "resources_available":
			if a.HasResc {
				s.cfg.ResourcesAvail[a.Resc] = a.Value
			}
		case "resources_default":
			if a.HasResc {
				s.cfg.ResourcesDefault[a.Resc] = a.Value
			}
		case "resources_max":
			if a.HasResc {
				s.cfg.ResourcesMax[a.Resc] = a.Value
			}
		case "resources_cost":
			if a.HasResc {
				s.cfg.ResourcesCost[a.Resc] = a.Value
			}
		// Mail
		case "mail_domain":
			s.cfg.MailDomain = a.Value
		case "mail_from":
			s.cfg.MailFrom = a.Value
		case "no_mail_force":
			s.cfg.NoMailForce = parseBool(a.Value)
		case "mail_subject_fmt":
			s.cfg.MailSubjectFmt = a.Value
		case "mail_body_fmt":
			s.cfg.MailBodyFmt = a.Value
		case "email_batch_seconds":
			fmt.Sscanf(a.Value, "%d", &s.cfg.EmailBatchSeconds)
		// Node & job policy
		case "default_node":
			s.cfg.DefaultNode = a.Value
		case "node_pack":
			s.cfg.NodePack = parseBool(a.Value)
		case "query_other_jobs":
			s.cfg.QueryOtherJobs = parseBool(a.Value)
		case "mom_job_sync":
			s.cfg.MOMJobSync = parseBool(a.Value)
		case "down_on_error":
			s.cfg.DownOnError = parseBool(a.Value)
		case "disable_server_id_check":
			s.cfg.DisableServerIdCheck = parseBool(a.Value)
		case "allow_node_submit":
			s.cfg.AllowNodeSubmit = parseBool(a.Value)
		case "allow_proxy_user":
			s.cfg.AllowProxyUser = parseBool(a.Value)
		case "auto_node_np":
			s.cfg.AutoNodeNP = parseBool(a.Value)
		case "np_default":
			fmt.Sscanf(a.Value, "%d", &s.cfg.NPDefault)
		case "job_nanny":
			s.cfg.JobNanny = parseBool(a.Value)
		case "owner_purge":
			s.cfg.OwnerPurge = parseBool(a.Value)
		case "copy_on_rerun":
			s.cfg.CopyOnRerun = parseBool(a.Value)
		case "job_exclusive_on_use":
			s.cfg.JobExclusiveOnUse = parseBool(a.Value)
		case "disable_automatic_requeue":
			s.cfg.DisableAutoRequeue = parseBool(a.Value)
		case "automatic_requeue_exit_code":
			fmt.Sscanf(a.Value, "%d", &s.cfg.AutoRequeueExitCode)
		case "dont_write_nodes_file":
			s.cfg.DontWriteNodesFile = parseBool(a.Value)
		// Job array & display
		case "max_job_array_size":
			fmt.Sscanf(a.Value, "%d", &s.cfg.MaxJobArraySize)
		case "max_slot_limit":
			fmt.Sscanf(a.Value, "%d", &s.cfg.MaxSlotLimit)
		case "clone_batch_size":
			fmt.Sscanf(a.Value, "%d", &s.cfg.CloneBatchSize)
		case "clone_batch_delay":
			fmt.Sscanf(a.Value, "%d", &s.cfg.CloneBatchDelay)
		case "moab_array_compatible":
			s.cfg.MoabArrayCompatible = parseBool(a.Value)
		case "display_job_server_suffix":
			s.cfg.DisplayJobServerSuffix = parseBool(a.Value)
		case "job_suffix_alias":
			s.cfg.JobSuffixAlias = a.Value
		case "use_jobs_subdirs":
			s.cfg.UseJobsSubdirs = parseBool(a.Value)
		// Kill & cancel timeouts
		case "kill_delay":
			fmt.Sscanf(a.Value, "%d", &s.cfg.KillDelay)
		case "user_kill_delay":
			fmt.Sscanf(a.Value, "%d", &s.cfg.UserKillDelay)
		case "exit_code_canceled_job":
			fmt.Sscanf(a.Value, "%d", &s.cfg.ExitCodeCanceledJob)
		case "timeout_for_job_delete":
			fmt.Sscanf(a.Value, "%d", &s.cfg.TimeoutForJobDelete)
		case "timeout_for_job_requeue":
			fmt.Sscanf(a.Value, "%d", &s.cfg.TimeoutForJobRequeue)
		// Hardware
		case "default_gpu_mode":
			s.cfg.DefaultGpuMode = a.Value
		case "idle_slot_limit":
			fmt.Sscanf(a.Value, "%d", &s.cfg.IdleSlotLimit)
		case "cgroup_per_task":
			s.cfg.CgroupPerTask = parseBool(a.Value)
		case "pass_cpu_clock":
			s.cfg.PassCpuClock = parseBool(a.Value)
		// Other
		case "submit_hosts":
			s.cfg.SubmitHosts = mergeACLValue(s.cfg.SubmitHosts, a.Value, a.Op)
		case "node_submit_exceptions":
			s.cfg.NodeSubmitExceptions = a.Value
		case "node_suffix":
			s.cfg.NodeSuffix = a.Value
		case "comment":
			s.cfg.Comment = a.Value
		case "lock_file_update_time":
			fmt.Sscanf(a.Value, "%d", &s.cfg.LockFileUpdateTime)
		case "lock_file_check_time":
			fmt.Sscanf(a.Value, "%d", &s.cfg.LockFileCheckTime)
		case "interactive_jobs_can_roam":
			s.cfg.InteractiveJobsCanRoam = parseBool(a.Value)
		case "legacy_vmem":
			s.cfg.LegacyVmem = parseBool(a.Value)
		case "ghost_array_recovery":
			s.cfg.GhostArrayRecovery = parseBool(a.Value)
		case "tcp_incoming_timeout":
			fmt.Sscanf(a.Value, "%d", &s.cfg.TCPIncomingTimeout)
		case "job_full_report_time":
			fmt.Sscanf(a.Value, "%d", &s.cfg.JobFullReportTime)
		default:
			log.Printf("[SERVER] Unknown server attribute: %s", a.Name)
		}
	}
	return nil
}

// parseBool handles True/true/1/False/false/0 boolean values.
func parseBool(val string) bool {
	return val == "True" || val == "true" || val == "1"
}

// mergeACLValue handles += (append) and = (replace) operations for ACL list attributes.
// Op 7 is INCR (+=), other ops replace.
func mergeACLValue(existing, newVal string, op int) string {
	if op == 7 && existing != "" {
		return existing + "," + newVal
	}
	return newVal
}

func (s *Server) mgrUnsetServer(attrs []dis.SvrAttrl) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	defaults := config.NewConfig(s.cfg.PBSHome)
	for _, a := range attrs {
		switch a.Name {
		case "scheduling":
			s.cfg.Scheduling = defaults.Scheduling
		case "default_queue":
			s.cfg.DefaultQueue = defaults.DefaultQueue
		case "scheduler_iteration":
			s.cfg.SchedulerIteration = defaults.SchedulerIteration
		case "node_check_rate":
			s.cfg.NodeCheckRate = defaults.NodeCheckRate
		case "tcp_timeout":
			s.cfg.TCPTimeout = defaults.TCPTimeout
		case "keep_completed":
			s.cfg.KeepCompleted = defaults.KeepCompleted
		case "log_events":
			s.cfg.LogLevel = defaults.LogLevel
		case "job_stat_rate":
			s.cfg.JobStatRate = 0
		case "poll_jobs":
			s.cfg.PollJobs = false
		case "ping_rate":
			s.cfg.PingRate = defaults.PingRate
		case "job_start_timeout":
			s.cfg.JobStartTimeout = defaults.JobStartTimeout
		case "job_force_cancel_time":
			s.cfg.JobForceCancelTime = 0
		case "job_sync_timeout":
			s.cfg.JobSyncTimeout = defaults.JobSyncTimeout
		case "log_file_max_size":
			s.cfg.LogFileMaxSize = 0
		case "log_file_roll_depth":
			s.cfg.LogFileRollDepth = defaults.LogFileRollDepth
		case "log_keep_days":
			s.cfg.LogKeepDays = 0
		case "record_job_info":
			s.cfg.RecordJobInfo = false
		case "record_job_script":
			s.cfg.RecordJobScript = false
		case "managers":
			s.cfg.Managers = ""
		case "operators":
			s.cfg.Operators = ""
		case "acl_host_enable":
			s.cfg.ACLHostEnable = false
		case "acl_hosts":
			s.cfg.ACLHosts = ""
		case "acl_user_enable":
			s.cfg.ACLUserEnable = false
		case "acl_users":
			s.cfg.ACLUsers = ""
		case "max_running":
			s.cfg.MaxRunning = 0
		case "max_user_run":
			s.cfg.MaxUserRun = 0
		case "max_group_run":
			s.cfg.MaxGroupRun = 0
		case "max_user_queuable":
			s.cfg.MaxUserQueuable = 0
		case "resources_available":
			if a.HasResc {
				delete(s.cfg.ResourcesAvail, a.Resc)
			}
		case "resources_default":
			if a.HasResc {
				delete(s.cfg.ResourcesDefault, a.Resc)
			}
		case "resources_max":
			if a.HasResc {
				delete(s.cfg.ResourcesMax, a.Resc)
			}
		case "mail_domain":
			s.cfg.MailDomain = ""
		case "mail_from":
			s.cfg.MailFrom = ""
		case "default_node":
			s.cfg.DefaultNode = ""
		case "node_pack":
			s.cfg.NodePack = false
		case "query_other_jobs":
			s.cfg.QueryOtherJobs = defaults.QueryOtherJobs
		case "mom_job_sync":
			s.cfg.MOMJobSync = defaults.MOMJobSync
		case "kill_delay":
			s.cfg.KillDelay = defaults.KillDelay
		case "max_job_array_size":
			s.cfg.MaxJobArraySize = defaults.MaxJobArraySize
		case "comment":
			s.cfg.Comment = ""
		case "display_job_server_suffix":
			s.cfg.DisplayJobServerSuffix = defaults.DisplayJobServerSuffix
		}
	}
	return nil
}

// --- Status Formatting ---

// formatJobStatus builds a StatusObject containing all job attributes for status reply.
func (s *Server) formatJobStatus(j *job.Job) dis.StatusObject {
	j.Mu.RLock()
	defer j.Mu.RUnlock()

	var attrs []dis.SvrAttrl

	// Helper to add a simple attribute
	add := func(name, value string) {
		if value != "" {
			attrs = append(attrs, dis.SvrAttrl{Name: name, Value: value, Op: 1})
		}
	}
	// Helper to add a resource attribute
	addResc := func(name, resc, value string) {
		if value != "" {
			attrs = append(attrs, dis.SvrAttrl{Name: name, HasResc: true, Resc: resc, Value: value, Op: 1})
		}
	}

	add("Job_Name", j.Name)
	add("Job_Owner", j.Owner)
	add("job_state", j.StateName())
	add("queue", j.Queue)
	add("server", j.Server)
	add("Checkpoint", j.Checkpoint)
	if j.Checkpoint == "" {
		add("Checkpoint", "u")
	}

	// Output/Error paths
	add("Output_Path", j.StdoutPath)
	add("Error_Path", j.StderrPath)
	add("Join_Path", j.JoinPath)
	add("Keep_Files", j.KeepFiles)

	// New attributes
	add("Account_Name", j.Account)
	add("Shell_Path_List", j.Shell)
	add("Mail_Points", j.MailPoints)
	add("Mail_Users", j.MailUsers)
	add("Rerunable", j.Rerunnable)
	add("Hold_Types", j.HoldTypes)
	add("User_List", j.UserList)
	add("depend", j.DependList)
	add("stagein", j.StageinList)
	add("stageout", j.StageoutList)
	add("group_list", j.GroupList)
	add("job_array_request", j.JobArrayReq)
	add("init_work_dir", j.InitWorkDir)
	add("comment", j.Comment)
	if j.Priority != 0 {
		add("Priority", strconv.Itoa(j.Priority))
	}
	if !j.ExecutionTime.IsZero() {
		add("Execution_Time", strconv.FormatInt(j.ExecutionTime.Unix(), 10))
	}

	// Execution info
	add("exec_host", j.ExecHost)
	if j.ExecPort > 0 {
		add("exec_port", strconv.Itoa(j.ExecPort))
	}
	if j.SessionID > 0 {
		add("session_id", strconv.Itoa(j.SessionID))
	}

	// Timing
	if !j.CreateTime.IsZero() {
		add("ctime", strconv.FormatInt(j.CreateTime.Unix(), 10))
	}
	if !j.QueueTime.IsZero() {
		add("qtime", strconv.FormatInt(j.QueueTime.Unix(), 10))
	}
	if !j.StartTime.IsZero() {
		add("start_time", strconv.FormatInt(j.StartTime.Unix(), 10))
	}
	if !j.CompTime.IsZero() {
		add("comp_time", strconv.FormatInt(j.CompTime.Unix(), 10))
	}
	if !j.MTime.IsZero() {
		add("mtime", strconv.FormatInt(j.MTime.Unix(), 10))
	}

	// Effective user/group
	add("euser", j.EUser)
	add("egroup", j.EGroup)

	// Exit status (only for completed/exiting jobs)
	if j.State == job.StateComplete || j.State == job.StateExiting {
		add("exit_status", strconv.Itoa(j.ExitStatus))
	}

	// Resource requests
	for k, v := range j.ResourceReq {
		addResc("Resource_List", k, v)
	}

	// Resource usage
	for k, v := range j.ResourceUsed {
		addResc("resources_used", k, v)
	}

	// Variable list
	if len(j.VariableList) > 0 {
		var parts []string
		for k, v := range j.VariableList {
			parts = append(parts, k+"="+v)
		}
		add("Variable_List", strings.Join(parts, ","))
	}

	add("hashname", j.HashName)
	add("fault_tolerant", j.FaultTolerant)
	add("job_radix", j.JobRadix)
	add("request_version", j.ReqVersion)

	return dis.StatusObject{Type: dis.MgrObjJob, Name: j.ID, Attrs: attrs}
}

// formatQueueStatus builds a StatusObject for a queue.
func (s *Server) formatQueueStatus(q *queue.Queue) dis.StatusObject {
	q.Mu.RLock()
	defer q.Mu.RUnlock()

	var attrs []dis.SvrAttrl
	add := func(name, value string) {
		attrs = append(attrs, dis.SvrAttrl{Name: name, Value: value, Op: 1})
	}

	if q.Type == queue.TypeExecution {
		add("queue_type", "Execution")
	} else {
		add("queue_type", "Route")
	}

	add("total_jobs", strconv.Itoa(q.TotalJobs))
	add("state_count", fmt.Sprintf("Transit:%d Queued:%d Held:%d Waiting:%d Running:%d Exiting:%d Complete:%d",
		q.StateJobs[0], q.StateJobs[1], q.StateJobs[2], q.StateJobs[3],
		q.StateJobs[4], q.StateJobs[5], q.StateJobs[6]))

	if q.Enabled {
		add("enabled", "True")
	} else {
		add("enabled", "False")
	}
	if q.Started {
		add("started", "True")
	} else {
		add("started", "False")
	}
	if q.MaxJobs > 0 {
		add("max_queuable", strconv.Itoa(q.MaxJobs))
	}
	if q.MaxRun > 0 {
		add("max_running", strconv.Itoa(q.MaxRun))
	}

	for k, v := range q.ResourceMax {
		attrs = append(attrs, dis.SvrAttrl{Name: "resources_max", HasResc: true, Resc: k, Value: v, Op: 1})
	}
	for k, v := range q.ResourceDflt {
		attrs = append(attrs, dis.SvrAttrl{Name: "resources_default", HasResc: true, Resc: k, Value: v, Op: 1})
	}

	return dis.StatusObject{Type: dis.MgrObjQueue, Name: q.Name, Attrs: attrs}
}

// formatNodeStatus builds a StatusObject for a compute node.
func (s *Server) formatNodeStatus(n *node.Node) dis.StatusObject {
	n.Mu.RLock()
	defer n.Mu.RUnlock()

	var attrs []dis.SvrAttrl
	add := func(name, value string) {
		attrs = append(attrs, dis.SvrAttrl{Name: name, Value: value, Op: 1})
	}

	add("state", n.StateName())
	add("power_state", n.PowerState)
	add("np", strconv.Itoa(n.NumProcs))
	add("ntype", "cluster")

	// Include status info from MOM
	for k, v := range n.Status {
		add(k, v)
	}

	// List jobs on this node
	if len(n.AssignedJobs) > 0 {
		add("jobs", strings.Join(n.AssignedJobs, ", "))
	}

	if len(n.Properties) > 0 {
		add("properties", strings.Join(n.Properties, ","))
	}
	if n.Note != "" {
		add("note", n.Note)
	}

	return dis.StatusObject{Type: dis.MgrObjNode, Name: n.Name, Attrs: attrs}
}

// formatServerStatus builds a StatusObject for the server itself.
func (s *Server) formatServerStatus() dis.StatusObject {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var attrs []dis.SvrAttrl
	add := func(name, value string) {
		attrs = append(attrs, dis.SvrAttrl{Name: name, Value: value, Op: 1})
	}
	addBool := func(name string, val bool) {
		if val {
			add(name, "True")
		} else {
			add(name, "False")
		}
	}
	addInt := func(name string, val int) {
		add(name, strconv.Itoa(val))
	}
	// Only report non-zero int values
	addIntNZ := func(name string, val int) {
		if val != 0 {
			add(name, strconv.Itoa(val))
		}
	}
	addStr := func(name, val string) {
		if val != "" {
			add(name, val)
		}
	}
	addResc := func(name string, m map[string]string) {
		for k, v := range m {
			attrs = append(attrs, dis.SvrAttrl{Name: name, HasResc: true, Resc: k, Value: v, Op: 1})
		}
	}

	// Core
	add("server_name", s.cfg.ServerName)
	add("server_host", s.cfg.ServerName)
	add("pbs_version", "7.0.0-go")
	add("server_state", "Active")
	addBool("scheduling", s.cfg.Scheduling)
	add("total_jobs", strconv.Itoa(s.jobMgr.JobCount()))
	add("state_count", fmt.Sprintf("Transit:%d Queued:%d Held:%d Waiting:%d Running:%d Exiting:%d Complete:%d",
		s.jobMgr.StateCount(0), s.jobMgr.StateCount(1), s.jobMgr.StateCount(2),
		s.jobMgr.StateCount(3), s.jobMgr.StateCount(4), s.jobMgr.StateCount(5),
		s.jobMgr.StateCount(6)))
	add("default_queue", s.cfg.DefaultQueue)

	// Scheduling & timing
	addInt("scheduler_iteration", s.cfg.SchedulerIteration)
	addInt("node_check_rate", s.cfg.NodeCheckRate)
	addInt("tcp_timeout", s.cfg.TCPTimeout)
	addInt("keep_completed", s.cfg.KeepCompleted)
	addIntNZ("job_stat_rate", s.cfg.JobStatRate)
	if s.cfg.PollJobs {
		addBool("poll_jobs", true)
	}
	addInt("ping_rate", s.cfg.PingRate)
	addInt("job_start_timeout", s.cfg.JobStartTimeout)
	addIntNZ("job_force_cancel_time", s.cfg.JobForceCancelTime)
	addInt("job_sync_timeout", s.cfg.JobSyncTimeout)
	add("next_job_number", strconv.Itoa(s.jobMgr.GetNextJobIDNum()))

	// Logging
	addInt("log_events", s.cfg.LogLevel)
	addIntNZ("log_file_max_size", s.cfg.LogFileMaxSize)
	addInt("log_file_roll_depth", s.cfg.LogFileRollDepth)
	addIntNZ("log_keep_days", s.cfg.LogKeepDays)
	if s.cfg.RecordJobInfo {
		addBool("record_job_info", true)
	}
	if s.cfg.RecordJobScript {
		addBool("record_job_script", true)
	}
	addIntNZ("job_log_file_max_size", s.cfg.JobLogFileMaxSize)
	addIntNZ("job_log_file_roll_depth", s.cfg.JobLogFileRollDepth)
	addIntNZ("job_log_keep_days", s.cfg.JobLogKeepDays)

	// ACL
	addStr("managers", s.cfg.Managers)
	addStr("operators", s.cfg.Operators)
	if s.cfg.ACLHostEnable {
		addBool("acl_host_enable", true)
	}
	addStr("acl_hosts", s.cfg.ACLHosts)
	if s.cfg.ACLUserEnable {
		addBool("acl_user_enable", true)
	}
	addStr("acl_users", s.cfg.ACLUsers)
	addStr("acl_roots", s.cfg.ACLRoots)
	if s.cfg.ACLLogicOr {
		addBool("acl_logic_or", true)
	}
	if s.cfg.ACLGroupSloppy {
		addBool("acl_group_sloppy", true)
	}
	addStr("acl_user_hosts", s.cfg.ACLUserHosts)
	addStr("acl_group_hosts", s.cfg.ACLGroupHosts)

	// Resource limits
	addIntNZ("max_running", s.cfg.MaxRunning)
	addIntNZ("max_user_run", s.cfg.MaxUserRun)
	addIntNZ("max_group_run", s.cfg.MaxGroupRun)
	addIntNZ("max_user_queuable", s.cfg.MaxUserQueuable)
	addResc("resources_available", s.cfg.ResourcesAvail)
	addResc("resources_default", s.cfg.ResourcesDefault)
	addResc("resources_max", s.cfg.ResourcesMax)
	addResc("resources_cost", s.cfg.ResourcesCost)

	// Mail
	addStr("mail_domain", s.cfg.MailDomain)
	addStr("mail_from", s.cfg.MailFrom)
	if s.cfg.NoMailForce {
		addBool("no_mail_force", true)
	}
	addStr("mail_subject_fmt", s.cfg.MailSubjectFmt)
	addStr("mail_body_fmt", s.cfg.MailBodyFmt)
	addIntNZ("email_batch_seconds", s.cfg.EmailBatchSeconds)

	// Node & job policy
	addStr("default_node", s.cfg.DefaultNode)
	if s.cfg.NodePack {
		addBool("node_pack", true)
	}
	addBool("query_other_jobs", s.cfg.QueryOtherJobs)
	addBool("mom_job_sync", s.cfg.MOMJobSync)
	if s.cfg.DownOnError {
		addBool("down_on_error", true)
	}
	if s.cfg.DisableServerIdCheck {
		addBool("disable_server_id_check", true)
	}
	if s.cfg.AllowNodeSubmit {
		addBool("allow_node_submit", true)
	}
	if s.cfg.AllowProxyUser {
		addBool("allow_proxy_user", true)
	}
	if s.cfg.AutoNodeNP {
		addBool("auto_node_np", true)
	}
	addIntNZ("np_default", s.cfg.NPDefault)
	if s.cfg.JobNanny {
		addBool("job_nanny", true)
	}
	if s.cfg.OwnerPurge {
		addBool("owner_purge", true)
	}
	if s.cfg.CopyOnRerun {
		addBool("copy_on_rerun", true)
	}
	if s.cfg.JobExclusiveOnUse {
		addBool("job_exclusive_on_use", true)
	}
	if s.cfg.DisableAutoRequeue {
		addBool("disable_automatic_requeue", true)
	}
	if s.cfg.AutoRequeueExitCode >= 0 {
		addInt("automatic_requeue_exit_code", s.cfg.AutoRequeueExitCode)
	}
	if s.cfg.DontWriteNodesFile {
		addBool("dont_write_nodes_file", true)
	}

	// Job array & display
	addInt("max_job_array_size", s.cfg.MaxJobArraySize)
	addIntNZ("max_slot_limit", s.cfg.MaxSlotLimit)
	addInt("clone_batch_size", s.cfg.CloneBatchSize)
	addInt("clone_batch_delay", s.cfg.CloneBatchDelay)
	if s.cfg.MoabArrayCompatible {
		addBool("moab_array_compatible", true)
	}
	addBool("display_job_server_suffix", s.cfg.DisplayJobServerSuffix)
	addStr("job_suffix_alias", s.cfg.JobSuffixAlias)
	if s.cfg.UseJobsSubdirs {
		addBool("use_jobs_subdirs", true)
	}

	// Kill & cancel timeouts
	addInt("kill_delay", s.cfg.KillDelay)
	addIntNZ("user_kill_delay", s.cfg.UserKillDelay)
	addInt("exit_code_canceled_job", s.cfg.ExitCodeCanceledJob)
	addInt("timeout_for_job_delete", s.cfg.TimeoutForJobDelete)
	addInt("timeout_for_job_requeue", s.cfg.TimeoutForJobRequeue)

	// Hardware
	addStr("default_gpu_mode", s.cfg.DefaultGpuMode)
	addIntNZ("idle_slot_limit", s.cfg.IdleSlotLimit)
	if s.cfg.CgroupPerTask {
		addBool("cgroup_per_task", true)
	}
	if s.cfg.PassCpuClock {
		addBool("pass_cpu_clock", true)
	}

	// Other
	addStr("submit_hosts", s.cfg.SubmitHosts)
	addStr("node_submit_exceptions", s.cfg.NodeSubmitExceptions)
	addStr("node_suffix", s.cfg.NodeSuffix)
	addStr("comment", s.cfg.Comment)
	addIntNZ("lock_file_update_time", s.cfg.LockFileUpdateTime)
	addIntNZ("lock_file_check_time", s.cfg.LockFileCheckTime)
	if s.cfg.InteractiveJobsCanRoam {
		addBool("interactive_jobs_can_roam", true)
	}
	if s.cfg.LegacyVmem {
		addBool("legacy_vmem", true)
	}
	if s.cfg.GhostArrayRecovery {
		addBool("ghost_array_recovery", true)
	}
	addIntNZ("tcp_incoming_timeout", s.cfg.TCPIncomingTimeout)
	addIntNZ("job_full_report_time", s.cfg.JobFullReportTime)

	return dis.StatusObject{Type: dis.MgrObjServer, Name: s.cfg.ServerName, Attrs: attrs}
}

// --- Enforcement Hooks ---

// enforceSubmitLimits checks ACLs and resource limits before accepting a job.
func (s *Server) enforceSubmitLimits(j *job.Job, user string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Check user ACL: if acl_user_enable is set, only listed users may submit
	if s.cfg.ACLUserEnable && s.cfg.ACLUsers != "" {
		if !aclContains(s.cfg.ACLUsers, extractUser(user)) {
			return fmt.Errorf("user %s not in acl_users", user)
		}
	}

	// Check max_user_queuable: limit number of queued jobs per user
	if s.cfg.MaxUserQueuable > 0 {
		count := s.jobMgr.CountJobsByOwner(user)
		if count >= s.cfg.MaxUserQueuable {
			return fmt.Errorf("user %s exceeds max_user_queuable (%d)", user, s.cfg.MaxUserQueuable)
		}
	}

	return nil
}

// enforceRunLimits checks if a job is allowed to run based on server limits.
// Returns true if the job may be dispatched.
func (s *Server) enforceRunLimits(j *job.Job) bool {
	// Check server-wide max_running by counting actual running jobs
	if s.cfg.MaxRunning > 0 {
		running := s.jobMgr.CountByState(job.StateRunning)
		if running >= s.cfg.MaxRunning {
			log.Printf("[LIMIT] Job %s blocked: max_running=%d, running=%d", j.ID, s.cfg.MaxRunning, running)
			return false
		}
	}

	j.Mu.RLock()
	owner := j.Owner
	group := j.EGroup
	j.Mu.RUnlock()

	// Check per-user max_user_run
	if s.cfg.MaxUserRun > 0 {
		count := s.jobMgr.CountRunningByOwner(owner)
		if count >= s.cfg.MaxUserRun {
			log.Printf("[LIMIT] Job %s blocked: max_user_run=%d, user %s running=%d", j.ID, s.cfg.MaxUserRun, owner, count)
			return false
		}
	}

	// Check per-group max_group_run
	if s.cfg.MaxGroupRun > 0 && group != "" {
		count := s.jobMgr.CountRunningByGroup(group)
		if count >= s.cfg.MaxGroupRun {
			log.Printf("[LIMIT] Job %s blocked: max_group_run=%d, group %s running=%d", j.ID, s.cfg.MaxGroupRun, group, count)
			return false
		}
	}

	return true
}

// aclContains checks if a user is in a comma-separated ACL list.
// Supports both "user" and "user@host" entries.
func aclContains(aclList, user string) bool {
	for _, entry := range strings.Split(aclList, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if entry == user || extractUser(entry) == user {
			return true
		}
	}
	return false
}

// extractUser returns the username part from "user@host" or just "user".
func extractUser(s string) string {
	if idx := strings.Index(s, "@"); idx >= 0 {
		return s[:idx]
	}
	return s
}

// --- Attribute Application Helpers ---

// applyJobAttrs applies svrattrl attributes to a Job object.
func (s *Server) applyJobAttrs(j *job.Job, attrs []dis.SvrAttrl) {
	for _, a := range attrs {
		switch a.Name {
		case "Job_Name":
			j.Name = a.Value
		case "Job_Owner":
			j.Owner = a.Value
		case "queue":
			j.Queue = a.Value
		case "Output_Path":
			j.StdoutPath = a.Value
		case "Error_Path":
			j.StderrPath = a.Value
		case "Join_Path":
			j.JoinPath = a.Value
		case "Keep_Files":
			j.KeepFiles = a.Value
		case "Checkpoint":
			j.Checkpoint = a.Value
		case "Shell_Path_List":
			j.Shell = a.Value
		case "Account_Name":
			j.Account = a.Value
		case "Mail_Points":
			j.MailPoints = a.Value
		case "Mail_Users":
			j.MailUsers = a.Value
		case "Priority":
			fmt.Sscanf(a.Value, "%d", &j.Priority)
		case "Rerunable":
			j.Rerunnable = a.Value
		case "Hold_Types":
			j.HoldTypes = a.Value
			// Apply hold: if hold types are set, transition to Held state
			if a.Value != "" && a.Value != "n" {
				j.SetState(job.StateHeld, job.SubstateHeld)
			}
		case "Execution_Time":
			if ts, err := strconv.ParseInt(a.Value, 10, 64); err == nil {
				j.ExecutionTime = time.Unix(ts, 0)
				// Deferred execution: place in Waiting state if time is in the future
				if j.ExecutionTime.After(time.Now()) {
					j.SetState(job.StateWaiting, job.SubstateWaiting)
				}
			}
		case "User_List":
			j.UserList = a.Value
		case "depend":
			j.DependList = a.Value
		case "stagein":
			j.StageinList = a.Value
		case "stageout":
			j.StageoutList = a.Value
		case "group_list":
			j.GroupList = a.Value
		case "job_array_request":
			j.JobArrayReq = a.Value
		case "init_work_dir":
			j.InitWorkDir = a.Value
		case "init_root_dir":
			j.RootDir = a.Value
		case "comment":
			j.Comment = a.Value
		case "euser":
			j.EUser = a.Value
		case "egroup":
			j.EGroup = a.Value
		case "exec_host":
			j.ExecHost = a.Value
		case "exec_port":
			fmt.Sscanf(a.Value, "%d", &j.ExecPort)
		case "hashname":
			j.HashName = a.Value
		case "fault_tolerant":
			j.FaultTolerant = a.Value
		case "job_radix":
			j.JobRadix = a.Value
		case "request_version":
			j.ReqVersion = a.Value
		case "start_time":
			if ts, err := strconv.ParseInt(a.Value, 10, 64); err == nil {
				j.StartTime = time.Unix(ts, 0)
			}
		case "Resource_List":
			if a.HasResc {
				j.ResourceReq[a.Resc] = a.Value
			}
		case "resources_used":
			if a.HasResc {
				j.ResourceUsed[a.Resc] = a.Value
			}
		case "Variable_List":
			// Parse "KEY1=val1,KEY2=val2" format
			for _, pair := range strings.Split(a.Value, ",") {
				if idx := strings.Index(pair, "="); idx > 0 {
					j.VariableList[pair[:idx]] = pair[idx+1:]
				}
			}
		case "server":
			j.Server = a.Value
		default:
			j.Attrs[a.Name] = a.Value
		}
	}
	// Derive defaults
	if j.EUser == "" && j.Owner != "" {
		if idx := strings.Index(j.Owner, "@"); idx > 0 {
			j.EUser = j.Owner[:idx]
		}
	}
	if j.HashName == "" {
		j.HashName = j.ID
	}
}

// applyQueueAttrs applies svrattrl attributes to a Queue object.
func (s *Server) applyQueueAttrs(q *queue.Queue, attrs []dis.SvrAttrl) {
	q.Mu.Lock()
	defer q.Mu.Unlock()
	for _, a := range attrs {
		switch a.Name {
		case "queue_type":
			if strings.EqualFold(a.Value, "Execution") || strings.EqualFold(a.Value, "execution") || a.Value == "e" {
				q.Type = queue.TypeExecution
			} else {
				q.Type = queue.TypeRoute
			}
		case "enabled":
			q.Enabled = (a.Value == "True" || a.Value == "true" || a.Value == "1")
		case "started":
			q.Started = (a.Value == "True" || a.Value == "true" || a.Value == "1")
		case "max_queuable":
			fmt.Sscanf(a.Value, "%d", &q.MaxJobs)
		case "max_running":
			fmt.Sscanf(a.Value, "%d", &q.MaxRun)
		case "resources_max":
			if a.HasResc {
				q.ResourceMax[a.Resc] = a.Value
			}
		case "resources_min":
			if a.HasResc {
				q.ResourceMin[a.Resc] = a.Value
			}
		case "resources_default":
			if a.HasResc {
				q.ResourceDflt[a.Resc] = a.Value
			}
		default:
			q.Attrs[a.Name] = a.Value
		}
	}
}

// applyNodeAttrs applies svrattrl attributes to a Node object.
func (s *Server) applyNodeAttrs(n *node.Node, attrs []dis.SvrAttrl) {
	n.Mu.Lock()
	defer n.Mu.Unlock()
	for _, a := range attrs {
		switch a.Name {
		case "state":
			switch strings.ToLower(a.Value) {
			case "offline":
				n.State |= node.StateOffline
			case "free":
				n.State &^= node.StateOffline
			}
		case "np":
			fmt.Sscanf(a.Value, "%d", &n.NumProcs)
			n.SlotsTotal = n.NumProcs
		case "properties":
			n.Properties = strings.Split(a.Value, ",")
		case "note":
			n.Note = a.Value
		default:
			n.Attrs[a.Name] = a.Value
		}
	}
}

// --- Background Tasks ---

// startBackgroundTasks launches periodic background operations.
func (s *Server) startBackgroundTasks() {
	// Read scheduler mode from sched_config if present
	s.loadSchedConfig()

	// Built-in scheduler: only start when scheduler_mode is "builtin"
	schedInterval := time.Duration(s.cfg.SchedulerIteration) * time.Second
	if schedInterval < 5*time.Second {
		schedInterval = 5 * time.Second
	}
	s.schedTicker = time.NewTicker(schedInterval)
	if s.cfg.SchedulerMode == "builtin" {
		go s.schedulerLoop()
		log.Printf("[SERVER] Built-in FIFO scheduler enabled (interval=%ds)", s.cfg.SchedulerIteration)
	} else {
		log.Printf("[SERVER] External scheduler mode — built-in scheduler disabled")
	}

	// Node health check
	nodeCheckInterval := time.Duration(s.cfg.NodeCheckRate) * time.Second
	if nodeCheckInterval < 30*time.Second {
		nodeCheckInterval = 30 * time.Second
	}
	s.nodeTicker = time.NewTicker(nodeCheckInterval)
	go s.nodeCheckLoop()

	// Completed job cleanup
	go s.completedJobCleanup()
}

// schedulerLoop runs the built-in FIFO scheduler on a timer.
// It picks queued jobs and dispatches them to available nodes.
func (s *Server) schedulerLoop() {
	for {
		select {
		case <-s.done:
			return
		case <-s.schedTicker.C:
			if s.cfg.Scheduling {
				s.runScheduler()
			}
		}
	}
}

// runScheduler is the built-in FIFO job scheduler.
// It iterates through queued jobs and dispatches them to free nodes.
// It also promotes Waiting jobs whose execution time has passed.
func (s *Server) runScheduler() {
	// Check for Waiting jobs whose deferred execution time has passed
	s.promoteWaitingJobs()

	queued := s.jobMgr.QueuedJobs()
	if len(queued) == 0 {
		return
	}

	// Sort by queue time (FIFO order)
	sort.Slice(queued, func(i, k int) bool {
		return queued[i].QueueTime.Before(queued[k].QueueTime)
	})

	for _, j := range queued {
		j.Mu.RLock()
		if j.State != job.StateQueued {
			j.Mu.RUnlock()
			continue
		}
		j.Mu.RUnlock()

		s.scheduleJob(j)
	}
}

// promoteWaitingJobs transitions jobs from Waiting to Queued state
// when their deferred execution time (-a) has passed.
func (s *Server) promoteWaitingJobs() {
	now := time.Now()
	for _, j := range s.jobMgr.AllJobs() {
		j.Mu.RLock()
		state := j.State
		execTime := j.ExecutionTime
		jobID := j.ID
		j.Mu.RUnlock()

		if state == job.StateWaiting && !execTime.IsZero() && now.After(execTime) {
			s.jobMgr.UpdateJobState(jobID, job.StateQueued, job.SubstateQueued)
			log.Printf("[SCHED] Job %s execution time reached, moved to Queued", jobID)
		}
	}
}

// checkDependencies verifies that all job dependencies are satisfied.
// Returns true if the job can run, false if it must wait.
// Supports: afterok, afternotok, afterany, before, beforeok, beforenotok, on.
func (s *Server) checkDependencies(j *job.Job) bool {
	j.Mu.RLock()
	depStr := j.DependList
	j.Mu.RUnlock()

	if depStr == "" {
		return true // no dependencies
	}

	// Parse dependency string: "type:jobid[:jobid][,type:jobid...]"
	for _, clause := range strings.Split(depStr, ",") {
		parts := strings.SplitN(clause, ":", 2)
		if len(parts) < 2 {
			continue
		}
		depType := parts[0]
		depJobIDs := strings.Split(parts[1], ":")

		for _, depID := range depJobIDs {
			depID = strings.TrimSpace(depID)
			if depID == "" {
				continue
			}
			depJob := s.jobMgr.GetJob(depID)

			switch depType {
			case "afterok":
				// Dependent job must have completed successfully (exit=0)
				if depJob == nil {
					continue // job purged = treated as completed ok
				}
				depJob.Mu.RLock()
				st := depJob.State
				exit := depJob.ExitStatus
				depJob.Mu.RUnlock()
				if st != job.StateComplete {
					return false // still running or queued
				}
				if exit != 0 {
					// Dependency failed — cancel this job
					j.Mu.Lock()
					j.Comment = fmt.Sprintf("dependency %s failed (exit=%d)", depID, exit)
					j.Mu.Unlock()
					return false
				}

			case "afternotok":
				// Dependent job must have completed with non-zero exit
				if depJob == nil {
					return false // purged = assume success, so not satisfied
				}
				depJob.Mu.RLock()
				st := depJob.State
				exit := depJob.ExitStatus
				depJob.Mu.RUnlock()
				if st != job.StateComplete {
					return false
				}
				if exit == 0 {
					return false // it succeeded, so afternotok not met
				}

			case "afterany":
				// Dependent job must have completed (any exit status)
				if depJob == nil {
					continue // purged = completed
				}
				depJob.Mu.RLock()
				st := depJob.State
				depJob.Mu.RUnlock()
				if st != job.StateComplete {
					return false
				}

			case "before", "beforeok", "beforenotok", "beforeany":
				// "before" types mean: this job must run before the specified job.
				// The current job is allowed to run; the target is blocked until this one finishes.
				// So for the current job, "before" is always satisfied.
				continue

			case "on":
				// Count-based dependency — simplified: treat as no-op
				continue

			default:
				log.Printf("[SERVER] Unknown dependency type %q for job %s", depType, j.ID)
				continue
			}
		}
	}
	return true
}

// resolveDependencies is called when a job completes to release dependent jobs.
// It scans all queued/waiting jobs for dependencies on the completed job.
func (s *Server) resolveDependencies(completedJobID string, exitStatus int) {
	for _, j := range s.jobMgr.AllJobs() {
		j.Mu.RLock()
		depStr := j.DependList
		state := j.State
		jobID := j.ID
		j.Mu.RUnlock()

		if depStr == "" || (state != job.StateQueued && state != job.StateHeld && state != job.StateWaiting) {
			continue
		}

		// Check if this job has a dependency on the completed job
		if !strings.Contains(depStr, completedJobID) {
			continue
		}

		log.Printf("[SERVER] Job %s has dependency on completed %s, rechecking", jobID, completedJobID)
	}
}

// scheduleJob attempts to place a single job on a compute node and dispatch it.
func (s *Server) scheduleJob(j *job.Job) {
	j.Mu.RLock()
	if j.State != job.StateQueued {
		j.Mu.RUnlock()
		return
	}
	j.Mu.RUnlock()

	// Check job dependencies before scheduling
	if !s.checkDependencies(j) {
		return // Dependencies not yet satisfied
	}

	// Check server-wide and per-user/group run limits
	if !s.enforceRunLimits(j) {
		return // Limits exceeded, try again next cycle
	}

	// Find a node with available slots
	neededSlots := 1
	n, slots := s.nodeMgr.FindNodeForJob(neededSlots)
	if n == nil {
		return // No free nodes, try again next cycle
	}

	// Reserve the node for this job
	n.Mu.Lock()
	n.AssignJob(j.ID, slots)
	execHost := fmt.Sprintf("%s/0", n.Name)
	momPort := n.MomPort
	n.Mu.Unlock()

	// Update job state to Running
	j.Mu.Lock()
	j.ExecHost = execHost
	j.ExecPort = momPort
	oldState := j.State
	queueName := j.Queue
	j.Mu.Unlock()

	// Use manager's UpdateJobState to keep stateCounts in sync
	s.jobMgr.UpdateJobState(j.ID, job.StateRunning, job.SubstateRunning)

	// Update queue counters
	if q := s.queueMgr.GetQueue(queueName); q != nil {
		q.TransferJobState(oldState, job.StateRunning)
	}

	// Persist job state change
	s.saveJob(j)

	log.Printf("[SCHED] Dispatching job %s to %s (port %d)", j.ID, execHost, momPort)

	// Write S (started) accounting record
	if s.acctLog != nil {
		j.Mu.RLock()
		info := s.buildJobInfo(j)
		j.Mu.RUnlock()
		s.acctLog.RecordStarted(j.ID, info)
	}

	// Send job to MOM in a background goroutine
	go s.dispatchJobToMOM(j, n)
}

// dispatchJobToMOM sends a job (QueueJob + JobScript + Commit) to a MOM daemon.
// This follows the same protocol sequence that the C pbs_server uses.
func (s *Server) dispatchJobToMOM(j *job.Job, n *node.Node) {
	n.Mu.RLock()
	momAddr := fmt.Sprintf("%s:%d", n.Name, n.MomPort)
	n.Mu.RUnlock()

	// Connect to MOM from a privileged port (required for authentication)
	conn, err := dialPrivileged(momAddr)
	if err != nil {
		log.Printf("[SCHED] Failed to connect to MOM %s: %v", momAddr, err)
		s.undoJobDispatch(j, n)
		return
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(30 * time.Second))

	j.Mu.RLock()
	jobID := j.ID
	script := j.Script
	jobAttrs := s.buildMOMJobAttrs(j)
	j.Mu.RUnlock()

	w := dis.NewWriter(conn)
	r := dis.NewReader(conn)

	// Step 1: Send QueueJob request with all job attributes
	if err := s.sendQueueJobToMOM(w, jobID, jobAttrs); err != nil {
		log.Printf("[SCHED] QueueJob to MOM failed for %s: %v", jobID, err)
		s.undoJobDispatch(j, n)
		return
	}

	// Read QueueJob reply
	code, _, _, _, err := dis.ReadReply(r)
	if err != nil || code != 0 {
		log.Printf("[SCHED] QueueJob reply error for %s: code=%d err=%v", jobID, code, err)
		s.undoJobDispatch(j, n)
		return
	}

	// Step 2: Send JobScript
	if err := s.sendJobScriptToMOM(w, jobID, script); err != nil {
		log.Printf("[SCHED] JobScript to MOM failed for %s: %v", jobID, err)
		s.undoJobDispatch(j, n)
		return
	}

	// Read JobScript reply
	code, _, _, _, err = dis.ReadReply(r)
	if err != nil || code != 0 {
		log.Printf("[SCHED] JobScript reply error for %s: code=%d err=%v", jobID, code, err)
		s.undoJobDispatch(j, n)
		return
	}

	// Step 3: Send Commit (skip RdytoCommit for version >= 610)
	if err := s.sendCommitToMOM(w, jobID); err != nil {
		log.Printf("[SCHED] Commit to MOM failed for %s: %v", jobID, err)
		s.undoJobDispatch(j, n)
		return
	}

	// Read Commit reply (contains session ID as text)
	code, _, choice, data, err := dis.ReadReply(r)
	if err != nil || code != 0 {
		log.Printf("[SCHED] Commit reply error for %s: code=%d err=%v", jobID, code, err)
		s.undoJobDispatch(j, n)
		return
	}

	// Extract session ID from reply
	if choice == dis.ReplyChoiceText && data != "" {
		if sid, err := strconv.Atoi(data); err == nil {
			j.Mu.Lock()
			j.SessionID = sid
			j.Mu.Unlock()
		}
	}

	// Send Disconnect
	dis.WriteRequestHeader(w, dis.BatchReqDisconnect, s.cfg.ServerName)
	dis.WriteReqExtend(w, "")
	w.Flush()

	log.Printf("[SCHED] Job %s dispatched to MOM successfully", jobID)
}

// sendQueueJobToMOM writes a QueueJob request to a MOM.
func (s *Server) sendQueueJobToMOM(w *dis.Writer, jobID string, attrs []dis.SvrAttrl) error {
	if err := dis.WriteRequestHeader(w, dis.BatchReqQueueJob, s.cfg.ServerName); err != nil {
		return err
	}
	if err := w.WriteString(jobID); err != nil {
		return err
	}
	if err := w.WriteString(s.cfg.ServerName); err != nil {
		return err
	}
	if err := dis.WriteSvrAttrl(w, attrs); err != nil {
		return err
	}
	if err := dis.WriteReqExtend(w, ""); err != nil {
		return err
	}
	return w.Flush()
}

// sendJobScriptToMOM writes a JobScript request to a MOM.
func (s *Server) sendJobScriptToMOM(w *dis.Writer, jobID, script string) error {
	if err := dis.WriteRequestHeader(w, dis.BatchReqJobScript, s.cfg.ServerName); err != nil {
		return err
	}
	// seq=0, type=0, size=len(script)
	if err := w.WriteUint(0); err != nil {
		return err
	}
	if err := w.WriteUint(0); err != nil {
		return err
	}
	if err := w.WriteUint(uint64(len(script))); err != nil {
		return err
	}
	if err := w.WriteString(jobID); err != nil {
		return err
	}
	if err := w.WriteString(script); err != nil {
		return err
	}
	if err := dis.WriteReqExtend(w, ""); err != nil {
		return err
	}
	return w.Flush()
}

// sendCommitToMOM writes a Commit request to a MOM.
func (s *Server) sendCommitToMOM(w *dis.Writer, jobID string) error {
	if err := dis.WriteRequestHeader(w, dis.BatchReqCommit, s.cfg.ServerName); err != nil {
		return err
	}
	if err := w.WriteString(jobID); err != nil {
		return err
	}
	if err := dis.WriteReqExtend(w, ""); err != nil {
		return err
	}
	return w.Flush()
}

// buildMOMJobAttrs constructs the attribute list to send with QueueJob to MOM.
func (s *Server) buildMOMJobAttrs(j *job.Job) []dis.SvrAttrl {
	var attrs []dis.SvrAttrl
	add := func(name, value string) {
		if value != "" {
			attrs = append(attrs, dis.SvrAttrl{Name: name, Value: value, Op: 2})
		}
	}
	addResc := func(name, resc, value string) {
		if value != "" {
			attrs = append(attrs, dis.SvrAttrl{Name: name, HasResc: true, Resc: resc, Value: value, Op: 2})
		}
	}

	add("Job_Name", j.Name)
	add("Job_Owner", j.Owner)
	add("queue", j.Queue)
	add("server", j.Server)
	add("Checkpoint", j.Checkpoint)
	if j.Checkpoint == "" {
		add("Checkpoint", "u")
	}
	add("Error_Path", j.StderrPath)
	add("exec_host", j.ExecHost)
	add("exec_port", strconv.Itoa(j.ExecPort))
	add("Join_Path", j.JoinPath)
	if j.JoinPath == "" {
		add("Join_Path", "n")
	}
	add("Keep_Files", j.KeepFiles)
	if j.KeepFiles == "" {
		add("Keep_Files", "n")
	}
	add("Output_Path", j.StdoutPath)

	// Resource list
	for k, v := range j.ResourceReq {
		addResc("Resource_List", k, v)
	}

	// Variable list
	if len(j.VariableList) > 0 {
		var parts []string
		for k, v := range j.VariableList {
			parts = append(parts, k+"="+v)
		}
		add("Variable_List", strings.Join(parts, ","))
	}

	add("euser", j.EUser)
	add("egroup", j.EGroup)
	add("hashname", j.HashName)
	add("start_time", strconv.FormatInt(time.Now().Unix(), 10))
	add("fault_tolerant", "False")
	add("job_radix", "0")
	add("request_version", "1")

	// Pass shell, staging specs, and rerunnable flag to MOM
	add("Shell_Path_List", j.Shell)
	add("stagein", j.StageinList)
	add("stageout", j.StageoutList)
	if j.Rerunnable != "" {
		add("Rerunable", j.Rerunnable)
	}

	return attrs
}

// undoJobDispatch reverts a failed job dispatch (release node, requeue job).
func (s *Server) undoJobDispatch(j *job.Job, n *node.Node) {
	j.Mu.Lock()
	jobID := j.ID
	queueName := j.Queue
	j.SetState(job.StateQueued, job.SubstateQueued)
	j.ExecHost = ""
	j.ExecPort = 0
	j.Mu.Unlock()

	n.Mu.Lock()
	n.ReleaseJob(jobID, 1)
	n.Mu.Unlock()

	if q := s.queueMgr.GetQueue(queueName); q != nil {
		q.TransferJobState(job.StateRunning, job.StateQueued)
	}

	log.Printf("[SCHED] Reverted dispatch for job %s", jobID)
}

// releaseNodeResources frees node slots when a job completes.
func (s *Server) releaseNodeResources(execHost, jobID string) {
	// Parse exec_host format: "nodename/slot+nodename/slot"
	parts := strings.Split(execHost, "+")
	for _, part := range parts {
		nodeName := strings.Split(part, "/")[0]
		if n := s.nodeMgr.GetNode(nodeName); n != nil {
			n.Mu.Lock()
			n.ReleaseJob(jobID, 1)
			n.Mu.Unlock()
		}
	}
}

// sendDeleteToMOM sends a DeleteJob request to the MOM running the job.
func (s *Server) sendDeleteToMOM(j *job.Job) {
	j.Mu.RLock()
	execHost := j.ExecHost
	execPort := j.ExecPort
	jobID := j.ID
	j.Mu.RUnlock()

	if execHost == "" {
		return
	}

	nodeName := strings.Split(execHost, "/")[0]
	port := execPort
	if port == 0 {
		port = 15002
	}

	addr := fmt.Sprintf("%s:%d", nodeName, port)
	conn, err := dialPrivileged(addr)
	if err != nil {
		log.Printf("[SERVER] Failed to connect to MOM for delete: %v", err)
		return
	}
	defer conn.Close()

	w := dis.NewWriter(conn)
	dis.WriteRequestHeader(w, dis.BatchReqDeleteJob, s.cfg.ServerName)
	w.WriteString(jobID)
	dis.WriteReqExtend(w, "")
	w.Flush()

	// Read reply
	r := dis.NewReader(conn)
	dis.ReadReply(r)
}

// nodeCheckLoop periodically checks node health.
func (s *Server) nodeCheckLoop() {
	for {
		select {
		case <-s.done:
			return
		case <-s.nodeTicker.C:
			s.checkNodes()
		}
	}
}

// checkNodes verifies that all nodes are still responsive.
func (s *Server) checkNodes() {
	for _, n := range s.nodeMgr.AllNodes() {
		n.Mu.RLock()
		name := n.Name
		lastUpdate := n.LastUpdate
		isDown := n.IsDown()
		n.Mu.RUnlock()

		// Mark node as down if we haven't heard from it in 5 minutes
		if !isDown && time.Since(lastUpdate) > 5*time.Minute && !lastUpdate.IsZero() {
			log.Printf("[SERVER] Node %s appears down (no update for %v)", name, time.Since(lastUpdate))
			s.nodeMgr.MarkNodeDown(name)
		}
	}
}

// completedJobCleanup periodically removes old completed jobs.
func (s *Server) completedJobCleanup() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			keepDuration := time.Duration(s.cfg.KeepCompleted) * time.Second
			for _, j := range s.jobMgr.CompletedJobs() {
				j.Mu.RLock()
				compTime := j.CompTime
				jobID := j.ID
				queueName := j.Queue
				j.Mu.RUnlock()

				if !compTime.IsZero() && time.Since(compTime) > keepDuration {
					if q := s.queueMgr.GetQueue(queueName); q != nil {
						q.DecrJobCount(job.StateComplete)
					}
					s.removeJobFiles(jobID)
					s.jobMgr.RemoveJob(jobID)
					log.Printf("[SERVER] Purged completed job %s", jobID)
				}
			}
		}
	}
}

// --- Scheduler Config ---

// loadSchedConfig reads scheduler_mode from $PBS_HOME/sched_priv/sched_config.
func (s *Server) loadSchedConfig() {
	configPath := filepath.Join(s.cfg.PBSHome, "sched_priv", "sched_config")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return // Use defaults (builtin mode)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}
		// Parse "key: value" or "key value" format
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			parts = strings.Fields(line)
			if len(parts) < 2 {
				continue
			}
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		// Strip trailing time-scope specifiers like "ALL", "prime", "non_prime"
		valParts := strings.Fields(val)
		if len(valParts) > 0 {
			val = valParts[0]
		}
		switch key {
		case "scheduler_mode":
			if val == "external" || val == "builtin" {
				s.cfg.SchedulerMode = val
			}
		}
	}
}

// --- Persistence ---

// recoverState loads previously saved state from disk.
func (s *Server) recoverState() error {
	// Recover server database (job ID counter, etc.)
	s.recoverServerDB()

	// Recover queues
	s.recoverQueues()

	// Recover nodes
	s.recoverNodes()

	// Recover jobs
	s.recoverJobs()

	return nil
}

// recoverServerDB loads the server database file.
// Supports both Go simple format (key=value) and C XML format (<nextjobid>N</nextjobid>).
func (s *Server) recoverServerDB() {
	data, err := os.ReadFile(s.cfg.ServerDB)
	if err != nil {
		if s.cfg.StartType == "create" {
			log.Printf("[SERVER] Creating new server database")
		}
		return
	}

	content := string(data)

	// Detect XML format (used by both C pbs_server and Go save)
	if strings.Contains(content, "<server_db>") || strings.Contains(content, "<nextjobid>") {
		s.recoverServerDBXML(content)
		return
	}

	// Legacy simple line-based format: key=value
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if idx := strings.Index(line, "="); idx > 0 {
			key := line[:idx]
			val := line[idx+1:]
			s.applyServerDBAttr(key, val)
		}
	}
}

// recoverServerDBXML parses the XML serverdb format.
func (s *Server) recoverServerDBXML(content string) {
	// Parse all known XML tags
	xmlAttrs := map[string]string{
		"nextjobid": "", "default_queue": "", "scheduling": "",
		"scheduler_iteration": "", "node_check_rate": "", "tcp_timeout": "",
		"keep_completed": "", "job_stat_rate": "", "poll_jobs": "",
		"ping_rate": "", "job_start_timeout": "", "job_force_cancel_time": "",
		"job_sync_timeout": "", "log_events": "", "log_file_max_size": "",
		"log_file_roll_depth": "", "log_keep_days": "", "record_job_info": "",
		"record_job_script": "", "job_log_file_max_size": "",
		"job_log_file_roll_depth": "", "job_log_keep_days": "",
		"managers": "", "operators": "", "acl_host_enable": "", "acl_hosts": "",
		"acl_user_enable": "", "acl_users": "", "acl_roots": "",
		"acl_logic_or": "", "acl_group_sloppy": "",
		"acl_user_hosts": "", "acl_group_hosts": "",
		"max_running": "", "max_user_run": "", "max_group_run": "",
		"max_user_queuable": "",
		"mail_domain": "", "mail_from": "", "no_mail_force": "",
		"mail_subject_fmt": "", "mail_body_fmt": "", "email_batch_seconds": "",
		"default_node": "", "node_pack": "", "query_other_jobs": "",
		"mom_job_sync": "", "down_on_error": "", "disable_server_id_check": "",
		"allow_node_submit": "", "allow_proxy_user": "", "auto_node_np": "",
		"np_default": "", "job_nanny": "", "owner_purge": "",
		"copy_on_rerun": "", "job_exclusive_on_use": "",
		"disable_automatic_requeue": "", "automatic_requeue_exit_code": "",
		"dont_write_nodes_file": "",
		"max_job_array_size": "", "max_slot_limit": "",
		"clone_batch_size": "", "clone_batch_delay": "",
		"moab_array_compatible": "", "display_job_server_suffix": "",
		"job_suffix_alias": "", "use_jobs_subdirs": "",
		"kill_delay": "", "user_kill_delay": "", "exit_code_canceled_job": "",
		"timeout_for_job_delete": "", "timeout_for_job_requeue": "",
		"default_gpu_mode": "", "idle_slot_limit": "",
		"cgroup_per_task": "", "pass_cpu_clock": "",
		"submit_hosts": "", "node_submit_exceptions": "", "node_suffix": "",
		"comment": "", "lock_file_update_time": "", "lock_file_check_time": "",
		"interactive_jobs_can_roam": "", "legacy_vmem": "",
		"ghost_array_recovery": "", "tcp_incoming_timeout": "",
		"job_full_report_time": "",
	}

	for tag := range xmlAttrs {
		if val := extractXMLTag(content, tag); val != "" {
			s.applyServerDBAttr(tag, val)
		}
	}

	// Parse resource map tags: <resources_available.mem>4gb</resources_available.mem>
	rescMaps := map[string]*map[string]string{
		"resources_available": &s.cfg.ResourcesAvail,
		"resources_default":  &s.cfg.ResourcesDefault,
		"resources_max":      &s.cfg.ResourcesMax,
		"resources_cost":     &s.cfg.ResourcesCost,
	}
	for prefix, m := range rescMaps {
		parseResourceMapXML(content, prefix, *m)
	}
}

// parseResourceMapXML extracts resource map entries like <resources_available.mem>4gb</...>
func parseResourceMapXML(content, prefix string, m map[string]string) {
	search := "<" + prefix + "."
	pos := 0
	for {
		idx := strings.Index(content[pos:], search)
		if idx < 0 {
			break
		}
		idx += pos
		tagStart := idx + 1
		tagEnd := strings.Index(content[tagStart:], ">")
		if tagEnd < 0 {
			break
		}
		tagEnd += tagStart
		fullTag := content[tagStart:tagEnd]
		resc := strings.TrimPrefix(fullTag, prefix+".")
		closeTag := "</" + fullTag + ">"
		valStart := tagEnd + 1
		closeIdx := strings.Index(content[valStart:], closeTag)
		if closeIdx < 0 {
			break
		}
		val := strings.TrimSpace(content[valStart : valStart+closeIdx])
		m[resc] = val
		pos = valStart + closeIdx + len(closeTag)
	}
}

// applyServerDBAttr sets a server config attribute from a key-value pair.
func (s *Server) applyServerDBAttr(key, val string) {
	switch key {
	// Core
	case "nextjobid", "next_job_id":
		if n, err := strconv.Atoi(val); err == nil {
			s.jobMgr.SetNextJobIDNum(n)
			log.Printf("[SERVER] Recovered next_job_id=%d", n)
		}
	case "default_queue":
		s.cfg.DefaultQueue = val
	case "scheduling":
		s.cfg.Scheduling = parseBool(val)
	// Scheduling & timing
	case "scheduler_iteration":
		fmt.Sscanf(val, "%d", &s.cfg.SchedulerIteration)
	case "node_check_rate":
		fmt.Sscanf(val, "%d", &s.cfg.NodeCheckRate)
	case "tcp_timeout":
		fmt.Sscanf(val, "%d", &s.cfg.TCPTimeout)
	case "keep_completed":
		fmt.Sscanf(val, "%d", &s.cfg.KeepCompleted)
	case "job_stat_rate":
		fmt.Sscanf(val, "%d", &s.cfg.JobStatRate)
	case "poll_jobs":
		s.cfg.PollJobs = parseBool(val)
	case "ping_rate":
		fmt.Sscanf(val, "%d", &s.cfg.PingRate)
	case "job_start_timeout":
		fmt.Sscanf(val, "%d", &s.cfg.JobStartTimeout)
	case "job_force_cancel_time":
		fmt.Sscanf(val, "%d", &s.cfg.JobForceCancelTime)
	case "job_sync_timeout":
		fmt.Sscanf(val, "%d", &s.cfg.JobSyncTimeout)
	// Logging
	case "log_events":
		fmt.Sscanf(val, "%d", &s.cfg.LogLevel)
	case "log_file_max_size":
		fmt.Sscanf(val, "%d", &s.cfg.LogFileMaxSize)
	case "log_file_roll_depth":
		fmt.Sscanf(val, "%d", &s.cfg.LogFileRollDepth)
	case "log_keep_days":
		fmt.Sscanf(val, "%d", &s.cfg.LogKeepDays)
	case "record_job_info":
		s.cfg.RecordJobInfo = parseBool(val)
	case "record_job_script":
		s.cfg.RecordJobScript = parseBool(val)
	case "job_log_file_max_size":
		fmt.Sscanf(val, "%d", &s.cfg.JobLogFileMaxSize)
	case "job_log_file_roll_depth":
		fmt.Sscanf(val, "%d", &s.cfg.JobLogFileRollDepth)
	case "job_log_keep_days":
		fmt.Sscanf(val, "%d", &s.cfg.JobLogKeepDays)
	// ACL
	case "managers":
		s.cfg.Managers = val
	case "operators":
		s.cfg.Operators = val
	case "acl_host_enable":
		s.cfg.ACLHostEnable = parseBool(val)
	case "acl_hosts":
		s.cfg.ACLHosts = val
	case "acl_user_enable":
		s.cfg.ACLUserEnable = parseBool(val)
	case "acl_users":
		s.cfg.ACLUsers = val
	case "acl_roots":
		s.cfg.ACLRoots = val
	case "acl_logic_or":
		s.cfg.ACLLogicOr = parseBool(val)
	case "acl_group_sloppy":
		s.cfg.ACLGroupSloppy = parseBool(val)
	case "acl_user_hosts":
		s.cfg.ACLUserHosts = val
	case "acl_group_hosts":
		s.cfg.ACLGroupHosts = val
	// Resource limits
	case "max_running":
		fmt.Sscanf(val, "%d", &s.cfg.MaxRunning)
	case "max_user_run":
		fmt.Sscanf(val, "%d", &s.cfg.MaxUserRun)
	case "max_group_run":
		fmt.Sscanf(val, "%d", &s.cfg.MaxGroupRun)
	case "max_user_queuable":
		fmt.Sscanf(val, "%d", &s.cfg.MaxUserQueuable)
	// Mail
	case "mail_domain":
		s.cfg.MailDomain = val
	case "mail_from":
		s.cfg.MailFrom = val
	case "no_mail_force":
		s.cfg.NoMailForce = parseBool(val)
	case "mail_subject_fmt":
		s.cfg.MailSubjectFmt = val
	case "mail_body_fmt":
		s.cfg.MailBodyFmt = val
	case "email_batch_seconds":
		fmt.Sscanf(val, "%d", &s.cfg.EmailBatchSeconds)
	// Node & job policy
	case "default_node":
		s.cfg.DefaultNode = val
	case "node_pack":
		s.cfg.NodePack = parseBool(val)
	case "query_other_jobs":
		s.cfg.QueryOtherJobs = parseBool(val)
	case "mom_job_sync":
		s.cfg.MOMJobSync = parseBool(val)
	case "down_on_error":
		s.cfg.DownOnError = parseBool(val)
	case "disable_server_id_check":
		s.cfg.DisableServerIdCheck = parseBool(val)
	case "allow_node_submit":
		s.cfg.AllowNodeSubmit = parseBool(val)
	case "allow_proxy_user":
		s.cfg.AllowProxyUser = parseBool(val)
	case "auto_node_np":
		s.cfg.AutoNodeNP = parseBool(val)
	case "np_default":
		fmt.Sscanf(val, "%d", &s.cfg.NPDefault)
	case "job_nanny":
		s.cfg.JobNanny = parseBool(val)
	case "owner_purge":
		s.cfg.OwnerPurge = parseBool(val)
	case "copy_on_rerun":
		s.cfg.CopyOnRerun = parseBool(val)
	case "job_exclusive_on_use":
		s.cfg.JobExclusiveOnUse = parseBool(val)
	case "disable_automatic_requeue":
		s.cfg.DisableAutoRequeue = parseBool(val)
	case "automatic_requeue_exit_code":
		fmt.Sscanf(val, "%d", &s.cfg.AutoRequeueExitCode)
	case "dont_write_nodes_file":
		s.cfg.DontWriteNodesFile = parseBool(val)
	// Job array & display
	case "max_job_array_size":
		fmt.Sscanf(val, "%d", &s.cfg.MaxJobArraySize)
	case "max_slot_limit":
		fmt.Sscanf(val, "%d", &s.cfg.MaxSlotLimit)
	case "clone_batch_size":
		fmt.Sscanf(val, "%d", &s.cfg.CloneBatchSize)
	case "clone_batch_delay":
		fmt.Sscanf(val, "%d", &s.cfg.CloneBatchDelay)
	case "moab_array_compatible":
		s.cfg.MoabArrayCompatible = parseBool(val)
	case "display_job_server_suffix":
		s.cfg.DisplayJobServerSuffix = parseBool(val)
	case "job_suffix_alias":
		s.cfg.JobSuffixAlias = val
	case "use_jobs_subdirs":
		s.cfg.UseJobsSubdirs = parseBool(val)
	// Kill & cancel timeouts
	case "kill_delay":
		fmt.Sscanf(val, "%d", &s.cfg.KillDelay)
	case "user_kill_delay":
		fmt.Sscanf(val, "%d", &s.cfg.UserKillDelay)
	case "exit_code_canceled_job":
		fmt.Sscanf(val, "%d", &s.cfg.ExitCodeCanceledJob)
	case "timeout_for_job_delete":
		fmt.Sscanf(val, "%d", &s.cfg.TimeoutForJobDelete)
	case "timeout_for_job_requeue":
		fmt.Sscanf(val, "%d", &s.cfg.TimeoutForJobRequeue)
	// Hardware
	case "default_gpu_mode":
		s.cfg.DefaultGpuMode = val
	case "idle_slot_limit":
		fmt.Sscanf(val, "%d", &s.cfg.IdleSlotLimit)
	case "cgroup_per_task":
		s.cfg.CgroupPerTask = parseBool(val)
	case "pass_cpu_clock":
		s.cfg.PassCpuClock = parseBool(val)
	// Other
	case "submit_hosts":
		s.cfg.SubmitHosts = val
	case "node_submit_exceptions":
		s.cfg.NodeSubmitExceptions = val
	case "node_suffix":
		s.cfg.NodeSuffix = val
	case "comment":
		s.cfg.Comment = val
	case "lock_file_update_time":
		fmt.Sscanf(val, "%d", &s.cfg.LockFileUpdateTime)
	case "lock_file_check_time":
		fmt.Sscanf(val, "%d", &s.cfg.LockFileCheckTime)
	case "interactive_jobs_can_roam":
		s.cfg.InteractiveJobsCanRoam = parseBool(val)
	case "legacy_vmem":
		s.cfg.LegacyVmem = parseBool(val)
	case "ghost_array_recovery":
		s.cfg.GhostArrayRecovery = parseBool(val)
	case "tcp_incoming_timeout":
		fmt.Sscanf(val, "%d", &s.cfg.TCPIncomingTimeout)
	case "job_full_report_time":
		fmt.Sscanf(val, "%d", &s.cfg.JobFullReportTime)
	}
}

// extractXMLTag extracts the text content of a simple XML tag.
func extractXMLTag(content, tag string) string {
	start := "<" + tag + ">"
	end := "</" + tag + ">"
	si := strings.Index(content, start)
	if si < 0 {
		return ""
	}
	si += len(start)
	ei := strings.Index(content[si:], end)
	if ei < 0 {
		return ""
	}
	return strings.TrimSpace(content[si : si+ei])
}

// recoverQueues loads queue definitions from disk.
func (s *Server) recoverQueues() {
	entries, err := os.ReadDir(s.cfg.QueuesDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.cfg.QueuesDir, e.Name()))
		if err != nil {
			continue
		}
		q := queue.NewQueue(e.Name(), queue.TypeExecution)
		content := string(data)

		// Detect XML format (C pbs_server)
		if strings.Contains(content, "<queue>") {
			if extractXMLTag(content, "queue_type") == "Execution" {
				q.Type = queue.TypeExecution
			} else {
				q.Type = queue.TypeRoute
			}
			if extractXMLTag(content, "enabled") == "1" || extractXMLTag(content, "enabled") == "true" {
				q.Enabled = true
			}
			if extractXMLTag(content, "started") == "1" || extractXMLTag(content, "started") == "true" {
				q.Started = true
			}
		} else {
			// Simple key=value format (Go format)
			for _, line := range strings.Split(content, "\n") {
				line = strings.TrimSpace(line)
				if idx := strings.Index(line, "="); idx > 0 {
					key := line[:idx]
					val := line[idx+1:]
					switch key {
					case "queue_type":
						if val == "Route" {
							q.Type = queue.TypeRoute
						}
					case "enabled":
						q.Enabled = (val == "True")
					case "started":
						q.Started = (val == "True")
					}
				}
			}
		}
		s.queueMgr.AddQueue(q)
		log.Printf("[SERVER] Recovered queue %s", q.Name)
	}
}

// recoverNodes loads the nodes file.
func (s *Server) recoverNodes() {
	data, err := os.ReadFile(s.cfg.NodesFile)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) == 0 {
			continue
		}
		nodeName := parts[0]
		np := 1
		for _, p := range parts[1:] {
			if strings.HasPrefix(p, "np=") {
				fmt.Sscanf(p[3:], "%d", &np)
			}
		}
		s.nodeMgr.AddNode(nodeName, np)
	}
}

// recoverJobs loads saved jobs from the jobs directory.
func (s *Server) recoverJobs() {
	entries, err := os.ReadDir(s.cfg.JobsDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".JB") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.cfg.JobsDir, e.Name()))
		if err != nil {
			continue
		}
		jobID := strings.TrimSuffix(e.Name(), ".JB")
		j := job.NewJob(jobID, "", s.cfg.ServerName)

		// Parse simple key=value format
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if idx := strings.Index(line, "="); idx > 0 {
				key := line[:idx]
				val := line[idx+1:]
				switch key {
				case "state":
					fmt.Sscanf(val, "%d", &j.State)
				case "substate":
					fmt.Sscanf(val, "%d", &j.Substate)
				case "queue":
					j.Queue = val
				case "owner":
					j.Owner = val
				case "name":
					j.Name = val
				case "euser":
					j.EUser = val
				case "egroup":
					j.EGroup = val
				case "exec_host":
					j.ExecHost = val
				case "stdout":
					j.StdoutPath = val
				case "stderr":
					j.StderrPath = val
				case "exit_status":
					fmt.Sscanf(val, "%d", &j.ExitStatus)
				}
			}
		}

		// Skip completed jobs if they're old
		if j.State == job.StateComplete {
			continue
		}

		// Re-queue running jobs that weren't actually running (server restart)
		if j.State == job.StateRunning {
			j.SetState(job.StateQueued, job.SubstateQueued)
			j.ExecHost = ""
		}

		s.jobMgr.AddJob(j)
		if q := s.queueMgr.GetQueue(j.Queue); q != nil {
			q.IncrJobCount(j.State)
		}
		log.Printf("[SERVER] Recovered job %s (state=%s, queue=%s)", j.ID, j.StateName(), j.Queue)
	}
}

// saveState saves all server state to disk.
func (s *Server) saveState() {
	s.saveServerDB()
	for _, q := range s.queueMgr.AllQueues() {
		s.saveQueue(q)
	}
	s.saveNodes()
	for _, j := range s.jobMgr.AllJobs() {
		s.saveJob(j)
	}
	log.Printf("[SERVER] State saved to disk")
}

// saveServerDB writes the server database file.
func (s *Server) saveServerDB() {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var sb strings.Builder
	sb.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	sb.WriteString("<server_db>\n")

	// Helper to write a single XML element
	writeTag := func(tag, value string) {
		sb.WriteString(fmt.Sprintf("  <%s>%s</%s>\n", tag, value, tag))
	}
	writeBool := func(tag string, val bool) {
		if val {
			writeTag(tag, "True")
		} else {
			writeTag(tag, "False")
		}
	}
	writeInt := func(tag string, val int) {
		writeTag(tag, strconv.Itoa(val))
	}
	writeStr := func(tag, val string) {
		if val != "" {
			writeTag(tag, val)
		}
	}
	writeMap := func(tag string, m map[string]string) {
		for k, v := range m {
			sb.WriteString(fmt.Sprintf("  <%s.%s>%s</%s.%s>\n", tag, k, v, tag, k))
		}
	}

	// Core
	writeInt("nextjobid", s.jobMgr.GetNextJobIDNum())
	writeStr("default_queue", s.cfg.DefaultQueue)
	writeBool("scheduling", s.cfg.Scheduling)

	// Scheduling & timing
	writeInt("scheduler_iteration", s.cfg.SchedulerIteration)
	writeInt("node_check_rate", s.cfg.NodeCheckRate)
	writeInt("tcp_timeout", s.cfg.TCPTimeout)
	writeInt("keep_completed", s.cfg.KeepCompleted)
	writeInt("job_stat_rate", s.cfg.JobStatRate)
	writeBool("poll_jobs", s.cfg.PollJobs)
	writeInt("ping_rate", s.cfg.PingRate)
	writeInt("job_start_timeout", s.cfg.JobStartTimeout)
	writeInt("job_force_cancel_time", s.cfg.JobForceCancelTime)
	writeInt("job_sync_timeout", s.cfg.JobSyncTimeout)

	// Logging
	writeInt("log_events", s.cfg.LogLevel)
	writeInt("log_file_max_size", s.cfg.LogFileMaxSize)
	writeInt("log_file_roll_depth", s.cfg.LogFileRollDepth)
	writeInt("log_keep_days", s.cfg.LogKeepDays)
	writeBool("record_job_info", s.cfg.RecordJobInfo)
	writeBool("record_job_script", s.cfg.RecordJobScript)
	writeInt("job_log_file_max_size", s.cfg.JobLogFileMaxSize)
	writeInt("job_log_file_roll_depth", s.cfg.JobLogFileRollDepth)
	writeInt("job_log_keep_days", s.cfg.JobLogKeepDays)

	// ACL
	writeStr("managers", s.cfg.Managers)
	writeStr("operators", s.cfg.Operators)
	writeBool("acl_host_enable", s.cfg.ACLHostEnable)
	writeStr("acl_hosts", s.cfg.ACLHosts)
	writeBool("acl_user_enable", s.cfg.ACLUserEnable)
	writeStr("acl_users", s.cfg.ACLUsers)
	writeStr("acl_roots", s.cfg.ACLRoots)
	writeBool("acl_logic_or", s.cfg.ACLLogicOr)
	writeBool("acl_group_sloppy", s.cfg.ACLGroupSloppy)
	writeStr("acl_user_hosts", s.cfg.ACLUserHosts)
	writeStr("acl_group_hosts", s.cfg.ACLGroupHosts)

	// Resource limits
	writeInt("max_running", s.cfg.MaxRunning)
	writeInt("max_user_run", s.cfg.MaxUserRun)
	writeInt("max_group_run", s.cfg.MaxGroupRun)
	writeInt("max_user_queuable", s.cfg.MaxUserQueuable)
	writeMap("resources_available", s.cfg.ResourcesAvail)
	writeMap("resources_default", s.cfg.ResourcesDefault)
	writeMap("resources_max", s.cfg.ResourcesMax)
	writeMap("resources_cost", s.cfg.ResourcesCost)

	// Mail
	writeStr("mail_domain", s.cfg.MailDomain)
	writeStr("mail_from", s.cfg.MailFrom)
	writeBool("no_mail_force", s.cfg.NoMailForce)
	writeStr("mail_subject_fmt", s.cfg.MailSubjectFmt)
	writeStr("mail_body_fmt", s.cfg.MailBodyFmt)
	writeInt("email_batch_seconds", s.cfg.EmailBatchSeconds)

	// Node & job policy
	writeStr("default_node", s.cfg.DefaultNode)
	writeBool("node_pack", s.cfg.NodePack)
	writeBool("query_other_jobs", s.cfg.QueryOtherJobs)
	writeBool("mom_job_sync", s.cfg.MOMJobSync)
	writeBool("down_on_error", s.cfg.DownOnError)
	writeBool("disable_server_id_check", s.cfg.DisableServerIdCheck)
	writeBool("allow_node_submit", s.cfg.AllowNodeSubmit)
	writeBool("allow_proxy_user", s.cfg.AllowProxyUser)
	writeBool("auto_node_np", s.cfg.AutoNodeNP)
	writeInt("np_default", s.cfg.NPDefault)
	writeBool("job_nanny", s.cfg.JobNanny)
	writeBool("owner_purge", s.cfg.OwnerPurge)
	writeBool("copy_on_rerun", s.cfg.CopyOnRerun)
	writeBool("job_exclusive_on_use", s.cfg.JobExclusiveOnUse)
	writeBool("disable_automatic_requeue", s.cfg.DisableAutoRequeue)
	writeInt("automatic_requeue_exit_code", s.cfg.AutoRequeueExitCode)
	writeBool("dont_write_nodes_file", s.cfg.DontWriteNodesFile)

	// Job array & display
	writeInt("max_job_array_size", s.cfg.MaxJobArraySize)
	writeInt("max_slot_limit", s.cfg.MaxSlotLimit)
	writeInt("clone_batch_size", s.cfg.CloneBatchSize)
	writeInt("clone_batch_delay", s.cfg.CloneBatchDelay)
	writeBool("moab_array_compatible", s.cfg.MoabArrayCompatible)
	writeBool("display_job_server_suffix", s.cfg.DisplayJobServerSuffix)
	writeStr("job_suffix_alias", s.cfg.JobSuffixAlias)
	writeBool("use_jobs_subdirs", s.cfg.UseJobsSubdirs)

	// Kill & cancel timeouts
	writeInt("kill_delay", s.cfg.KillDelay)
	writeInt("user_kill_delay", s.cfg.UserKillDelay)
	writeInt("exit_code_canceled_job", s.cfg.ExitCodeCanceledJob)
	writeInt("timeout_for_job_delete", s.cfg.TimeoutForJobDelete)
	writeInt("timeout_for_job_requeue", s.cfg.TimeoutForJobRequeue)

	// Hardware
	writeStr("default_gpu_mode", s.cfg.DefaultGpuMode)
	writeInt("idle_slot_limit", s.cfg.IdleSlotLimit)
	writeBool("cgroup_per_task", s.cfg.CgroupPerTask)
	writeBool("pass_cpu_clock", s.cfg.PassCpuClock)

	// Other
	writeStr("submit_hosts", s.cfg.SubmitHosts)
	writeStr("node_submit_exceptions", s.cfg.NodeSubmitExceptions)
	writeStr("node_suffix", s.cfg.NodeSuffix)
	writeStr("comment", s.cfg.Comment)
	writeInt("lock_file_update_time", s.cfg.LockFileUpdateTime)
	writeInt("lock_file_check_time", s.cfg.LockFileCheckTime)
	writeBool("interactive_jobs_can_roam", s.cfg.InteractiveJobsCanRoam)
	writeBool("legacy_vmem", s.cfg.LegacyVmem)
	writeBool("ghost_array_recovery", s.cfg.GhostArrayRecovery)
	writeInt("tcp_incoming_timeout", s.cfg.TCPIncomingTimeout)
	writeInt("job_full_report_time", s.cfg.JobFullReportTime)

	sb.WriteString("</server_db>\n")

	tmpFile := s.cfg.ServerDB + ".new"
	if err := os.WriteFile(tmpFile, []byte(sb.String()), 0640); err == nil {
		os.Rename(tmpFile, s.cfg.ServerDB)
	}
}

// saveQueue writes a queue definition to disk.
func (s *Server) saveQueue(q *queue.Queue) {
	q.Mu.RLock()
	defer q.Mu.RUnlock()

	var sb strings.Builder
	if q.Type == queue.TypeExecution {
		sb.WriteString("queue_type=Execution\n")
	} else {
		sb.WriteString("queue_type=Route\n")
	}
	if q.Enabled {
		sb.WriteString("enabled=True\n")
	} else {
		sb.WriteString("enabled=False\n")
	}
	if q.Started {
		sb.WriteString("started=True\n")
	} else {
		sb.WriteString("started=False\n")
	}

	path := filepath.Join(s.cfg.QueuesDir, q.Name)
	tmpFile := path + ".new"
	if err := os.WriteFile(tmpFile, []byte(sb.String()), 0640); err == nil {
		os.Rename(tmpFile, path)
	}
}

// saveNodes writes the nodes inventory file.
func (s *Server) saveNodes() {
	var sb strings.Builder
	for _, n := range s.nodeMgr.AllNodes() {
		n.Mu.RLock()
		sb.WriteString(fmt.Sprintf("%s np=%d\n", n.Name, n.NumProcs))
		n.Mu.RUnlock()
	}
	tmpFile := s.cfg.NodesFile + ".new"
	if err := os.WriteFile(tmpFile, []byte(sb.String()), 0640); err == nil {
		os.Rename(tmpFile, s.cfg.NodesFile)
	}
}

// saveJob writes a job's state to disk.
func (s *Server) saveJob(j *job.Job) {
	j.Mu.RLock()
	defer j.Mu.RUnlock()

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("state=%d\n", j.State))
	sb.WriteString(fmt.Sprintf("substate=%d\n", j.Substate))
	sb.WriteString(fmt.Sprintf("queue=%s\n", j.Queue))
	sb.WriteString(fmt.Sprintf("owner=%s\n", j.Owner))
	sb.WriteString(fmt.Sprintf("name=%s\n", j.Name))
	sb.WriteString(fmt.Sprintf("euser=%s\n", j.EUser))
	sb.WriteString(fmt.Sprintf("egroup=%s\n", j.EGroup))
	sb.WriteString(fmt.Sprintf("exec_host=%s\n", j.ExecHost))
	sb.WriteString(fmt.Sprintf("stdout=%s\n", j.StdoutPath))
	sb.WriteString(fmt.Sprintf("stderr=%s\n", j.StderrPath))
	sb.WriteString(fmt.Sprintf("exit_status=%d\n", j.ExitStatus))

	path := filepath.Join(s.cfg.JobsDir, j.ID+".JB")
	tmpFile := path + ".new"
	if err := os.WriteFile(tmpFile, []byte(sb.String()), 0640); err == nil {
		os.Rename(tmpFile, path)
	}
}

// removeJobFiles cleans up all files for a given job.
func (s *Server) removeJobFiles(jobID string) {
	os.Remove(filepath.Join(s.cfg.JobsDir, jobID+".JB"))
	os.Remove(filepath.Join(s.cfg.JobsDir, jobID+".SC"))
}

// --- Privileged Port Helper ---

// dialPrivileged connects to addr from a privileged local port (< 1024).
// Required for PBS authentication: MOM trusts connections from privileged ports.
func dialPrivileged(addr string) (net.Conn, error) {
	for port := 1023; port >= 600; port-- {
		localAddr := &net.TCPAddr{Port: port}
		d := net.Dialer{
			LocalAddr: localAddr,
			Timeout:   10 * time.Second,
		}
		conn, err := d.Dial("tcp", addr)
		if err == nil {
			return conn, nil
		}
		if !strings.Contains(err.Error(), "address already in use") {
			return nil, err
		}
	}
	// Fall back to any port
	return net.DialTimeout("tcp", addr, 10*time.Second)
}

// --- Helpers ---

// readNodeFile reads the nodes file and returns a list of "hostname np=N" lines.
func readNodeFile(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			lines = append(lines, line)
		}
	}
	return lines, scanner.Err()
}
