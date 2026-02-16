package mom

import (
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/xinlaoda/opentorque/internal/mom/config"
	"github.com/xinlaoda/opentorque/internal/mom/dis"
	"github.com/xinlaoda/opentorque/internal/mom/job"
	"github.com/xinlaoda/opentorque/internal/mom/resource"
	"github.com/xinlaoda/opentorque/internal/mom/server"
)

// Daemon is the main pbs_mom daemon.
type Daemon struct {
	cfg       *config.Config
	jobMgr    *job.Manager
	executor  *job.Executor
	monitor   resource.Monitor
	servers   []*server.Connection
	listener  net.Listener
	rmListener net.Listener

	shutdown  chan struct{}
	wg        sync.WaitGroup

	mu        sync.RWMutex
	running   bool
}

// New creates a new MOM daemon.
func New(cfg *config.Config) *Daemon {
	mon := resource.NewMonitor()
	executor := job.NewExecutor(cfg)
	jobMgr := job.NewManager()

	d := &Daemon{
		cfg:      cfg,
		jobMgr:   jobMgr,
		executor: executor,
		monitor:  mon,
		shutdown: make(chan struct{}),
	}

	// Set up server connections
	for _, host := range cfg.PBSServers {
		conn := server.NewConnection(cfg, mon, host)
		d.servers = append(d.servers, conn)
	}

	// Register job completion callback
	jobMgr.SetOnJobComplete(d.onJobComplete)

	return d
}

// Run starts the daemon.
func (d *Daemon) Run() error {
	log.Printf("[MOM] Starting pbs_mom (Go version)")
	log.Printf("[MOM] PBS Home: %s", d.cfg.PBSHome)
	log.Printf("[MOM] Servers: %v", d.cfg.PBSServers)
	log.Printf("[MOM] MOM port: %d, RM port: %d", d.cfg.MomPort, d.cfg.RMPort)
	d.logNonDefaultConfig()

	// Ensure directories exist
	if err := d.cfg.EnsureDirs(); err != nil {
		return fmt.Errorf("ensure dirs: %w", err)
	}

	// Write PID file
	if err := d.writePIDFile(); err != nil {
		log.Printf("[MOM] Warning: could not write PID file: %v", err)
	}

	// Start listening for requests from server
	if err := d.startListener(); err != nil {
		return fmt.Errorf("start listener: %w", err)
	}

	// Start RM listener
	if err := d.startRMListener(); err != nil {
		log.Printf("[MOM] Warning: could not start RM listener: %v", err)
	}

	// Connect to servers
	d.connectToServers()

	// Set up signal handling
	d.setupSignals()

	d.mu.Lock()
	d.running = true
	d.mu.Unlock()

	log.Printf("[MOM] pbs_mom is ready")

	// Main loop
	d.mainLoop()

	// Cleanup
	d.cleanup()
	return nil
}

// Stop signals the daemon to shut down.
func (d *Daemon) Stop() {
	log.Printf("[MOM] Shutdown requested")
	close(d.shutdown)
}

func (d *Daemon) mainLoop() {
	statusTicker := time.NewTicker(time.Duration(d.cfg.StatusUpdateTime) * time.Second)
	defer statusTicker.Stop()

	pollTicker := time.NewTicker(time.Duration(d.cfg.CheckPollTime) * time.Second)
	defer pollTicker.Stop()

	reconnectTicker := time.NewTicker(30 * time.Second)
	defer reconnectTicker.Stop()

	for {
		select {
		case <-d.shutdown:
			log.Printf("[MOM] Shutting down main loop")
			return

		case <-statusTicker.C:
			d.sendStatusUpdates()

		case <-pollTicker.C:
			d.pollJobs()

		case <-reconnectTicker.C:
			d.connectToServers()
		}
	}
}

func (d *Daemon) startListener() error {
	addr := fmt.Sprintf(":%d", d.cfg.MomPort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}
	d.listener = ln
	log.Printf("[MOM] Listening on %s (MOM service port)", addr)

	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		d.acceptLoop(ln)
	}()

	return nil
}

func (d *Daemon) startRMListener() error {
	addr := fmt.Sprintf(":%d", d.cfg.RMPort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}
	d.rmListener = ln
	log.Printf("[MOM] Listening on %s (Resource Manager port)", addr)

	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		d.acceptLoop(ln)
	}()

	return nil
}

