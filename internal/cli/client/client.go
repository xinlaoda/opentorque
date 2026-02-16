// Package client provides a PBS batch protocol client with token-based authentication.
// It connects to a pbs_server, authenticates using HMAC-SHA256 shared key,
// and provides methods for sending batch requests and reading replies.
package client

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/xinlaoda/opentorque/internal/cli/dis"
)

const (
	defaultServer  = "localhost"
	defaultPort    = 15001
	defaultPBSHome = "/var/spool/torque"
	authKeyFile    = "auth_key"
	authTimeout    = 10 * time.Second
	requestTimeout = 30 * time.Second
)

// Conn represents an authenticated connection to the PBS server.
type Conn struct {
	conn     net.Conn
	reader   *dis.Reader
	writer   *dis.Writer
	server   string
	user     string
	pbsHome  string
}

// SvrAttrl is re-exported from the dis package for convenience.
type SvrAttrl = dis.SvrAttrl

// StatusObject represents one object in a status reply.
type StatusObject struct {
	Type  int
	Name  string
	Attrs []SvrAttrl
}

// Connect establishes and authenticates a connection to the PBS server.
// It reads the server name from PBS_DEFAULT or server_name file,
// then authenticates using the shared HMAC key.
func Connect(server string) (*Conn, error) {
	pbsHome := os.Getenv("PBS_HOME")
	if pbsHome == "" {
		pbsHome = defaultPBSHome
	}

	// Determine server address
	if server == "" {
		server = resolveServer(pbsHome)
	}

	// Determine current user
	u, err := user.Current()
	if err != nil {
		return nil, fmt.Errorf("get current user: %w", err)
	}
	username := u.Username

	// Add default port if not specified
	host, port := server, fmt.Sprintf("%d", defaultPort)
	if h, p, err := net.SplitHostPort(server); err == nil {
		host, port = h, p
	}
	addr := net.JoinHostPort(host, port)

	// Connect to server
	conn, err := net.DialTimeout("tcp", addr, authTimeout)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", addr, err)
	}

	c := &Conn{
		conn:    conn,
		reader:  dis.NewReader(conn),
		writer:  dis.NewWriter(conn),
		server:  host,
		user:    username,
		pbsHome: pbsHome,
	}

	// Authenticate with token
	if err := c.authenticate(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("authenticate: %w", err)
	}

	return c, nil
}

// Close sends a Disconnect request and closes the connection.
func (c *Conn) Close() error {
	// Send disconnect
	c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	c.writeHeader(dis.BatchReqDisconnect)
	c.writer.WriteString("") // extension
	c.writer.Flush()
	return c.conn.Close()
}

// Server returns the server hostname.
func (c *Conn) Server() string {
	return c.server
}

// User returns the authenticated username.
func (c *Conn) User() string {
	return c.user
}

// authenticate sends an AuthToken request to the server.
func (c *Conn) authenticate() error {
	c.conn.SetDeadline(time.Now().Add(authTimeout))
	defer c.conn.SetDeadline(time.Time{})

	// Load shared key
	keyPath := filepath.Join(c.pbsHome, authKeyFile)
	data, err := os.ReadFile(keyPath)
	if err != nil {
		return fmt.Errorf("read auth key %s: %w", keyPath, err)
	}
	hexStr := strings.TrimSpace(string(data))
	key, err := hex.DecodeString(hexStr)
	if err != nil {
		return fmt.Errorf("decode auth key: %w", err)
	}

	// Compute HMAC token
	timestamp := time.Now().Unix()
	message := fmt.Sprintf("%s|%d", c.user, timestamp)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(message))
	token := hex.EncodeToString(mac.Sum(nil))

	// Send AuthToken request header + body
	if err := c.writeHeader(dis.BatchReqAuthToken); err != nil {
		return err
	}
	if err := c.writer.WriteUint(uint64(timestamp)); err != nil {
		return err
	}
	if err := c.writer.WriteString(token); err != nil {
		return err
	}
	if err := c.writer.WriteString(""); err != nil { // extension
		return err
	}
	if err := c.writer.Flush(); err != nil {
		return err
	}

	// Read reply
	code, _, _, _, err := c.readReply()
	if err != nil {
		return fmt.Errorf("read auth reply: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("authentication rejected (code=%d)", code)
	}
	return nil
}

