package dis

import (
"bufio"
"fmt"
"io"
"log"
)

// LogReader wraps an io.Reader and logs all bytes read.
type LogReader struct {
r     io.Reader
label string
buf   []byte
}

func NewLogReader(r io.Reader, label string) *LogReader {
return &LogReader{r: r, label: label}
}

func (lr *LogReader) Read(p []byte) (int, error) {
n, err := lr.r.Read(p)
if n > 0 {
lr.buf = append(lr.buf, p[:n]...)
log.Printf("[WIRE:%s] read %d bytes: %q", lr.label, n, string(p[:n]))
}
if err != nil && err != io.EOF {
log.Printf("[WIRE:%s] read error: %v", lr.label, err)
}
return n, err
}

// LogWriter wraps an io.Writer and logs all bytes written.
type LogWriter struct {
w     io.Writer
label string
}

func NewLogWriter(w io.Writer, label string) *LogWriter {
return &LogWriter{w: w, label: label}
}

func (lw *LogWriter) Write(p []byte) (int, error) {
n, err := lw.w.Write(p)
if n > 0 {
log.Printf("[WIRE:%s] write %d bytes: %q", lw.label, n, string(p[:n]))
}
if err != nil {
log.Printf("[WIRE:%s] write error: %v", lw.label, err)
}
return n, err
}

// NewLoggingReader creates a DIS Reader that logs all reads.
func NewLoggingReader(r io.Reader, label string) *Reader {
return &Reader{r: bufio.NewReader(NewLogReader(r, label))}
}

// NewLoggingWriter creates a DIS Writer that logs all writes.
func NewLoggingWriter(w io.Writer, label string) *Writer {
return &Writer{w: bufio.NewWriter(NewLogWriter(w, label))}
}

func init() {
_ = fmt.Sprintf("") // avoid unused import
}