func (d *Daemon) acceptLoop(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-d.shutdown:
				return
			default:
				log.Printf("[MOM] Accept error: %v", err)
				continue
			}
		}

		go d.handleConnection(conn)
	}
}

// handleConnection handles a TCP connection that may carry MULTIPLE requests.
// The server sends all requests for a job (QueueJob, JobCred, JobScript,
// RdytoCommit, Commit) over the SAME TCP connection.
func (d *Daemon) handleConnection(conn net.Conn) {
	defer conn.Close()

	reader := dis.NewReader(conn)

	for {
		// Set a generous read deadline for each request
		conn.SetDeadline(time.Now().Add(60 * time.Second))

		header, err := dis.ReadRequestHeader(reader)
		if err != nil {
			if err == io.EOF || strings.Contains(err.Error(), "EOF") ||
				strings.Contains(err.Error(), "connection reset") ||
				strings.Contains(err.Error(), "i/o timeout") {
				return // Client disconnected or timeout, normal
			}
			log.Printf("[MOM] Error reading request from %s: %v", conn.RemoteAddr(), err)
			return
		}

		log.Printf("[MOM] Request from %s: type=%d (%s) user=%s",
			conn.RemoteAddr(), header.ReqType, dis.BatchRequestName(header.ReqType), header.User)

		if header.Protocol != dis.BatchProtType {
			if header.Protocol == dis.IMProtocol || header.Protocol == 4 {
				// IS protocol message from server - just consume and close
				return
			}
			log.Printf("[MOM] Unknown protocol type %d", header.Protocol)
			dis.SendErrorReply(conn, dis.PbsErrProtocol, 0)
			return
		}

		// Dispatch the request - returns false if connection should close
		if !d.dispatchRequest(conn, reader, header) {
			return
		}
	}
}

// dispatchRequest handles a single request. Returns true to continue reading, false to close.
func (d *Daemon) dispatchRequest(conn net.Conn, reader *dis.Reader, header *dis.RequestHeader) bool {
	var bodyOk bool

	switch header.ReqType {
	case dis.BatchQueueJob, dis.BatchQueueJob2:
		bodyOk = d.handleQueueJob(conn, reader, header)

	case dis.BatchJobCred:
		bodyOk = d.handleJobCred(conn, reader, header)

	case dis.BatchJobScript, dis.BatchJobScript2:
		bodyOk = d.handleJobScript(conn, reader, header)

	case dis.BatchRdytoCommit:
		bodyOk = d.handleReadyToCommit(conn, reader, header)

	case dis.BatchCommit, dis.BatchCommit2:
		bodyOk = d.handleCommit(conn, reader, header)

	case dis.BatchDeleteJob:
		bodyOk = d.handleDeleteJob(conn, reader, header)

	case dis.BatchSignalJob, dis.BatchAsySignalJob:
		bodyOk = d.handleSignalJob(conn, reader, header)

	case dis.BatchStatusJob:
		bodyOk = d.handleStatusJob(conn, reader, header)

	case dis.BatchHoldJob:
		bodyOk = d.handleHoldJob(conn, reader, header)

	case dis.BatchModifyJob:
		bodyOk = d.handleModifyJob(conn, reader, header)

	case dis.BatchShutdown:
		bodyOk = d.handleShutdown(conn, reader, header)
		return false // close after shutdown

	case dis.BatchCopyFiles:
		bodyOk = d.handleCopyFiles(conn, reader, header)

	case dis.BatchDelFiles:
		bodyOk = d.handleDelFiles(conn, reader, header)

	case dis.BatchReturnFiles:
		bodyOk = d.handleReturnFiles(conn, reader, header)

	case dis.BatchDisconnect:
		return false // close connection

	default:
		log.Printf("[MOM] Unhandled request type %d (%s)", header.ReqType, dis.BatchRequestName(header.ReqType))
		dis.SendErrorReply(conn, dis.PbsErrUnknownReq, 0)
		bodyOk = false
	}

	// Read and discard the request extension field
	// Every server request ends with: diswui(has_extend) [diswst(extend_string)]
	if bodyOk {
		if err := readReqExtend(reader); err != nil {
			log.Printf("[MOM] Error reading request extension: %v", err)
			return false
		}
	}

	return true // keep connection open
}

// connectToServers attempts to connect to all configured servers.
func (d *Daemon) connectToServers() {
	for _, srv := range d.servers {
		if !srv.IsConnected() {
			if err := srv.Connect(); err != nil {
				log.Printf("[MOM] Cannot connect to server: %v", err)
			}
		}
	}
}

