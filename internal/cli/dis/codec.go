package dis

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
)

// DIS protocol constants
const (
	BatchProtType = 2
	BatchProtVer  = 2
)

// Batch Request Types
const (
	BatchReqConnect      = 0
	BatchReqQueueJob     = 1
	BatchReqJobCred      = 2
	BatchReqJobScript    = 3
	BatchReqRdytoCommit  = 4
	BatchReqCommit       = 5
	BatchReqDeleteJob    = 6
	BatchReqHoldJob      = 7
	BatchReqLocateJob    = 8
	BatchReqManager      = 9
	BatchReqMessJob      = 10
	BatchReqModifyJob    = 11
	BatchReqMoveJob      = 12
	BatchReqReleaseJob   = 13
	BatchReqRerun        = 14
	BatchReqRunJob       = 15
	BatchReqSelectJobs   = 16
	BatchReqShutdown     = 17
	BatchReqSignalJob    = 18
	BatchReqStatusJob    = 19
	BatchReqStatusQueue  = 20
	BatchReqStatusServer = 21
	BatchReqQueueJob2    = 29
	BatchReqCheckpointJob = 27
	BatchReqCommit2      = 30
	BatchReqJobScript2   = 31
	BatchReqAuthenUser   = 49
	BatchReqOrderJob     = 50
	BatchReqStatusNode   = 58
	BatchReqDisconnect   = 59
	BatchReqAuthToken    = 63
)

// Reply choice types
const (
	ReplyChoiceNull     = 1
	ReplyChoiceQueue    = 2
	ReplyChoiceRdytoCom = 3
	ReplyChoiceCommit   = 4
	ReplyChoiceSelect   = 5
	ReplyChoiceStatus   = 6
	ReplyChoiceText     = 7
	ReplyChoiceLocate   = 8
)

// Manager command types
const (
	MgrCmdCreate = 1
	MgrCmdDelete = 2
	MgrCmdSet    = 3
	MgrCmdUnset  = 4
	MgrCmdList   = 5
	MgrCmdPrint  = 6
)

// Manager object types
const (
	MgrObjServer = 1
	MgrObjQueue  = 2
	MgrObjJob    = 3
	MgrObjNode   = 4
)

// Reader reads DIS-encoded data from a stream.
type Reader struct {
	r *bufio.Reader
}

func NewReader(r io.Reader) *Reader {
	return &Reader{r: bufio.NewReader(r)}
}

func (r *Reader) disrsi(count int) (int64, bool, error) {
	c, err := r.r.ReadByte()
	if err != nil {
		return 0, false, err
	}
	switch {
	case c == '+' || c == '-':
		negate := c == '-'
		buf := make([]byte, count)
		_, err = io.ReadFull(r.r, buf)
		if err != nil {
			return 0, false, err
		}
		val, err := strconv.ParseInt(string(buf), 10, 64)
		if err != nil {
			return 0, false, fmt.Errorf("dis: parse digits %q: %w", buf, err)
		}
		return val, negate, nil
	case c >= '1' && c <= '9':
		ndigs := int(c - '0')
		if count > 1 {
			buf := make([]byte, count-1)
			_, err = io.ReadFull(r.r, buf)
			if err != nil {
				return 0, false, err
			}
			for _, b := range buf {
				ndigs = 10*ndigs + int(b-'0')
			}
		}
		return r.disrsi(ndigs)
	case c == '0':
		return 0, false, fmt.Errorf("dis: leading zero in count")
	default:
		return 0, false, fmt.Errorf("dis: unexpected byte 0x%02x", c)
	}
}

func (r *Reader) ReadUint() (uint64, error) {
	val, negate, err := r.disrsi(1)
	if err != nil {
		return 0, err
	}
	if negate {
		return 0, fmt.Errorf("dis: ReadUint: unexpected negative")
	}
	return uint64(val), nil
}

func (r *Reader) ReadInt() (int64, error) {
	val, negate, err := r.disrsi(1)
	if err != nil {
		return 0, err
	}
	if negate {
		return -val, nil
	}
	return val, nil
}

func (r *Reader) ReadString() (string, error) {
	length, err := r.ReadUint()
	if err != nil {
		return "", err
	}
	if length == 0 {
		return "", nil
	}
	buf := make([]byte, length)
	_, err = io.ReadFull(r.r, buf)
	if err != nil {
		return "", err
	}
	return string(buf), nil
}

// Writer writes DIS-encoded data to a stream.
type Writer struct {
	w *bufio.Writer
}

func NewWriter(w io.Writer) *Writer {
	return &Writer{w: bufio.NewWriter(w)}
}

func (w *Writer) WriteUint(val uint64) error {
	digits := strconv.FormatUint(val, 10)
	ndigs := len(digits)
	prefix := "+"
	for ndigs > 1 {
		countStr := strconv.Itoa(ndigs)
		prefix = countStr + prefix
		ndigs = len(countStr)
	}
	if _, err := w.w.WriteString(prefix); err != nil {
		return err
	}
	_, err := w.w.WriteString(digits)
	return err
}

func (w *Writer) WriteInt(val int64) error {
	sign := "+"
	if val < 0 {
		sign = "-"
		val = -val
	}
	digits := strconv.FormatInt(val, 10)
	ndigs := len(digits)
	prefix := sign
	for ndigs > 1 {
		countStr := strconv.Itoa(ndigs)
		prefix = countStr + prefix
		ndigs = len(countStr)
	}
	if _, err := w.w.WriteString(prefix); err != nil {
		return err
	}
	_, err := w.w.WriteString(digits)
	return err
}

func (w *Writer) WriteString(s string) error {
	if err := w.WriteUint(uint64(len(s))); err != nil {
		return err
	}
	if len(s) > 0 {
		_, err := w.w.WriteString(s)
		return err
	}
	return nil
}

func (w *Writer) Flush() error {
	return w.w.Flush()
}

// SvrAttrl represents a single attribute in the svrattrl list format.
type SvrAttrl struct {
	Name    string
	HasResc bool
	Resc    string
	Value   string
	Op      int
}

// WriteSvrAttrl writes an svrattrl list to the DIS stream.
func WriteSvrAttrl(w *Writer, attrs []SvrAttrl) error {
	if err := w.WriteUint(uint64(len(attrs))); err != nil {
		return err
	}
	for _, a := range attrs {
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

// ReadSvrAttrl reads an svrattrl list from the DIS stream.
func ReadSvrAttrl(r *Reader) ([]SvrAttrl, error) {
	count, err := r.ReadUint()
	if err != nil {
		return nil, err
	}
	attrs := make([]SvrAttrl, 0, count)
	for i := uint64(0); i < count; i++ {
		if _, err := r.ReadUint(); err != nil { // size (unused)
			return nil, err
		}
		name, err := r.ReadString()
		if err != nil {
			return nil, err
		}
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
		value, err := r.ReadString()
		if err != nil {
			return nil, err
		}
		op, err := r.ReadUint()
		if err != nil {
			return nil, err
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
