package dis

import (
	"fmt"
	"io"
	"net"
)

// RequestHeader represents a decoded DIS batch request header.
type RequestHeader struct {
	Protocol int
	Version  int
	ReqType  int
	User     string
}

// ReadRequestHeader reads a batch request header from the connection.
// Wire format: diswui(proto_type) diswui(proto_ver) diswui(req_type) diswst(user)
func ReadRequestHeader(r *Reader) (*RequestHeader, error) {
	protocol, err := r.ReadUint()
	if err != nil {
		return nil, fmt.Errorf("dis: reading protocol type: %w", err)
	}

	version, err := r.ReadUint()
	if err != nil {
		return nil, fmt.Errorf("dis: reading protocol version: %w", err)
	}

	reqType, err := r.ReadUint()
	if err != nil {
		return nil, fmt.Errorf("dis: reading request type: %w", err)
	}

	user, err := r.ReadString()
	if err != nil {
		return nil, fmt.Errorf("dis: reading user: %w", err)
	}

	return &RequestHeader{
		Protocol: int(protocol),
		Version:  int(version),
		ReqType:  int(reqType),
		User:     user,
	}, nil
}

// WriteReply writes a batch reply matching the C encode_DIS_reply() format:
//   diswui(PBS_BATCH_PROT_TYPE)
//   diswui(PBS_BATCH_PROT_VER)
//   diswsi(brp_code)       // SIGNED int
//   diswsi(brp_auxcode)    // SIGNED int
//   diswui(brp_choice)     // unsigned
//   [choice-specific data]
func WriteReply(w *Writer, code, auxCode, choice int, data string) error {
	if err := w.WriteUint(BatchProtType); err != nil {
		return err
	}
	if err := w.WriteUint(BatchProtVer); err != nil {
		return err
	}
	if err := w.WriteInt(int64(code)); err != nil {
		return err
	}
	if err := w.WriteInt(int64(auxCode)); err != nil {
		return err
	}
	if err := w.WriteUint(uint64(choice)); err != nil {
		return err
	}

	// Choice-specific data (must match C encode_DIS_reply exactly)
	switch choice {
	case ReplyChoiceNull:
		// NULL: no extra data written (just break in C code)
	case ReplyChoiceQueue, ReplyChoiceRdytoCom, ReplyChoiceCommit:
		// diswst(chan, brp_jid) - write job ID string
		if err := w.WriteString(data); err != nil {
			return err
		}
	case ReplyChoiceText:
		// diswcs(chan, str, len) - write counted string (same wire format as diswst)
		if err := w.WriteString(data); err != nil {
			return err
		}
	}

	return w.Flush()
}

// SendErrorReply sends an error reply to a connection.
func SendErrorReply(conn net.Conn, errCode, auxCode int) error {
	w := NewWriter(conn)
	return WriteReply(w, errCode, auxCode, ReplyChoiceNull, "")
}

// SendOkReply sends a success reply with no data.
func SendOkReply(conn net.Conn) error {
	w := NewWriter(conn)
	return WriteReply(w, PbsErrNone, 0, ReplyChoiceNull, "")
}

// SendJobIDReply sends a success reply with a job ID.
func SendJobIDReply(conn net.Conn, choice int, jobID string) error {
	w := NewWriter(conn)
	return WriteReply(w, PbsErrNone, 0, choice, jobID)
}

// SendTextReply sends a success reply with text data.
func SendTextReply(conn net.Conn, text string) error {
	w := NewWriter(conn)
	return WriteReply(w, PbsErrNone, 0, ReplyChoiceText, text)
}

// WriteRequestHeader writes a batch request header for outgoing requests (e.g., to server).
func WriteRequestHeader(w *Writer, reqType int, user string) error {
	if err := w.WriteUint(BatchProtType); err != nil {
		return err
	}
	if err := w.WriteUint(BatchProtVer); err != nil {
		return err
	}
	if err := w.WriteUint(uint64(reqType)); err != nil {
		return err
	}
	if err := w.WriteString(user); err != nil {
		return err
	}
	return nil
}

// SkipString reads and discards a DIS string.
func SkipString(r *Reader) error {
	_, err := r.ReadString()
	return err
}

// SkipUint reads and discards a DIS unsigned int.
func SkipUint(r *Reader) error {
	_, err := r.ReadUint()
	return err
}

// ReadRawBytes reads n raw bytes from the reader.
func (r *Reader) ReadRawBytes(n int) ([]byte, error) {
	buf := make([]byte, n)
	_, err := io.ReadFull(r.r, buf)
	return buf, err
}

// SkipSvrAttrl skips a DIS-encoded attribute list.
// Format: count (uint), then for each: size(uint) name(str) hasresc(uint) [resc(str)] value(str) op(uint)
func SkipSvrAttrl(r *Reader) error {
	count, err := r.ReadUint()
	if err != nil {
		return fmt.Errorf("dis: skip attrl count: %w", err)
	}

	for i := uint64(0); i < count; i++ {
		// Read the attribute
		if _, err := r.ReadUint(); err != nil { // size
			return err
		}
		if _, err := r.ReadString(); err != nil { // name
			return err
		}
		hasResc, err := r.ReadUint()
		if err != nil {
			return err
		}
		if hasResc != 0 {
			if _, err := r.ReadString(); err != nil { // resource name
				return err
			}
		}
		if _, err := r.ReadString(); err != nil { // value
			return err
		}
		if _, err := r.ReadUint(); err != nil { // op
			return err
		}
	}
	return nil
}