// sendStatusUpdates sends status to all connected servers.
func (d *Daemon) sendStatusUpdates() {
	log.Printf("[MOM] Sending IS heartbeat/status update to %d servers", len(d.servers))
	for _, srv := range d.servers {
		if srv.IsConnected() {
			if err := srv.SendStatusUpdate(); err != nil {
				log.Printf("[MOM] Status update failed: %v", err)
			}
		}
	}
}

// pollJobs checks resource usage of running jobs.
func (d *Daemon) pollJobs() {
	for _, j := range d.jobMgr.RunningJobs() {
		j.Mu.Lock()
		if len(j.Tasks) > 0 && j.Tasks[0].State == job.TaskExited {
			j.Mu.Unlock()
			d.processExitedJob(j)
			continue
		}

		// Update resource usage
		for _, task := range j.Tasks {
			if task.State == job.TaskRunning {
				res, err := d.monitor.GetProcessResources(task.Sid)
				if err == nil {
					j.ResourceUse.CPUTime = res.CPUTime
					if res.Memory > j.ResourceUse.MaxMemory {
						j.ResourceUse.MaxMemory = res.Memory
					}
					j.ResourceUse.Memory = res.Memory
					if res.VMem > j.ResourceUse.MaxVMem {
						j.ResourceUse.MaxVMem = res.VMem
					}
					j.ResourceUse.VMem = res.VMem
				}
			}
		}

		if !j.StartTime.IsZero() {
			j.ResourceUse.WallTime = time.Since(j.StartTime)
		}
		j.Mu.Unlock()
	}
}

// processExitedJob handles a job that has finished running.
func (d *Daemon) processExitedJob(j *job.Job) {
	log.Printf("[MOM] Processing exited job %s", j.ID)

	// Run epilogue
	d.executor.RunEpilog(j, d.cfg.PathEpilog, d.cfg.PathEpilogUser, d.cfg.PrologAlarm)

	// Perform stageout file transfers after epilogue
	if j.StageoutList != "" {
		if err := performStaging(j.StageoutList, "stageout", j.ID); err != nil {
			log.Printf("[MOM] Stageout failed for %s: %v", j.ID, err)
		}
	}

	// Send obit
	d.sendJobObit(j)

	// Deliver output files to final destination
	d.executor.DeliverOutput(j)

	// Mark complete
	d.jobMgr.MarkJobComplete(j.ID)

	// Cleanup
	d.executor.CleanupJob(j)
	d.jobMgr.RemoveJob(j.ID)
}

// sendJobObit sends the job obituary to the server.
func (d *Daemon) sendJobObit(j *job.Job) {
	resources := j.FormatResourceUsed()

	for _, srv := range d.servers {
		if srv.IsConnected() {
			if err := srv.SendJobObit(j.ID, j.ExitStatus, resources); err != nil {
				log.Printf("[MOM] Failed to send obit for %s: %v", j.ID, err)
			}
		}
	}
}

// onJobComplete is called when a job finishes.
func (d *Daemon) onJobComplete(j *job.Job) {
	log.Printf("[MOM] Job %s completed (exit=%d)", j.ID, j.ExitStatus)
}

func (d *Daemon) setupSignals() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)

	go func() {
		for sig := range sigCh {
			switch sig {
			case syscall.SIGTERM, syscall.SIGINT:
				log.Printf("[MOM] Received signal %v, shutting down", sig)
				d.Stop()
				return
			case syscall.SIGHUP:
				log.Printf("[MOM] Received SIGHUP, reloading config")
				d.reloadConfig()
			}
		}
	}()
}

func (d *Daemon) reloadConfig() {
	configFile := d.cfg.ConfigFile()
	newCfg := config.NewDefaultConfig()
	if err := newCfg.Load(configFile); err != nil {
		log.Printf("[MOM] Failed to reload config: %v", err)
		return
	}
	d.cfg.CheckPollTime = newCfg.CheckPollTime
	d.cfg.StatusUpdateTime = newCfg.StatusUpdateTime
	d.cfg.LogEvents = newCfg.LogEvents
	log.Printf("[MOM] Config reloaded from %s", configFile)
}

func (d *Daemon) cleanup() {
	log.Printf("[MOM] Cleaning up...")

	if d.listener != nil {
		d.listener.Close()
	}
	if d.rmListener != nil {
		d.rmListener.Close()
	}

	for _, srv := range d.servers {
		srv.Close()
	}

	os.Remove(d.pidFilePath())

	d.wg.Wait()
	log.Printf("[MOM] Shutdown complete")
}

