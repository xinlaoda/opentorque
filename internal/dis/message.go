// Package dis implements the DIS (Data Is Strings) wire protocol used by PBS/TORQUE.
// This file handles server-side message encoding/decoding for batch requests and replies.
package dis

import (
	"fmt"
	"io"
	"net"
)

// RequestHeader holds the parsed header fields from an incoming batch request.
type RequestHeader struct {
	Protocol int    // Protocol type (PBS_BATCH_PROT_TYPE=2 for batch, 4 for IS)
	Version  int    // Protocol version
	ReqType  int    // Request type code (PBS_BATCH_QueueJob, etc.)
	User     string // Authenticated user name
}

// ReadRequestHeader reads a batch protocol request header from the DIS stream.
// Header format: diswui(proto) diswui(ver) diswui(reqtype) diswst(user)
func ReadRequestHeader(r *Reader) (*RequestHeader, error) {
	proto, err := r.ReadUint()
	if err != nil {
		return nil, fmt.Errorf("read proto: %w", err)
	}
	ver, err := r.ReadUint()
	if err != nil {
		return nil, fmt.Errorf("read version: %w", err)
	}
	reqType, err := r.ReadUint()
	if err != nil {
		return nil, fmt.Errorf("read reqtype: %w", err)
	}
	user, err := r.ReadString()
	if err != nil {
		return nil, fmt.Errorf("read user: %w", err)
	}
	return &RequestHeader{
		Protocol: int(proto),
		Version:  int(ver),
		ReqType:  int(reqType),
		User:     user,
	}, nil
}

// WriteReply writes a batch reply to the DIS stream.
// Reply format: diswui(proto) diswui(ver) diswsi(code) diswsi(auxcode) diswui(choice) [data]
// CRITICAL: For BATCH_REPLY_CHOICE_NULL (1), NO extra data is written.
func WriteReply(w *Writer, code, auxCode, choice int, data string) error {
	if err := w.WriteUint(uint64(PbsBatchProtType)); err != nil {
		return err
	}
	if err := w.WriteUint(uint64(PbsBatchProtVer)); err != nil {
		return err
	}
	// Error code and aux code are signed integers
	if err := w.WriteInt(int64(code)); err != nil {
		return err
	}
	if err := w.WriteInt(int64(auxCode)); err != nil {
		return err
	}
	if err := w.WriteUint(uint64(choice)); err != nil {
		return err
	}
	// Write choice-specific data (NULL choice writes nothing)
	switch choice {
	case ReplyChoiceQueue, ReplyChoiceRdytoCom, ReplyChoiceCommit:
		// Job ID string
		if err := w.WriteString(data); err != nil {
			return err
		}
	case ReplyChoiceText:
		if err := w.WriteString(data); err != nil {
			return err
		}
	case ReplyChoiceNull:
		// No additional data for NULL reply
	case ReplyChoiceStatus:
		// Status data is written by caller before this
	}
	return w.Flush()
}

// SendOkReply sends a success reply with no data.
func SendOkReply(conn net.Conn) error {
	w := NewWriter(conn)
	return WriteReply(w, PbsErrNone, 0, ReplyChoiceNull, "")
}

// SendErrorReply sends an error reply to a connection.
func SendErrorReply(conn net.Conn, errCode, auxCode int) error {
	w := NewWriter(conn)
	return WriteReply(w, errCode, auxCode, ReplyChoiceNull, "")
}

// SendJobIDReply sends a success reply with a job ID for Queue/RdytoCom/Commit.
func SendJobIDReply(conn net.Conn, choice int, jobID string) error {
	w := NewWriter(conn)
	return WriteReply(w, PbsErrNone, 0, choice, jobID)
}

// SendTextReply sends a success reply with text data.
func SendTextReply(conn net.Conn, text string) error {
	w := NewWriter(conn)
	return WriteReply(w, PbsErrNone, 0, ReplyChoiceText, text)
}