// writeHeader writes a batch request header.
func (c *Conn) writeHeader(reqType int) error {
	if err := c.writer.WriteUint(dis.BatchProtType); err != nil {
		return err
	}
	if err := c.writer.WriteUint(dis.BatchProtVer); err != nil {
		return err
	}
	if err := c.writer.WriteUint(uint64(reqType)); err != nil {
		return err
	}
	return c.writer.WriteString(c.user)
}

// readReply reads a standard batch reply (proto, ver, code, auxcode, choice).
func (c *Conn) readReply() (code, auxCode, choice int, data string, err error) {
	c.conn.SetReadDeadline(time.Now().Add(requestTimeout))
	defer c.conn.SetReadDeadline(time.Time{})

	if _, err = c.reader.ReadUint(); err != nil { // proto
		return
	}
	if _, err = c.reader.ReadUint(); err != nil { // ver
		return
	}
	var ci64 int64
	ci64, err = c.reader.ReadInt() // code
	if err != nil {
		return
	}
	code = int(ci64)

	ci64, err = c.reader.ReadInt() // auxcode
	if err != nil {
		return
	}
	auxCode = int(ci64)

	var cu64 uint64
	cu64, err = c.reader.ReadUint() // choice
	if err != nil {
		return
	}
	choice = int(cu64)

	// Read choice-specific data
	switch choice {
	case dis.ReplyChoiceQueue, dis.ReplyChoiceRdytoCom, dis.ReplyChoiceCommit:
		data, err = c.reader.ReadString()
	case dis.ReplyChoiceText:
		data, err = c.reader.ReadString()
	case dis.ReplyChoiceNull:
		// no extra data
	}
	return
}

// readStatusReply reads a status reply containing multiple objects.
// Format after header: count(uint), then per-object: objtype(uint) name(str) attrs(svrattrl)
func (c *Conn) readStatusReply() ([]StatusObject, error) {
	c.conn.SetReadDeadline(time.Now().Add(requestTimeout))
	defer c.conn.SetReadDeadline(time.Time{})

	if _, err := c.reader.ReadUint(); err != nil { // proto
		return nil, err
	}
	if _, err := c.reader.ReadUint(); err != nil { // ver
		return nil, err
	}
	codeI, err := c.reader.ReadInt() // code
	if err != nil {
		return nil, err
	}
	if _, err := c.reader.ReadInt(); err != nil { // auxcode
		return nil, err
	}
	choiceU, err := c.reader.ReadUint() // choice
	if err != nil {
		return nil, err
	}

	if int(codeI) != 0 {
		return nil, fmt.Errorf("server error %d", codeI)
	}
	if int(choiceU) != dis.ReplyChoiceStatus {
		return nil, fmt.Errorf("unexpected reply choice %d (expected %d)", choiceU, dis.ReplyChoiceStatus)
	}

	count, err := c.reader.ReadUint()
	if err != nil {
		return nil, fmt.Errorf("read status count: %w", err)
	}

	objects := make([]StatusObject, 0, count)
	for i := uint64(0); i < count; i++ {
		objType, err := c.reader.ReadUint()
		if err != nil {
			return nil, fmt.Errorf("read obj[%d] type: %w", i, err)
		}
		name, err := c.reader.ReadString()
		if err != nil {
			return nil, fmt.Errorf("read obj[%d] name: %w", i, err)
		}
		attrs, err := dis.ReadSvrAttrl(c.reader)
		if err != nil {
			return nil, fmt.Errorf("read obj[%d] attrs: %w", i, err)
		}
		objects = append(objects, StatusObject{
			Type:  int(objType),
			Name:  name,
			Attrs: attrs,
		})
	}
	return objects, nil
}

// StatusJob sends a StatusJob request and returns status objects.
func (c *Conn) StatusJob(jobID string) ([]StatusObject, error) {
	return c.sendStatusRequest(dis.BatchReqStatusJob, jobID)
}

// StatusQueue sends a StatusQueue request and returns status objects.
func (c *Conn) StatusQueue(queueName string) ([]StatusObject, error) {
	return c.sendStatusRequest(dis.BatchReqStatusQueue, queueName)
}

// StatusServer sends a StatusServer request and returns status objects.
func (c *Conn) StatusServer() ([]StatusObject, error) {
	return c.sendStatusRequest(dis.BatchReqStatusServer, "")
}

// StatusNode sends a StatusNode request and returns status objects.
func (c *Conn) StatusNode(nodeID string) ([]StatusObject, error) {
	return c.sendStatusRequest(dis.BatchReqStatusNode, nodeID)
}