func (d *Daemon) writePIDFile() error {
	pidFile := d.pidFilePath()
	return os.WriteFile(pidFile, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0644)
}

func (d *Daemon) pidFilePath() string {
	return d.cfg.PathMomPriv + "/mom.lock"
}

// logNonDefaultConfig logs configuration values that differ from defaults.
func (d *Daemon) logNonDefaultConfig() {
	c := d.cfg
	if c.MomHost != "" {
		log.Printf("[MOM] Config: mom_host=%s", c.MomHost)
	}
	if c.LogEvents != config.DefaultLogEvents {
		log.Printf("[MOM] Config: logevent=0x%x", c.LogEvents)
	}
	if c.CheckPollTime != config.DefaultCheckPoll {
		log.Printf("[MOM] Config: check_poll_time=%d", c.CheckPollTime)
	}
	if c.StatusUpdateTime != config.DefaultStatusUpdate {
		log.Printf("[MOM] Config: status_update_time=%d", c.StatusUpdateTime)
	}
	if c.PrologAlarm != config.DefaultPrologAlarm {
		log.Printf("[MOM] Config: prologalarm=%d", c.PrologAlarm)
	}
	if c.EnableMomRestart {
		log.Printf("[MOM] Config: enablemomrestart=true")
	}
	if c.DownOnError {
		log.Printf("[MOM] Config: down_on_error=true")
	}
	if len(c.UseCp) > 0 {
		log.Printf("[MOM] Config: usecp entries=%d", len(c.UseCp))
	}
	if c.Timeout != 300 {
		log.Printf("[MOM] Config: timeout=%d", c.Timeout)
	}
	if len(c.Restricted) > 0 {
		log.Printf("[MOM] Config: restricted=%v", c.Restricted)
	}
	if len(c.PBSClient) > 0 {
		log.Printf("[MOM] Config: pbsclient=%v", c.PBSClient)
	}
	if c.IdealLoad >= 0 {
		log.Printf("[MOM] Config: ideal_load=%.2f", c.IdealLoad)
	}
	if c.MaxLoad >= 0 {
		log.Printf("[MOM] Config: max_load=%.2f", c.MaxLoad)
	}
	if c.TmpDir != "" {
		log.Printf("[MOM] Config: tmpdir=%s", c.TmpDir)
	}
	if c.LogDirectory != "" {
		log.Printf("[MOM] Config: log_directory=%s", c.LogDirectory)
	}
	if c.LogLevel != 0 {
		log.Printf("[MOM] Config: loglevel=%d", c.LogLevel)
	}
	if c.NodeCheckScript != "" {
		log.Printf("[MOM] Config: node_check_script=%s", c.NodeCheckScript)
	}
	if c.JobStarter != "" {
		log.Printf("[MOM] Config: job_starter=%s", c.JobStarter)
	}
	if !c.SourceLoginBatch {
		log.Printf("[MOM] Config: source_login_batch=false")
	}
	if !c.SourceLoginInteractive {
		log.Printf("[MOM] Config: source_login_interactive=false")
	}
	if c.JobOutputFileUmask != 0077 {
		log.Printf("[MOM] Config: job_output_file_umask=0%o", c.JobOutputFileUmask)
	}
}

// --- Request Handlers ---

// handleQueueJob decodes: disrst(job_id) disrst(destination) decode_DIS_svrattrl(attrs)
func (d *Daemon) handleQueueJob(conn net.Conn, reader *dis.Reader, header *dis.RequestHeader) bool {
	// Read job ID
	jobID, err := reader.ReadString()
	if err != nil {
		log.Printf("[MOM] Error reading job ID: %v", err)
		dis.SendErrorReply(conn, dis.PbsErrProtocol, 0)
		return false
	}

	// Read destination
	dest, err := reader.ReadString()
	if err != nil {
		log.Printf("[MOM] Error reading destination: %v", err)
		dis.SendErrorReply(conn, dis.PbsErrProtocol, 0)
		return false
	}

	// Read and parse attributes
	attrs, err := readSvrAttrl(reader)
	if err != nil {
		log.Printf("[MOM] Error reading attributes for %s: %v", jobID, err)
		dis.SendErrorReply(conn, dis.PbsErrProtocol, 0)
		return false
	}

	log.Printf("[MOM] QueueJob: id=%s dest=%s attrs=%d", jobID, dest, len(attrs))

	// Create new job with attributes
	j := job.NewJob(jobID)
	j.Owner = header.User
	j.Queue = dest

	// Apply attributes from the server
	for _, attr := range attrs {
		applyJobAttribute(j, attr)
	}

	if err := d.jobMgr.AddJob(j); err != nil {
		dis.SendErrorReply(conn, dis.PbsErrJobExist, 0)
		return false
	}

	// Reply with job ID (BATCH_REPLY_CHOICE_Queue=2)
	dis.SendJobIDReply(conn, dis.ReplyChoiceQueue, jobID)
	return true
}