// WriteRequestHeader writes a batch request header for outgoing requests (server→MOM).
// Format: diswui(proto) diswui(ver) diswui(reqtype) diswst(user)
func WriteRequestHeader(w *Writer, reqType int, user string) error {
	if err := w.WriteUint(uint64(PbsBatchProtType)); err != nil {
		return err
	}
	if err := w.WriteUint(uint64(PbsBatchProtVer)); err != nil {
		return err
	}
	if err := w.WriteUint(uint64(reqType)); err != nil {
		return err
	}
	return w.WriteString(user)
}

// WriteReqExtend writes the request extension field.
// Format: diswui(has_extend) [diswst(extend_string)]
func WriteReqExtend(w *Writer, ext string) error {
	if ext == "" {
		return w.WriteUint(0)
	}
	if err := w.WriteUint(1); err != nil {
		return err
	}
	return w.WriteString(ext)
}

// ReadReqExtend reads the request extension field from a DIS stream.
func ReadReqExtend(r *Reader) (string, error) {
	hasExt, err := r.ReadUint()
	if err != nil {
		return "", err
	}
	if hasExt == 0 {
		return "", nil
	}
	return r.ReadString()
}

// SvrAttrl represents a single attribute in the svrattrl list format.
type SvrAttrl struct {
	Name    string
	HasResc bool
	Resc    string
	Value   string
	Op      int // SET=1, UNSET=2, INCR=3, DECR=4, EQ=5, NE=6, GE=7, GT=8, LE=9, LT=10, DFLT=11
}

// ReadSvrAttrl reads an svrattrl list from the DIS stream.
// Format: count(uint), then per-attr: size(uint), name(str), hasResc(uint), [resc(str)], value(str), op(uint)
func ReadSvrAttrl(r *Reader) ([]SvrAttrl, error) {
	count, err := r.ReadUint()
	if err != nil {
		return nil, fmt.Errorf("read attr count: %w", err)
	}
	attrs := make([]SvrAttrl, 0, count)
	for i := uint64(0); i < count; i++ {
		// Read size field (unused but required)
		if _, err := r.ReadUint(); err != nil {
			return nil, fmt.Errorf("read attr[%d] size: %w", i, err)
		}
		name, err := r.ReadString()
		if err != nil {
			return nil, fmt.Errorf("read attr[%d] name: %w", i, err)
		}
		hasResc, err := r.ReadUint()
		if err != nil {
			return nil, fmt.Errorf("read attr[%d] hasResc: %w", i, err)
		}
		var resc string
		if hasResc != 0 {
			resc, err = r.ReadString()
			if err != nil {
				return nil, fmt.Errorf("read attr[%d] resc: %w", i, err)
			}
		}
		value, err := r.ReadString()
		if err != nil {
			return nil, fmt.Errorf("read attr[%d] value: %w", i, err)
		}
		op, err := r.ReadUint()
		if err != nil {
			return nil, fmt.Errorf("read attr[%d] op: %w", i, err)
		}
		attrs = append(attrs, SvrAttrl{
			Name:    name,
			HasResc: hasResc != 0,
			Resc:    resc,
			Value:   value,
			Op:      int(op),
		})
	}
	return attrs, nil
}

// WriteSvrAttrl writes an svrattrl list to the DIS stream.
func WriteSvrAttrl(w *Writer, attrs []SvrAttrl) error {
	if err := w.WriteUint(uint64(len(attrs))); err != nil {
		return err
	}
	for _, a := range attrs {
		// Calculate size field (name + hasResc + [resc] + value + op)
		size := len(a.Name) + len(a.Value) + 2
		if a.HasResc {
			size += len(a.Resc) + 1
		}
		if err := w.WriteUint(uint64(size)); err != nil {
			return err
		}
		if err := w.WriteString(a.Name); err != nil {
			return err
		}
		if a.HasResc {
			if err := w.WriteUint(1); err != nil {
				return err
			}
			if err := w.WriteString(a.Resc); err != nil {
				return err
			}
		} else {
			if err := w.WriteUint(0); err != nil {
				return err
			}
		}
		if err := w.WriteString(a.Value); err != nil {
			return err
		}
		if err := w.WriteUint(uint64(a.Op)); err != nil {
			return err
		}
	}
	return nil
}