// sendStatusRequest sends a status request and reads the status reply.
// Body format: string(id) svrattrl(query_attrs — empty) string(extension)
func (c *Conn) sendStatusRequest(reqType int, id string) ([]StatusObject, error) {
	c.conn.SetWriteDeadline(time.Now().Add(requestTimeout))
	if err := c.writeHeader(reqType); err != nil {
		return nil, err
	}
	if err := c.writer.WriteString(id); err != nil {
		return nil, err
	}
	// Empty attribute list (query all attrs)
	if err := dis.WriteSvrAttrl(c.writer, nil); err != nil {
		return nil, err
	}
	if err := c.writer.WriteString(""); err != nil { // extension
		return nil, err
	}
	if err := c.writer.Flush(); err != nil {
		return nil, err
	}
	return c.readStatusReply()
}

// SubmitJob sends a QueueJob2 + JobScript2 request (auto-commit protocol).
// Returns the assigned job ID.
func (c *Conn) SubmitJob(queue string, attrs []dis.SvrAttrl, script string) (string, error) {
	c.conn.SetWriteDeadline(time.Now().Add(requestTimeout))

	// QueueJob2: header + jobid("") + destination(queue) + attrs
	if err := c.writeHeader(dis.BatchReqQueueJob2); err != nil {
		return "", err
	}
	if err := c.writer.WriteString(""); err != nil { // empty jobid (server assigns)
		return "", err
	}
	if err := c.writer.WriteString(queue); err != nil {
		return "", err
	}
	if err := dis.WriteSvrAttrl(c.writer, attrs); err != nil {
		return "", err
	}
	if err := c.writer.WriteString(""); err != nil { // extension
		return "", err
	}
	if err := c.writer.Flush(); err != nil {
		return "", err
	}

	// Read QueueJob reply — should return job ID
	code, _, choice, jobID, err := c.readReply()
	if err != nil {
		return "", fmt.Errorf("read queuejob reply: %w", err)
	}
	if code != 0 {
		return "", fmt.Errorf("queuejob rejected (code=%d)", code)
	}
	if choice != dis.ReplyChoiceQueue {
		return "", fmt.Errorf("unexpected reply choice %d", choice)
	}

	// JobScript2: header + seq(0) + type(0) + size + jobid + script_data
	if err := c.writeHeader(dis.BatchReqJobScript2); err != nil {
		return "", err
	}
	if err := c.writer.WriteUint(0); err != nil { // seq
		return "", err
	}
	if err := c.writer.WriteUint(0); err != nil { // type
		return "", err
	}
	if err := c.writer.WriteUint(uint64(len(script))); err != nil { // size
		return "", err
	}
	if err := c.writer.WriteString(jobID); err != nil {
		return "", err
	}
	if err := c.writer.WriteString(script); err != nil {
		return "", err
	}
	if err := c.writer.WriteString(""); err != nil { // extension
		return "", err
	}
	if err := c.writer.Flush(); err != nil {
		return "", err
	}

	// Read script reply
	code, _, _, _, err = c.readReply()
	if err != nil {
		return "", fmt.Errorf("read script reply: %w", err)
	}
	if code != 0 {
		return "", fmt.Errorf("script rejected (code=%d)", code)
	}

	return jobID, nil
}

// DeleteJob sends a DeleteJob request.
// Body uses Manage format: cmd(uint) objtype(uint) name(str) attrs(svrattrl) extension(str)
func (c *Conn) DeleteJob(jobID string) error {
	return c.DeleteJobExtend(jobID, "")
}