// handleJobCred decodes: disrui(cred_type) disrcs(cred_data)
func (d *Daemon) handleJobCred(conn net.Conn, reader *dis.Reader, header *dis.RequestHeader) bool {
	// Read credential type
	_, err := reader.ReadUint()
	if err != nil {
		log.Printf("[MOM] Error reading cred type: %v", err)
		dis.SendErrorReply(conn, dis.PbsErrProtocol, 0)
		return false
	}

	// Read credential data (counted string)
	_, err = reader.ReadString()
	if err != nil {
		log.Printf("[MOM] Error reading cred data: %v", err)
		dis.SendErrorReply(conn, dis.PbsErrProtocol, 0)
		return false
	}

	log.Printf("[MOM] JobCred received")
	dis.SendOkReply(conn)
	return true
}

// handleJobScript decodes: disrui(seq) disrui(type) disrui(size) disrst(jobid) disrcs(data)
func (d *Daemon) handleJobScript(conn net.Conn, reader *dis.Reader, header *dis.RequestHeader) bool {
	// Read sequence number
	_, err := reader.ReadUint()
	if err != nil {
		log.Printf("[MOM] Error reading script seq: %v", err)
		dis.SendErrorReply(conn, dis.PbsErrProtocol, 0)
		return false
	}

	// Read file type
	_, err = reader.ReadUint()
	if err != nil {
		log.Printf("[MOM] Error reading script type: %v", err)
		dis.SendErrorReply(conn, dis.PbsErrProtocol, 0)
		return false
	}

	// Read data size
	dataSize, err := reader.ReadUint()
	if err != nil {
		log.Printf("[MOM] Error reading script size: %v", err)
		dis.SendErrorReply(conn, dis.PbsErrProtocol, 0)
		return false
	}

	// Read job ID
	jobID, err := reader.ReadString()
	if err != nil {
		log.Printf("[MOM] Error reading script jobid: %v", err)
		dis.SendErrorReply(conn, dis.PbsErrProtocol, 0)
		return false
	}

	// Read script data (counted string)
	script, err := reader.ReadString()
	if err != nil {
		log.Printf("[MOM] Error reading script data: %v", err)
		dis.SendErrorReply(conn, dis.PbsErrProtocol, 0)
		return false
	}

	log.Printf("[MOM] JobScript for %s (%d bytes, declared %d)", jobID, len(script), dataSize)

	if j, ok := d.jobMgr.GetJob(jobID); ok {
		j.Mu.Lock()
		j.Script = script
		j.Mu.Unlock()
	}

	dis.SendOkReply(conn)
	return true
}

// handleReadyToCommit decodes: disrst(job_id)
func (d *Daemon) handleReadyToCommit(conn net.Conn, reader *dis.Reader, header *dis.RequestHeader) bool {
	jobID, err := reader.ReadString()
	if err != nil {
		log.Printf("[MOM] Error reading RdytoCommit jobid: %v", err)
		dis.SendErrorReply(conn, dis.PbsErrProtocol, 0)
		return false
	}

	log.Printf("[MOM] ReadyToCommit for %s", jobID)

	if _, ok := d.jobMgr.GetJob(jobID); !ok {
		dis.SendErrorReply(conn, dis.PbsErrUnkjobid, 0)
		return false
	}

	// Reply with job ID (BATCH_REPLY_CHOICE_RdytoCom=3)
	dis.SendJobIDReply(conn, dis.ReplyChoiceRdytoCom, jobID)
	return true
}

