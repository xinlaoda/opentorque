// Store is the persistence boundary for pbs_server's authoritative cluster
// state: server config + counters (serverdb), queues, nodes, and jobs (+ their
// scripts). The default backend is the local filesystem (FileStore). A remote
// PostgreSQL backend (PostgresStore) is planned so a master/standby pair can
// share state for HA (TODO 5.1); both implement this same interface, so the
// server never depends on where its state is stored.
//
// FileStore reproduces the exact on-disk layout and atomic (write-then-rename)
// behavior of the original file-based persistence, so switching today is a
// behavior-preserving refactor. Serialization/parsing live in the Server; this
// package handles raw byte persistence only.
package server

import (
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/xinlaoda/opentorque/internal/config"
)

// Store persists pbs_server state. LoadX returns an error when the data does
// not exist (recovery treats a missing store as an empty/initial state).
type Store interface {
	// server-level config + counters (single document)
	LoadServerDB() ([]byte, error)
	SaveServerDB(data []byte) error

	// queues: one document per queue, keyed by queue name
	LoadQueues() (map[string][]byte, error)
	SaveQueue(name string, data []byte) error

	// nodes: a single inventory document
	LoadNodes() ([]byte, error)
	SaveNodes(data []byte) error

	// jobs: one attributes document (.JB) plus one script document (.SC) per job
	LoadJobs() (map[string][]byte, error) // id -> attrs bytes
	LoadJobScript(id string) ([]byte, error)
	SaveJob(id string, attrs []byte) error
	SaveJobScript(id string, data []byte) error
	DeleteJob(id string) error

	Close() error
}

// FileStore is the default Store backed by server_priv files, preserving the
// original write-then-rename scheme.
type FileStore struct {
	serverDB  string
	queuesDir string
	nodesFile string
	jobsDir   string
}

// NewStore returns the configured state Store. When PBS_PG_DSN is set a
// PostgreSQL backend is used (single-master with a local DB, or shared for HA);
// otherwise the default file store is returned. If the PostgreSQL store is
// configured but cannot connect, it degrades to the file store with a clear
// warning so the server still starts.
func NewStore(cfg *config.Config) Store {
	if dsn := GetPostgresStoreDSN(); dsn != "" {
		ps, err := NewPostgresStore(dsn)
		if err == nil {
			log.Printf("[SERVER] Using PostgreSQL state store")
			return ps
		}
		log.Printf("[SERVER] ERROR: PBS_PG_DSN set but PostgreSQL store unavailable (%v); falling back to file store", err)
	}
	return &FileStore{
		serverDB:  cfg.ServerDB,
		queuesDir: cfg.QueuesDir,
		nodesFile: cfg.NodesFile,
		jobsDir:   cfg.JobsDir,
	}
}

func (f *FileStore) LoadServerDB() ([]byte, error) { return os.ReadFile(f.serverDB) }

func (f *FileStore) SaveServerDB(data []byte) error {
	tmp := f.serverDB + ".new"
	if err := os.WriteFile(tmp, data, 0640); err != nil {
		return err
	}
	return os.Rename(tmp, f.serverDB)
}

func (f *FileStore) LoadQueues() (map[string][]byte, error) {
	entries, err := os.ReadDir(f.queuesDir)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]byte, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if b, err := os.ReadFile(filepath.Join(f.queuesDir, e.Name())); err == nil {
			out[e.Name()] = b
		}
	}
	return out, nil
}

func (f *FileStore) SaveQueue(name string, data []byte) error {
	path := filepath.Join(f.queuesDir, name)
	tmp := path + ".new"
	if err := os.WriteFile(tmp, data, 0640); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (f *FileStore) LoadNodes() ([]byte, error) { return os.ReadFile(f.nodesFile) }

func (f *FileStore) SaveNodes(data []byte) error {
	tmp := f.nodesFile + ".new"
	if err := os.WriteFile(tmp, data, 0640); err != nil {
		return err
	}
	return os.Rename(tmp, f.nodesFile)
}

func (f *FileStore) LoadJobs() (map[string][]byte, error) {
	entries, err := os.ReadDir(f.jobsDir)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]byte)
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".JB") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".JB")
		if b, err := os.ReadFile(filepath.Join(f.jobsDir, e.Name())); err == nil {
			out[id] = b
		}
	}
	return out, nil
}

func (f *FileStore) SaveJob(id string, attrs []byte) error {
	path := filepath.Join(f.jobsDir, id+".JB")
	tmp := path + ".new"
	if err := os.WriteFile(tmp, attrs, 0640); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (f *FileStore) LoadJobScript(id string) ([]byte, error) {
	return os.ReadFile(filepath.Join(f.jobsDir, id+".SC"))
}

func (f *FileStore) SaveJobScript(id string, data []byte) error {
	return os.WriteFile(filepath.Join(f.jobsDir, id+".SC"), data, 0700)
}

func (f *FileStore) DeleteJob(id string) error {
	os.Remove(filepath.Join(f.jobsDir, id+".JB"))
	os.Remove(filepath.Join(f.jobsDir, id+".SC"))
	return nil
}

func (f *FileStore) Close() error { return nil }