// DeleteJobExtend sends a DeleteJob request with an extension string.
func (c *Conn) DeleteJobExtend(jobID, extend string) error {
	c.conn.SetWriteDeadline(time.Now().Add(requestTimeout))
	if err := c.writeHeader(dis.BatchReqDeleteJob); err != nil {
		return err
	}
	if err := c.writer.WriteUint(0); err != nil { // cmd
		return err
	}
	if err := c.writer.WriteUint(uint64(dis.MgrObjJob)); err != nil { // objtype
		return err
	}
	if err := c.writer.WriteString(jobID); err != nil {
		return err
	}
	if err := dis.WriteSvrAttrl(c.writer, nil); err != nil { // empty attrs
		return err
	}
	if err := c.writer.WriteString(extend); err != nil { // extension
		return err
	}
	if err := c.writer.Flush(); err != nil {
		return err
	}

	code, _, _, _, err := c.readReply()
	if err != nil {
		return fmt.Errorf("read delete reply: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("delete rejected (code=%d)", code)
	}
	return nil
}

// HoldJob sends a HoldJob request with the specified hold type.
// Body uses Manage format: cmd(uint) objtype(uint) name(str) attrs(svrattrl) extension(str)
func (c *Conn) HoldJob(jobID string, holdType string) error {
	c.conn.SetWriteDeadline(time.Now().Add(requestTimeout))
	if err := c.writeHeader(dis.BatchReqHoldJob); err != nil {
		return err
	}
	if err := c.writer.WriteUint(0); err != nil { // cmd
		return err
	}
	if err := c.writer.WriteUint(uint64(dis.MgrObjJob)); err != nil { // objtype
		return err
	}
	if err := c.writer.WriteString(jobID); err != nil {
		return err
	}

	// Hold type as attribute
	attrs := []dis.SvrAttrl{{
		Name:  "Hold_Types",
		Value: holdType,
		Op:    1, // SET
	}}
	if err := dis.WriteSvrAttrl(c.writer, attrs); err != nil {
		return err
	}
	if err := c.writer.WriteString(""); err != nil { // extension
		return err
	}
	if err := c.writer.Flush(); err != nil {
		return err
	}

	code, _, _, _, err := c.readReply()
	if err != nil {
		return fmt.Errorf("read hold reply: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("hold rejected (code=%d)", code)
	}
	return nil
}

// ReleaseJob sends a ReleaseJob request.
// Body uses Manage format: cmd(uint) objtype(uint) name(str) attrs(svrattrl) extension(str)
func (c *Conn) ReleaseJob(jobID string, holdType string) error {
	c.conn.SetWriteDeadline(time.Now().Add(requestTimeout))
	if err := c.writeHeader(dis.BatchReqReleaseJob); err != nil {
		return err
	}
	if err := c.writer.WriteUint(0); err != nil { // cmd
		return err
	}
	if err := c.writer.WriteUint(uint64(dis.MgrObjJob)); err != nil { // objtype
		return err
	}
	if err := c.writer.WriteString(jobID); err != nil {
		return err
	}

	attrs := []dis.SvrAttrl{{
		Name:  "Hold_Types",
		Value: holdType,
		Op:    1, // SET
	}}
	if err := dis.WriteSvrAttrl(c.writer, attrs); err != nil {
		return err
	}
	if err := c.writer.WriteString(""); err != nil { // extension
		return err
	}
	if err := c.writer.Flush(); err != nil {
		return err
	}

	code, _, _, _, err := c.readReply()
	if err != nil {
		return fmt.Errorf("read release reply: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("release rejected (code=%d)", code)
	}
	return nil
}

// Manager sends a Manager request (create/delete/set/unset/list queues, server, nodes).
func (c *Conn) Manager(cmd, objType int, objName string, attrs []dis.SvrAttrl) error {
	c.conn.SetWriteDeadline(time.Now().Add(requestTimeout))
	if err := c.writeHeader(dis.BatchReqManager); err != nil {
		return err
	}
	if err := c.writer.WriteUint(uint64(cmd)); err != nil {
		return err
	}
	if err := c.writer.WriteUint(uint64(objType)); err != nil {
		return err
	}
	if err := c.writer.WriteString(objName); err != nil {
		return err
	}
	if err := dis.WriteSvrAttrl(c.writer, attrs); err != nil {
		return err
	}
	if err := c.writer.WriteString(""); err != nil { // extension
		return err
	}
	if err := c.writer.Flush(); err != nil {
		return err
	}

	code, _, _, _, err := c.readReply()
	if err != nil {
		return fmt.Errorf("read manager reply: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("manager command rejected (code=%d)", code)
	}
	return nil
}

// ModifyJob sends a modify-job request to change job attributes (qalter).
// Body format matches server's ReadModifyJobBody: uint(cmd) uint(objtype) string(jobid) svrattrl
func (c *Conn) ModifyJob(jobID string, attrs []dis.SvrAttrl) error {
	c.conn.SetWriteDeadline(time.Now().Add(requestTimeout))
	if err := c.writeHeader(dis.BatchReqModifyJob); err != nil {
		return err
	}
	if err := c.writer.WriteUint(0); err != nil { // cmd (unused)
		return err
	}
	if err := c.writer.WriteUint(uint64(dis.MgrObjJob)); err != nil { // objtype
		return err
	}
	if err := c.writer.WriteString(jobID); err != nil {
		return err
	}
	if err := dis.WriteSvrAttrl(c.writer, attrs); err != nil {
		return err
	}
	if err := c.writer.WriteString(""); err != nil { // extension
		return err
	}
	if err := c.writer.Flush(); err != nil {
		return err
	}
	code, _, _, _, err := c.readReply()
	if err != nil {
		return fmt.Errorf("read modify reply: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("modify job rejected (code=%d)", code)
	}
	return nil
}

// RunJob sends a run-job request to force a job to run on a specific node (qrun).
func (c *Conn) RunJob(jobID, destination string) error {
	c.conn.SetWriteDeadline(time.Now().Add(requestTimeout))
	if err := c.writeHeader(dis.BatchReqRunJob); err != nil {
		return err
	}
	if err := c.writer.WriteString(jobID); err != nil {
		return err
	}
	if err := c.writer.WriteString(destination); err != nil {
		return err
	}
	if err := c.writer.WriteString(""); err != nil {
		return err
	}
	if err := c.writer.Flush(); err != nil {
		return err
	}
	code, _, _, _, err := c.readReply()
	if err != nil {
		return fmt.Errorf("read runjob reply: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("run job rejected (code=%d)", code)
	}
	return nil
}

// SignalJob sends a signal to a running job (qsig).
func (c *Conn) SignalJob(jobID, signal string) error {
	c.conn.SetWriteDeadline(time.Now().Add(requestTimeout))
	if err := c.writeHeader(dis.BatchReqSignalJob); err != nil {
		return err
	}
	if err := c.writer.WriteString(jobID); err != nil {
		return err
	}
	if err := c.writer.WriteString(signal); err != nil {
		return err
	}
	if err := c.writer.WriteString(""); err != nil {
		return err
	}
	if err := c.writer.Flush(); err != nil {
		return err
	}
	code, _, _, _, err := c.readReply()
	if err != nil {
		return fmt.Errorf("read signal reply: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("signal job rejected (code=%d)", code)
	}
	return nil
}

// Shutdown sends a server shutdown request (qterm).
func (c *Conn) Shutdown(manner int) error {
	c.conn.SetWriteDeadline(time.Now().Add(requestTimeout))
	if err := c.writeHeader(dis.BatchReqShutdown); err != nil {
		return err
	}
	if err := c.writer.WriteUint(uint64(manner)); err != nil {
		return err
	}
	if err := c.writer.WriteString(""); err != nil {
		return err
	}
	if err := c.writer.Flush(); err != nil {
		return err
	}
	code, _, _, _, err := c.readReply()
	if err != nil {
		// Server may close connection during shutdown - that's OK
		if strings.Contains(err.Error(), "EOF") || strings.Contains(err.Error(), "reset") {
			return nil
		}
		return fmt.Errorf("read shutdown reply: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("shutdown rejected (code=%d)", code)
	}
	return nil
}

// SelectJobs returns job IDs matching the given selection attributes (qselect).
// Body format: string(id) svrattrl (same as status body)
func (c *Conn) SelectJobs(attrs []dis.SvrAttrl) ([]string, error) {
	c.conn.SetWriteDeadline(time.Now().Add(requestTimeout))
	if err := c.writeHeader(dis.BatchReqSelectJobs); err != nil {
		return nil, err
	}
	if err := c.writer.WriteString(""); err != nil { // empty ID = all jobs
		return nil, err
	}
	if err := dis.WriteSvrAttrl(c.writer, attrs); err != nil {
		return nil, err
	}
	if err := c.writer.WriteString(""); err != nil { // extension
		return nil, err
	}
	if err := c.writer.Flush(); err != nil {
		return nil, err
	}

	c.conn.SetReadDeadline(time.Now().Add(requestTimeout))
	code, _, _, data, err := c.readReply()
	if err != nil {
		return nil, fmt.Errorf("read select reply: %w", err)
	}
	if code != 0 {
		return nil, fmt.Errorf("select jobs rejected (code=%d)", code)
	}
	// Parse text reply containing newline-separated job IDs
	if data == "" {
		return nil, nil
	}
	var ids []string
	for _, id := range strings.Split(data, "\n") {
		id = strings.TrimSpace(id)
		if id != "" {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// MoveJob moves a job to a different queue or server (qmove).
func (c *Conn) MoveJob(jobID, destination string) error {
	c.conn.SetWriteDeadline(time.Now().Add(requestTimeout))
	if err := c.writeHeader(dis.BatchReqMoveJob); err != nil {
		return err
	}
	if err := c.writer.WriteString(jobID); err != nil {
		return err
	}
	if err := c.writer.WriteString(destination); err != nil {
		return err
	}
	if err := c.writer.WriteString(""); err != nil {
		return err
	}
	if err := c.writer.Flush(); err != nil {
		return err
	}
	code, _, _, _, err := c.readReply()
	if err != nil {
		return fmt.Errorf("read move reply: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("move job rejected (code=%d)", code)
	}
	return nil
}

// RerunJob requeues a running job back to queued state (qrerun).
func (c *Conn) RerunJob(jobID string, force bool) error {
	c.conn.SetWriteDeadline(time.Now().Add(requestTimeout))
	if err := c.writeHeader(dis.BatchReqRerun); err != nil {
		return err
	}
	if err := c.writer.WriteString(jobID); err != nil {
		return err
	}
	// Extension: use WriteString("") for empty (compatible with ReadReqExtend)
	// For force mode, pass flag in the extension field
	if force {
		if err := c.writer.WriteString("RERUNFORCE"); err != nil {
			return err
		}
	} else {
		if err := c.writer.WriteString(""); err != nil {
			return err
		}
	}
	if err := c.writer.Flush(); err != nil {
		return err
	}
	code, _, _, _, err := c.readReply()
	if err != nil {
		return fmt.Errorf("read rerun reply: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("rerun job rejected (code=%d)", code)
	}
	return nil
}

// OrderJob swaps the order of two jobs in the queue (qorder).
func (c *Conn) OrderJob(jobID1, jobID2 string) error {
	c.conn.SetWriteDeadline(time.Now().Add(requestTimeout))
	if err := c.writeHeader(dis.BatchReqOrderJob); err != nil {
		return err
	}
	if err := c.writer.WriteString(jobID1); err != nil {
		return err
	}
	if err := c.writer.WriteString(jobID2); err != nil {
		return err
	}
	if err := c.writer.WriteString(""); err != nil {
		return err
	}
	if err := c.writer.Flush(); err != nil {
		return err
	}
	code, _, _, _, err := c.readReply()
	if err != nil {
		return fmt.Errorf("read order reply: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("order job rejected (code=%d)", code)
	}
	return nil
}

// MessJob sends a message to a job's output file (qmsg).
func (c *Conn) MessJob(jobID string, fileOpt int, message string) error {
	c.conn.SetWriteDeadline(time.Now().Add(requestTimeout))
	if err := c.writeHeader(dis.BatchReqMessJob); err != nil {
		return err
	}
	if err := c.writer.WriteString(jobID); err != nil {
		return err
	}
	if err := c.writer.WriteUint(uint64(fileOpt)); err != nil {
		return err
	}
	if err := c.writer.WriteString(message); err != nil {
		return err
	}
	if err := c.writer.WriteString(""); err != nil {
		return err
	}
	if err := c.writer.Flush(); err != nil {
		return err
	}
	code, _, _, _, err := c.readReply()
	if err != nil {
		return fmt.Errorf("read messjob reply: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("message job rejected (code=%d)", code)
	}
	return nil
}

// CheckpointJob requests checkpoint of a running job (qchkpt).
func (c *Conn) CheckpointJob(jobID string) error {
	c.conn.SetWriteDeadline(time.Now().Add(requestTimeout))
	if err := c.writeHeader(dis.BatchReqCheckpointJob); err != nil {
		return err
	}
	if err := c.writer.WriteString(jobID); err != nil {
		return err
	}
	if err := c.writer.WriteString(""); err != nil {
		return err
	}
	if err := c.writer.Flush(); err != nil {
		return err
	}
	code, _, _, _, err := c.readReply()
	if err != nil {
		return fmt.Errorf("read checkpoint reply: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("checkpoint job rejected (code=%d)", code)
	}
	return nil
}

// resolveServer determines the PBS server hostname from configuration.
func resolveServer(pbsHome string) string {
	// Check PBS_DEFAULT environment variable
	if s := os.Getenv("PBS_DEFAULT"); s != "" {
		return s
	}
	// Check server_name file
	path := filepath.Join(pbsHome, "server_name")
	data, err := os.ReadFile(path)
	if err == nil {
		s := strings.TrimSpace(string(data))
		if s != "" {
			return s
		}
	}
	return defaultServer
}