// handleCommit decodes: disrst(job_id)
func (d *Daemon) handleCommit(conn net.Conn, reader *dis.Reader, header *dis.RequestHeader) bool {
	jobID, err := reader.ReadString()
	if err != nil {
		log.Printf("[MOM] Error reading Commit jobid: %v", err)
		dis.SendErrorReply(conn, dis.PbsErrProtocol, 0)
		return false
	}

	log.Printf("[MOM] Commit (start) job %s", jobID)

	j, ok := d.jobMgr.GetJob(jobID)
	if !ok {
		dis.SendErrorReply(conn, dis.PbsErrUnkjobid, 0)
		return false
	}

	// Set execution host
	hostname, _ := os.Hostname()
	j.Mu.Lock()
	j.ExecHost = strings.Split(hostname, ".")[0] + "/0"
	if j.User == "" {
		j.User = header.User
	}
	if j.User == "" {
		j.User = j.Owner
	}
	j.Mu.Unlock()

	// Create node file
	d.executor.CreateNodeFile(j)

	// Perform stagein file transfers before prologue
	if j.StageinList != "" {
		if err := performStaging(j.StageinList, "stagein", j.ID); err != nil {
			log.Printf("[MOM] Stagein failed for %s: %v", jobID, err)
			dis.SendErrorReply(conn, dis.PbsErrSystem, 0)
			return false
		}
	}

	// Run prologue
	if err := d.executor.RunProlog(j, d.cfg.PathProlog, d.cfg.PathPrologUser, d.cfg.PrologAlarm); err != nil {
		log.Printf("[MOM] Prologue failed for %s: %v", jobID, err)
		dis.SendErrorReply(conn, dis.PbsErrSystem, 0)
		return false
	}

	// Start job execution
	if err := d.executor.StartJob(j); err != nil {
		log.Printf("[MOM] Failed to start job %s: %v", jobID, err)
		dis.SendErrorReply(conn, dis.PbsErrSystem, 0)
		return false
	}

	// Reply with session ID as text (BATCH_REPLY_CHOICE_Text=7)
	j.Mu.RLock()
	sid := j.SessionID
	j.Mu.RUnlock()
	sidStr := strconv.Itoa(sid)
	log.Printf("[MOM] Job %s started, pid/sid=%d", jobID, sid)
	dis.SendTextReply(conn, sidStr)
	return true
}

func (d *Daemon) handleDeleteJob(conn net.Conn, reader *dis.Reader, header *dis.RequestHeader) bool {
	// DeleteJob uses decode_DIS_Manage format: string jobid + attribute list
	jobID, err := reader.ReadString()
	if err != nil {
		log.Printf("[MOM] Error reading DeleteJob: %v", err)
		dis.SendErrorReply(conn, dis.PbsErrProtocol, 0)
		return false
	}

	// Skip the attribute list that follows in Manage requests
	dis.SkipSvrAttrl(reader)

	log.Printf("[MOM] DeleteJob %s", jobID)

	j, ok := d.jobMgr.GetJob(jobID)
	if !ok {
		dis.SendErrorReply(conn, dis.PbsErrUnkjobid, 0)
		return false
	}

	if j.IsRunning() {
		d.executor.KillJob(j, syscall.SIGTERM)
		time.AfterFunc(5*time.Second, func() {
			if j.IsRunning() {
				d.executor.KillJob(j, syscall.SIGKILL)
			}
		})
	}

	d.jobMgr.MarkJobExiting(jobID, -1)
	dis.SendOkReply(conn)
	return true
}

func (d *Daemon) handleSignalJob(conn net.Conn, reader *dis.Reader, header *dis.RequestHeader) bool {
	jobID, err := reader.ReadString()
	if err != nil {
		dis.SendErrorReply(conn, dis.PbsErrProtocol, 0)
		return false
	}
	sigStr, _ := reader.ReadString()

	log.Printf("[MOM] SignalJob %s signal=%s", jobID, sigStr)

	j, ok := d.jobMgr.GetJob(jobID)
	if !ok {
		dis.SendErrorReply(conn, dis.PbsErrUnkjobid, 0)
		return false
	}

	sig := parseSignal(sigStr)
	if j.IsRunning() {
		d.executor.KillJob(j, sig)
	}

	dis.SendOkReply(conn)
	return true
}

func (d *Daemon) handleStatusJob(conn net.Conn, reader *dis.Reader, header *dis.RequestHeader) bool {
	jobID, _ := reader.ReadString()
	log.Printf("[MOM] StatusJob %s", jobID)
	dis.SendOkReply(conn)
	return true
}