// ReadReply reads a batch protocol reply from a DIS stream (used when server reads
// a reply from MOM after sending a request).
func ReadReply(r *Reader) (code, auxCode, choice int, data string, err error) {
	proto, err := r.ReadUint()
	if err != nil {
		return 0, 0, 0, "", fmt.Errorf("read reply proto: %w", err)
	}
	_ = proto // verify if needed
	if _, err := r.ReadUint(); err != nil {
		return 0, 0, 0, "", fmt.Errorf("read reply ver: %w", err)
	}
	codeI64, err := r.ReadInt()
	code = int(codeI64)
	if err != nil {
		return 0, 0, 0, "", fmt.Errorf("read reply code: %w", err)
	}
	auxI64, err := r.ReadInt()
	auxCode = int(auxI64)
	if err != nil {
		return 0, 0, 0, "", fmt.Errorf("read reply auxcode: %w", err)
	}
	choiceU64, err := r.ReadUint()
	choice = int(choiceU64)
	if err != nil {
		return 0, 0, 0, "", fmt.Errorf("read reply choice: %w", err)
	}
	// Read choice-specific data
	switch choice {
	case ReplyChoiceQueue, ReplyChoiceRdytoCom, ReplyChoiceCommit:
		data, err = r.ReadString()
	case ReplyChoiceText:
		data, err = r.ReadString()
	case ReplyChoiceNull:
		// No extra data
	}
	return code, auxCode, choice, data, err
}

// WriteStatusReply writes a status reply containing a list of objects (jobs, queues, nodes).
// Each object has a name and a list of attributes.
// Format: proto ver code(0) aux(0) choice(STATUS=6)
//
//	then for each object: name(str) attrs(svrattrl)
//	terminated by empty name string
func WriteStatusReply(conn net.Conn, objects []StatusObject) error {
	w := NewWriter(conn)
	if err := w.WriteUint(uint64(PbsBatchProtType)); err != nil {
		return err
	}
	if err := w.WriteUint(uint64(PbsBatchProtVer)); err != nil {
		return err
	}
	if err := w.WriteInt(PbsErrNone); err != nil {
		return err
	}
	if err := w.WriteInt(0); err != nil {
		return err
	}
	if err := w.WriteUint(ReplyChoiceStatus); err != nil {
		return err
	}
	// Write count of status objects
	if err := w.WriteUint(uint64(len(objects))); err != nil {
		return err
	}
	// Write each status object: objtype(uint) name(string) attrs(attrl)
	for _, obj := range objects {
		if err := w.WriteUint(uint64(obj.Type)); err != nil {
			return err
		}
		if err := w.WriteString(obj.Name); err != nil {
			return err
		}
		if err := WriteSvrAttrl(w, obj.Attrs); err != nil {
			return err
		}
	}
	return w.Flush()
}

// StatusObject represents one object in a status reply.
type StatusObject struct {
	Type  int    // Object type (e.g., MGR_OBJ_NODE, MGR_OBJ_JOB, etc.)
	Name  string
	Attrs []SvrAttrl
}

// ReadJobObit reads the body of a JobObit request.
// Format: diswst(job_id) diswsi(exit_status) svrattrl(resources_used)
func ReadJobObit(r *Reader) (jobID string, exitStatus int, attrs []SvrAttrl, err error) {
	jobID, err = r.ReadString()
	if err != nil {
		return "", 0, nil, fmt.Errorf("read obit jobid: %w", err)
	}
	exitStatusI64, err := r.ReadInt()
	exitStatus = int(exitStatusI64)
	if err != nil {
		return "", 0, nil, fmt.Errorf("read obit exit status: %w", err)
	}
	attrs, err = ReadSvrAttrl(r)
	if err != nil {
		return "", 0, nil, fmt.Errorf("read obit attrs: %w", err)
	}
	return jobID, exitStatus, attrs, nil
}

