// PostgresStore is a Store backed by PostgreSQL. It is used for single or
// multi-master deployments: each server_state document (serverdb / nodes), each
// queue, and each job (attrs + script) is stored as a BYTEA blob keyed by
// name/id. PostgreSQL gives atomic per-document UPSERTs and crash durability via
// its WAL (fixing the original file store's no-fsync / non-atomic gaps), and -
// because it is a networked server DB - lets a master/standby pair share state
// for HA (TODO 5.1).
//
// Configure with the PBS_PG_DSN environment variable (a libpq-style URL, e.g.
// postgres://pbs:pbs@127.0.0.1:5432/pbs). When unset, the default file store is
// used.
package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/xinlaoda/opentorque/internal/config"
)

var _ Store = (*PostgresStore)(nil)

// PostgresStore implements Store over a pgx connection pool.
type PostgresStore struct {
	pool *pgxpool.Pool
}

// NewPostgresStore connects to PostgreSQL and ensures the schema exists.
func NewPostgresStore(dsn string) (*PostgresStore, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("pg store: parse/connect: %w", err)
	}
	if err := pgEnsureSchema(ctx, pool); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pg store: ensure schema: %w", err)
	}
	return &PostgresStore{pool: pool}, nil
}

// pgEnsureSchema creates the state tables if they do not exist yet.
func pgEnsureSchema(ctx context.Context, pool *pgxpool.Pool) error {
	const ddl = `
CREATE TABLE IF NOT EXISTS ot_state(
    kind TEXT PRIMARY KEY,
    data BYTEA NOT NULL
);
CREATE TABLE IF NOT EXISTS ot_queues(
    name TEXT PRIMARY KEY,
    data BYTEA NOT NULL
);
CREATE TABLE IF NOT EXISTS ot_jobs(
    id     TEXT PRIMARY KEY,
    attrs  BYTEA NOT NULL DEFAULT ''::bytea,
    script BYTEA NOT NULL DEFAULT ''::bytea
);
`
	_, err := pool.Exec(ctx, ddl)
	return err
}

// stateGet returns the single blob for a named ot_state row, or an error when
// it does not exist.
func (p *PostgresStore) stateGet(kind string) ([]byte, error) {
	var b []byte
	err := p.pool.QueryRow(context.Background(),
		"SELECT data FROM ot_state WHERE kind=$1", kind).Scan(&b)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// statePut upserts a named ot_state blob.
func (p *PostgresStore) statePut(kind string, data []byte) error {
	_, err := p.pool.Exec(context.Background(),
		`INSERT INTO ot_state(kind, data) VALUES($1,$2)
		 ON CONFLICT(kind) DO UPDATE SET data=EXCLUDED.data`, kind, data)
	return err
}

func (p *PostgresStore) LoadServerDB() ([]byte, error)  { return p.stateGet("serverdb") }
func (p *PostgresStore) SaveServerDB(data []byte) error { return p.statePut("serverdb", data) }

func (p *PostgresStore) LoadNodes() ([]byte, error)  { return p.stateGet("nodes") }
func (p *PostgresStore) SaveNodes(data []byte) error { return p.statePut("nodes", data) }

func (p *PostgresStore) LoadQueues() (map[string][]byte, error) {
	rows, err := p.pool.Query(context.Background(), "SELECT name, data FROM ot_queues")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string][]byte)
	for rows.Next() {
		var name string
		var b []byte
		if err := rows.Scan(&name, &b); err != nil {
			return nil, err
		}
		out[name] = b
	}
	return out, rows.Err()
}

func (p *PostgresStore) SaveQueue(name string, data []byte) error {
	_, err := p.pool.Exec(context.Background(),
		`INSERT INTO ot_queues(name, data) VALUES($1,$2)
		 ON CONFLICT(name) DO UPDATE SET data=EXCLUDED.data`, name, data)
	return err
}

func (p *PostgresStore) LoadJobs() (map[string][]byte, error) {
	rows, err := p.pool.Query(context.Background(), "SELECT id, attrs FROM ot_jobs")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string][]byte)
	for rows.Next() {
		var id string
		var b []byte
		if err := rows.Scan(&id, &b); err != nil {
			return nil, err
		}
		out[id] = b
	}
	return out, rows.Err()
}

func (p *PostgresStore) SaveJob(id string, attrs []byte) error {
	_, err := p.pool.Exec(context.Background(),
		`INSERT INTO ot_jobs(id, attrs) VALUES($1,$2)
		 ON CONFLICT(id) DO UPDATE SET attrs=EXCLUDED.attrs`, id, attrs)
	return err
}

func (p *PostgresStore) LoadJobScript(id string) ([]byte, error) {
	var b []byte
	err := p.pool.QueryRow(context.Background(),
		"SELECT script FROM ot_jobs WHERE id=$1", id).Scan(&b)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func (p *PostgresStore) SaveJobScript(id string, data []byte) error {
	_, err := p.pool.Exec(context.Background(),
		`INSERT INTO ot_jobs(id, script) VALUES($1,$2)
		 ON CONFLICT(id) DO UPDATE SET script=EXCLUDED.script`, id, data)
	return err
}

func (p *PostgresStore) DeleteJob(id string) error {
	_, err := p.pool.Exec(context.Background(),
		"DELETE FROM ot_jobs WHERE id=$1", id)
	return err
}

func (p *PostgresStore) Close() error {
	p.pool.Close()
	return nil
}

// GetPostgresStoreDSN returns the PostgreSQL DSN from PBS_PG_DSN, or "" when the
// file store should be used.
func GetPostgresStoreDSN() string {
	return os.Getenv("PBS_PG_DSN")
}

// isPostgresConfigured reports whether PBS_PG_DSN is set.
func isPostgresConfigured() bool {
	return GetPostgresStoreDSN() != ""
}

// MigrateFilesToPostgres performs a one-time, offline migration of the default
// file store (cfg paths) into the PostgreSQL store at dsn. It copies every
// server_state document, queue, and job (attrs + script) so an existing cluster
// can move to the PostgreSQL backend before switching PBS_PG_DSN on.
func MigrateFilesToPostgres(cfg *config.Config, dsn string) error {
	file := &FileStore{
		serverDB:  cfg.ServerDB,
		queuesDir: cfg.QueuesDir,
		nodesFile: cfg.NodesFile,
		jobsDir:   cfg.JobsDir,
	}
	ps, err := NewPostgresStore(dsn)
	if err != nil {
		return err
	}
	defer ps.Close()

	if b, err := file.LoadServerDB(); err == nil {
		if err := ps.SaveServerDB(b); err != nil {
			return err
		}
	}
	if b, err := file.LoadNodes(); err == nil {
		if err := ps.SaveNodes(b); err != nil {
			return err
		}
	}
	queues, err := file.LoadQueues()
	if err != nil {
		return err
	}
	for name, b := range queues {
		if err := ps.SaveQueue(name, b); err != nil {
			return err
		}
	}
	jobs, err := file.LoadJobs()
	if err != nil {
		return err
	}
	for id, b := range jobs {
		if err := ps.SaveJob(id, b); err != nil {
			return err
		}
		if sc, err := os.ReadFile(filepath.Join(cfg.JobsDir, id+".SC")); err == nil {
			if err := ps.SaveJobScript(id, sc); err != nil {
				return err
			}
		}
	}
	return nil
}