func (d *Daemon) handleHoldJob(conn net.Conn, reader *dis.Reader, header *dis.RequestHeader) bool {
	jobID, _ := reader.ReadString()
	log.Printf("[MOM] HoldJob %s", jobID)

	j, ok := d.jobMgr.GetJob(jobID)
	if !ok {
		dis.SendErrorReply(conn, dis.PbsErrUnkjobid, 0)
		return false
	}

	j.SetState(job.StateHeld, job.SubstateHeld)
	dis.SendOkReply(conn)
	return true
}

func (d *Daemon) handleModifyJob(conn net.Conn, reader *dis.Reader, header *dis.RequestHeader) bool {
	jobID, _ := reader.ReadString()
	// Skip attribute list
	dis.SkipSvrAttrl(reader)
	log.Printf("[MOM] ModifyJob %s", jobID)
	dis.SendOkReply(conn)
	return true
}

func (d *Daemon) handleShutdown(conn net.Conn, reader *dis.Reader, header *dis.RequestHeader) bool {
	log.Printf("[MOM] Shutdown request received")
	dis.SendOkReply(conn)
	d.Stop()
	return true
}

func (d *Daemon) handleCopyFiles(conn net.Conn, reader *dis.Reader, header *dis.RequestHeader) bool {
	log.Printf("[MOM] CopyFiles received")
	dis.SendOkReply(conn)
	return true
}

func (d *Daemon) handleDelFiles(conn net.Conn, reader *dis.Reader, header *dis.RequestHeader) bool {
	log.Printf("[MOM] DelFiles received")
	dis.SendOkReply(conn)
	return true
}

func (d *Daemon) handleReturnFiles(conn net.Conn, reader *dis.Reader, header *dis.RequestHeader) bool {
	log.Printf("[MOM] ReturnFiles received")
	dis.SendOkReply(conn)
	return true
}

func parseSignal(s string) syscall.Signal {
	s = strings.ToUpper(strings.TrimPrefix(s, "SIG"))
	signals := map[string]syscall.Signal{
		"TERM": syscall.SIGTERM,
		"KILL": syscall.SIGKILL,
		"INT":  syscall.SIGINT,
		"HUP":  syscall.SIGHUP,
		"USR1": syscall.SIGUSR1,
		"USR2": syscall.SIGUSR2,
		"CONT": syscall.SIGCONT,
		"STOP": syscall.SIGSTOP,
	}
	if sig, ok := signals[s]; ok {
		return sig
	}
	return syscall.SIGTERM
}

// --- Attribute parsing ---

type svrAttr struct {
	name     string
	resource string
	value    string
}

// readSvrAttrl reads a DIS-encoded attribute list.
// Format: count(uint), then per attribute: size(uint) name(str) hasResc(uint) [resc(str)] value(str) op(uint)
func readSvrAttrl(r *dis.Reader) ([]svrAttr, error) {
	count, err := r.ReadUint()
	if err != nil {
		return nil, err
	}

	attrs := make([]svrAttr, 0, count)
	for i := uint64(0); i < count; i++ {
		// size
		if _, err := r.ReadUint(); err != nil {
			return nil, err
		}
		// name
		name, err := r.ReadString()
		if err != nil {
			return nil, err
		}
		// has resource?
		hasResc, err := r.ReadUint()
		if err != nil {
			return nil, err
		}
		var resc string
		if hasResc != 0 {
			resc, err = r.ReadString()
			if err != nil {
				return nil, err
			}
		}
		// value
		value, err := r.ReadString()
		if err != nil {
			return nil, err
		}
		// op
		if _, err := r.ReadUint(); err != nil {
			return nil, err
		}

		attrs = append(attrs, svrAttr{name: name, resource: resc, value: value})
	}
	return attrs, nil
}

