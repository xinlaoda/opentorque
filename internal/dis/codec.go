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

	IMProtocol    = 3
	IMProtocolVer = 2
)

// DIS error codes
const (
	Success  = 0
	Overflow = 1
	EOD      = 7
	NoMalloc = 8
	EOF      = 11
)

// Reader reads DIS-encoded data from a stream.
//
// DIS number encoding (matching the C implementation):
//   - A number is encoded as: [recursive_count_chain] sign digits
//   - sign is '+' for non-negative, '-' for negative
//   - digits are the decimal representation
//   - The count chain encodes how many digits follow:
//     - If 1 digit: no count prefix needed (sign directly precedes the digit)
//     - If N digits (N>1): prefix with N, which itself may need a count chain
//   - Example: 5 -> "+5", 15 -> "2+15", 1234567890 -> "210+1234567890"
//
// DIS string encoding: the length is encoded as an unsigned int (above), followed by raw bytes.
type Reader struct {
	r *bufio.Reader
}

func NewReader(r io.Reader) *Reader {
	return &Reader{r: bufio.NewReader(r)}
}

// disrsi reads a DIS-encoded signed integer with a given expected digit count.
// This matches the C function disrsi_() exactly.
func (r *Reader) disrsi(count int) (int64, bool, error) {
	c, err := r.r.ReadByte()
	if err != nil {
		return 0, false, err
	}

	switch {
	case c == '+' || c == '-':
		// Sign character: read 'count' digits as the value
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
		// Count digit: build the count of digits for the next recursion
		ndigs := int(c - '0')
		if count > 1 {
			// Read remaining count digits
			buf := make([]byte, count-1)
			_, err = io.ReadFull(r.r, buf)
			if err != nil {
				return 0, false, err
			}
			for _, b := range buf {
				if b < '0' || b > '9' {
					return 0, false, fmt.Errorf("dis: non-digit %q in count", b)
				}
				ndigs = 10*ndigs + int(b-'0')
			}
		}
		// Recurse with the decoded count
		return r.disrsi(ndigs)

	case c == '0':
		return 0, false, fmt.Errorf("dis: leading zero in count")

	default:
		return 0, false, fmt.Errorf("dis: unexpected byte 0x%02x", c)
	}
}

// ReadUint reads an unsigned integer in DIS format.
func (r *Reader) ReadUint() (uint64, error) {
	val, negate, err := r.disrsi(1)
	if err != nil {
		return 0, fmt.Errorf("dis: ReadUint: %w", err)
	}
	if negate {
		return 0, fmt.Errorf("dis: ReadUint: unexpected negative")
	}
	return uint64(val), nil
}

// ReadInt reads a signed integer in DIS format.
func (r *Reader) ReadInt() (int64, error) {
	val, negate, err := r.disrsi(1)
	if err != nil {
		return 0, fmt.Errorf("dis: ReadInt: %w", err)
	}
	if negate {
		return -val, nil
	}
	return val, nil
}

// ReadString reads a DIS-encoded string.
// The length is encoded as a DIS unsigned int, followed by the raw string bytes.
func (r *Reader) ReadString() (string, error) {
	length, err := r.ReadUint()
	if err != nil {
		return "", fmt.Errorf("dis: ReadString length: %w", err)
	}
	if length == 0 {
		return "", nil
	}
	buf := make([]byte, length)
	_, err = io.ReadFull(r.r, buf)
	if err != nil {
		return "", fmt.Errorf("dis: ReadString data: %w", err)
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

// WriteUint writes an unsigned integer in DIS format.
// Format: [recursive_count_chain] '+' digits
// Matches the C diswui_() function exactly.
func (w *Writer) WriteUint(val uint64) error {
	digits := strconv.FormatUint(val, 10)
	ndigs := len(digits)

	// Build the prefix: recursive count chain + sign
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

// WriteInt writes a signed integer in DIS format.
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

// WriteString writes a DIS-encoded string.
// The length is written as a DIS unsigned int, followed by the raw string bytes.
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

// Flush flushes the write buffer.
func (w *Writer) Flush() error {
	return w.w.Flush()
}

// WriteRawBytes writes raw bytes.
func (w *Writer) WriteRawBytes(data []byte) error {
	_, err := w.w.Write(data)
	return err
}

// Buffered returns the number of buffered bytes available for reading.
func (r *Reader) Buffered() int {
	return r.r.Buffered()
}