// ReadQueueJobBody reads the body of a QueueJob request.
// Format: diswst(jobid) diswst(destination) svrattrl(attributes)
func ReadQueueJobBody(r *Reader) (jobID, dest string, attrs []SvrAttrl, err error) {
	jobID, err = r.ReadString()
	if err != nil {
		return "", "", nil, fmt.Errorf("read queuejob id: %w", err)
	}
	dest, err = r.ReadString()
	if err != nil {
		return "", "", nil, fmt.Errorf("read queuejob dest: %w", err)
	}
	attrs, err = ReadSvrAttrl(r)
	if err != nil {
		return "", "", nil, fmt.Errorf("read queuejob attrs: %w", err)
	}
	return jobID, dest, attrs, nil
}

// ReadJobScriptBody reads the body of a JobScript request.
// Format: diswui(seq) diswui(type) diswui(size) diswst(jobid) diswcs(data)
func ReadJobScriptBody(r *Reader) (jobID string, data []byte, err error) {
	if _, err = r.ReadUint(); err != nil { // seq
		return "", nil, err
	}
	if _, err = r.ReadUint(); err != nil { // type
		return "", nil, err
	}
	size, err := r.ReadUint()
	if err != nil {
		return "", nil, err
	}
	jobID, err = r.ReadString()
	if err != nil {
		return "", nil, err
	}
	scriptData, err := r.ReadString()
	if err != nil {
		return "", nil, err
	}
	_ = size // declared size vs actual
	return jobID, []byte(scriptData), nil
}

// ReadCommitBody reads the body of a Commit or RdytoCommit request.
// Format: diswst(jobid)
func ReadCommitBody(r *Reader) (string, error) {
	return r.ReadString()
}

// ReadDeleteJobBody reads the body of a DeleteJob request.
// Uses Manage format: diswui(cmd) diswui(objtype) diswst(jobid) svrattrl(attrs)
func ReadDeleteJobBody(r *Reader) (string, error) {
	r.ReadUint() // cmd (ignored)
	r.ReadUint() // objtype (ignored)
	jobID, err := r.ReadString()
	if err != nil {
		return "", err
	}
	ReadSvrAttrl(r) // attrs (ignored for delete)
	return jobID, nil
}

// ReadSignalJobBody reads the body of a SignalJob request.
// Format: diswst(jobid) diswst(signal)
func ReadSignalJobBody(r *Reader) (jobID, signal string, err error) {
	jobID, err = r.ReadString()
	if err != nil {
		return "", "", err
	}
	signal, err = r.ReadString()
	if err != nil {
		return "", "", err
	}
	return jobID, signal, nil
}

// ReadStatusBody reads the body of a Status request (job, queue, node, server).
// Format: diswst(id) svrattrl(query_attrs)
func ReadStatusBody(r *Reader) (id string, queryAttrs []SvrAttrl, err error) {
	id, err = r.ReadString()
	if err != nil {
		return "", nil, err
	}
	queryAttrs, err = ReadSvrAttrl(r)
	if err != nil {
		return "", nil, err
	}
	return id, queryAttrs, nil
}

// ReadManagerBody reads the body of a Manager request (qmgr).
// Format: diswui(command) diswui(obj_type) diswst(obj_name) svrattrl(attrs)
func ReadManagerBody(r *Reader) (command, objType int, objName string, attrs []SvrAttrl, err error) {
	commandU, err := r.ReadUint()
	command = int(commandU)
	if err != nil {
		return 0, 0, "", nil, err
	}
	objTypeU, err := r.ReadUint()
	objType = int(objTypeU)
	if err != nil {
		return 0, 0, "", nil, err
	}
	objName, err = r.ReadString()
	if err != nil {
		return 0, 0, "", nil, err
	}
	attrs, err = ReadSvrAttrl(r)
	if err != nil {
		return 0, 0, "", nil, err
	}
	return command, objType, objName, attrs, nil
}