// applyJobAttribute applies a parsed attribute to a job.
func applyJobAttribute(j *job.Job, attr svrAttr) {
	switch attr.name {
	case "Job_Name":
		j.Name = attr.value
	case "Job_Owner":
		j.Owner = attr.value
		// Extract username from user@host
		if idx := strings.Index(attr.value, "@"); idx >= 0 {
			j.User = attr.value[:idx]
		} else {
			j.User = attr.value
		}
	case "euser":
		j.User = attr.value
	case "egroup":
		j.Group = attr.value
	case "queue":
		j.Queue = attr.value
	case "Shell_Path_List":
		j.Shell = attr.value
	case "Output_Path":
		j.StdoutPath = attr.value
	case "Error_Path":
		j.StderrPath = attr.value
	case "Variable_List":
		// Parse comma-separated KEY=VALUE pairs
		for _, kv := range strings.Split(attr.value, ",") {
			if idx := strings.Index(kv, "="); idx >= 0 {
				j.VariableList[kv[:idx]] = kv[idx+1:]
			}
		}
	case "Join_Path":
		j.JoinPath = attr.value
	case "Rerunable":
		j.Rerunnable = attr.value == "True"
	case "Resource_List":
		switch attr.resource {
		case "walltime":
			j.ResourceReq.WallTime = parseDuration(attr.value)
		case "cput":
			j.ResourceReq.CPUTime = parseDuration(attr.value)
		case "mem":
			j.ResourceReq.Memory = parseSize(attr.value)
		case "vmem":
			j.ResourceReq.VMem = parseSize(attr.value)
		case "ncpus":
			if n, err := strconv.Atoi(attr.value); err == nil {
				j.ResourceReq.Ncpus = n
			}
		case "nodes":
			if n, err := strconv.Atoi(attr.value); err == nil {
				j.ResourceReq.Nodes = n
			}
		}
	case "stagein":
		j.StageinList = attr.value
	case "stageout":
		j.StageoutList = attr.value
	}
}

func parseDuration(s string) time.Duration {
	parts := strings.Split(s, ":")
	if len(parts) == 3 {
		h, _ := strconv.Atoi(parts[0])
		m, _ := strconv.Atoi(parts[1])
		sec, _ := strconv.Atoi(parts[2])
		return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(sec)*time.Second
	}
	d, _ := time.ParseDuration(s)
	return d
}

func parseSize(s string) int64 {
	s = strings.TrimSpace(s)
	multiplier := int64(1)
	if strings.HasSuffix(s, "kb") {
		multiplier = 1024
		s = strings.TrimSuffix(s, "kb")
	} else if strings.HasSuffix(s, "mb") {
		multiplier = 1024 * 1024
		s = strings.TrimSuffix(s, "mb")
	} else if strings.HasSuffix(s, "gb") {
		multiplier = 1024 * 1024 * 1024
		s = strings.TrimSuffix(s, "gb")
	} else if strings.HasSuffix(s, "b") {
		s = strings.TrimSuffix(s, "b")
	}
	val, _ := strconv.ParseInt(s, 10, 64)
	return val * multiplier
}

// readReqExtend reads and discards the request extension field.
// Format: diswui(has_extend) [if non-zero: diswst(extend_string)]
func readReqExtend(r *dis.Reader) error {
hasExtend, err := r.ReadUint()
if err != nil {
return fmt.Errorf("read extend flag: %w", err)
}
if hasExtend != 0 {
_, err = r.ReadString()
if err != nil {
return fmt.Errorf("read extend string: %w", err)
}
}
return nil
}

// performStaging executes file staging operations (stagein or stageout).
// Format: "local_file@remote_host:remote_path[,local2@host2:path2]"
// Uses scp for remote transfers and cp for local transfers.
func performStaging(spec, direction, jobID string) error {
	for _, item := range strings.Split(spec, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		// Parse "local@host:remote" format
		atIdx := strings.Index(item, "@")
		if atIdx < 0 {
			log.Printf("[MOM] %s: invalid spec %q (missing @) for job %s", direction, item, jobID)
			continue
		}
		localFile := item[:atIdx]
		remote := item[atIdx+1:] // "host:path"

		var cmd *exec.Cmd
		if direction == "stagein" {
			// Copy from remote to local
			log.Printf("[MOM] Stagein for %s: %s -> %s", jobID, remote, localFile)
			cmd = exec.Command("scp", "-o", "StrictHostKeyChecking=no", "-o", "BatchMode=yes", remote, localFile)
		} else {
			// Copy from local to remote
			log.Printf("[MOM] Stageout for %s: %s -> %s", jobID, localFile, remote)
			cmd = exec.Command("scp", "-o", "StrictHostKeyChecking=no", "-o", "BatchMode=yes", localFile, remote)
		}

		output, err := cmd.CombinedOutput()
		if err != nil {
			log.Printf("[MOM] %s failed for %s: %v output=%s", direction, jobID, err, string(output))
			return fmt.Errorf("%s %q: %w", direction, item, err)
		}
		log.Printf("[MOM] %s completed for %s: %s", direction, jobID, item)
	}
	return nil
}
