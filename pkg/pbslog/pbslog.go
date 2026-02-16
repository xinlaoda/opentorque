// Package pbslog provides TORQUE-compatible dated log files.
// Log files are named YYYYMMDD and stored in the specified directory.
// The file automatically rotates at midnight UTC.
package pbslog

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// DatedLog writes to YYYYMMDD-named files in a directory, rotating daily.
type DatedLog struct {
	dir     string
	mu      sync.Mutex
	curDate string
	file    *os.File
}

// New creates a DatedLog that writes into dir using YYYYMMDD filenames.
// The directory is created if it does not exist.
func New(dir string) (*DatedLog, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("pbslog: mkdir %s: %w", dir, err)
	}
	dl := &DatedLog{dir: dir}
	if err := dl.rotate(); err != nil {
		return nil, err
	}
	return dl, nil
}

// Write implements io.Writer; it checks the date on each write and
// rotates the file if the day has changed.
func (dl *DatedLog) Write(p []byte) (int, error) {
	dl.mu.Lock()
	defer dl.mu.Unlock()

	today := time.Now().Format("20060102")
	if today != dl.curDate {
		if err := dl.rotateLocked(today); err != nil {
			return 0, err
		}
	}
	return dl.file.Write(p)
}

// Close closes the current log file.
func (dl *DatedLog) Close() error {
	dl.mu.Lock()
	defer dl.mu.Unlock()
	if dl.file != nil {
		return dl.file.Close()
	}
	return nil
}

func (dl *DatedLog) rotate() error {
	dl.mu.Lock()
	defer dl.mu.Unlock()
	return dl.rotateLocked(time.Now().Format("20060102"))
}

func (dl *DatedLog) rotateLocked(date string) error {
	if dl.file != nil {
		dl.file.Close()
	}
	path := filepath.Join(dl.dir, date)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("pbslog: open %s: %w", path, err)
	}
	dl.file = f
	dl.curDate = date
	return nil
}

// Setup configures the standard logger to write to YYYYMMDD files in logDir.
// If debug is true, output also goes to stderr (tee mode).
// Returns the DatedLog so the caller can Close() it on shutdown.
func Setup(logDir string, debug bool) (*DatedLog, error) {
	dl, err := New(logDir)
	if err != nil {
		return nil, err
	}
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	if debug {
		log.SetOutput(io.MultiWriter(os.Stderr, dl))
	} else {
		log.SetOutput(dl)
	}
	return dl, nil
}