// ReadHoldJobBody reads the body of a HoldJob request.
// Uses Manage format: diswui(cmd) diswui(objtype) diswst(jobid) svrattrl(attrs)
func ReadHoldJobBody(r *Reader) (jobID string, attrs []SvrAttrl, err error) {
	r.ReadUint() // cmd
	r.ReadUint() // objtype
	jobID, err = r.ReadString()
	if err != nil {
		return "", nil, err
	}
	attrs, err = ReadSvrAttrl(r)
	if err != nil {
		return "", nil, err
	}
	return jobID, attrs, nil
}

// ReadModifyJobBody reads the body of a ModifyJob request.
// Uses Manage format: diswui(cmd) diswui(objtype) diswst(jobid) svrattrl(attrs)
func ReadModifyJobBody(r *Reader) (jobID string, attrs []SvrAttrl, err error) {
	r.ReadUint() // cmd
	r.ReadUint() // objtype
	jobID, err = r.ReadString()
	if err != nil {
		return "", nil, err
	}
	attrs, err = ReadSvrAttrl(r)
	if err != nil {
		return "", nil, err
	}
	return jobID, attrs, nil
}

// ReadRunJobBody reads the body of a RunJob request.
// Format: diswst(jobid) diswst(dest) diswui(resv_port)
func ReadRunJobBody(r *Reader) (jobID, dest string, err error) {
	jobID, err = r.ReadString()
	if err != nil {
		return "", "", err
	}
	dest, err = r.ReadString()
	if err != nil {
		return "", "", err
	}
	// resv_port may or may not be present depending on version
	return jobID, dest, nil
}

// ReadMoveJobBody reads the body of a MoveJob request.
// Format: diswst(jobid) diswst(destination)
func ReadMoveJobBody(r *Reader) (jobID, dest string, err error) {
	jobID, err = r.ReadString()
	if err != nil {
		return "", "", err
	}
	dest, err = r.ReadString()
	if err != nil {
		return "", "", err
	}
	return jobID, dest, nil
}

// ReadLocateJobBody reads the body of a LocateJob request.
// Format: diswst(jobid)
func ReadLocateJobBody(r *Reader) (string, error) {
	return r.ReadString()
}

// ReadJobCredBody reads the body of a JobCred request.
// Format: diswui(type) diswcs(cred_data)
func ReadJobCredBody(r *Reader) (credType int, credData string, err error) {
	credTypeU, err := r.ReadUint()
	credType = int(credTypeU)
	if err != nil {
		return 0, "", err
	}
	credData, err = r.ReadString()
	if err != nil {
		return 0, "", err
	}
	return credType, credData, nil
}

// ISMessage represents a decoded IS protocol message from a MOM.
type ISMessage struct {
	Version int
	Command int
	MomPort int
	RmPort  int
	Items   []string
}

// ReadISMessage reads an IS protocol message.
// The caller has already read the protocol type (4).
// Format: diswsi(version) diswsi(command) diswui(mom_port) diswui(rm_port)
//
//	diswst(items...) until "END_OF_LINE"
func ReadISMessage(r *Reader) (*ISMessage, error) {
	ver, err := r.ReadInt()
	if err != nil {
		return nil, fmt.Errorf("read IS version: %w", err)
	}
	cmd, err := r.ReadInt()
	if err != nil {
		return nil, fmt.Errorf("read IS command: %w", err)
	}
	momPort, err := r.ReadUint()
	if err != nil {
		return nil, fmt.Errorf("read IS mom_port: %w", err)
	}
	rmPort, err := r.ReadUint()
	if err != nil {
		return nil, fmt.Errorf("read IS rm_port: %w", err)
	}
	var items []string
	for {
		s, err := r.ReadString()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("read IS item: %w", err)
		}
		if s == "END_OF_LINE" {
			break
		}
		items = append(items, s)
	}
	return &ISMessage{
		Version: int(ver),
		Command: int(cmd),
		MomPort: int(momPort),
		RmPort:  int(rmPort),
		Items:   items,
	}, nil
}
