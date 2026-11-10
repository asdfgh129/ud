// Package store is the single source of truth for tasks, resources and chunks.
// It implements docs/06 §3 DDL v1, §5 crash recovery and ADR-0015 invariants.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"sync"
	"time"

	_ "modernc.org/sqlite" // pure Go SQLite driver: CGO_ENABLED=0 friendly

	"github.com/asdfgh129/ud/internal/core"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Store wraps one SQLite database. All writes are serialized by writeMu so the
// "single writer" rule of docs/06 §7 holds even with a connection pool.
type Store struct {
	db      *sql.DB
	path    string
	writeMu sync.Mutex
	bus     *core.EventBus
	now     func() int64
}

func Open(path string, bus *core.EventBus) (*Store, error) {
	if path == "" {
		path = "ud.db"
	}
	if bus == nil {
		bus = core.NewEventBus(256)
	}
	// _pragma applies to EVERY pooled connection, so only per-connection
	// settings belong here (foreign_keys / busy_timeout / synchronous).
	// journal_mode(WAL) is a database-file-level persistent setting and needs
	// exclusive access: re-issuing it on each lazily opened connection made
	// concurrent Pause/Cancel calls fail with "database is locked (SQLITE_BUSY)".
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)" +
		"&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// Set WAL exactly once, on the only connection in the pool, before any
	// concurrent access can start.
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(context.Background(), `PRAGMA journal_mode(WAL)`); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable WAL: %w", err)
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	s := &Store{db: db, path: path, bus: bus, now: func() int64 { return time.Now().UnixMilli() }}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && len(e.Name()) > 4 && e.Name()[len(e.Name())-4:] == ".sql" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	for _, name := range names {
		body, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		if _, err := s.db.ExecContext(context.Background(), string(body)); err != nil {
			return fmt.Errorf("migration %s: %w", name, err)
		}
	}
	version := fmt.Sprint(len(names))
	_, err = s.db.ExecContext(context.Background(),
		`INSERT INTO app_meta(key,value) VALUES('schema_version',?) 
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value`, version)
	if err != nil {
		return fmt.Errorf("set schema_version: %w", err)
	}
	_, _ = s.db.ExecContext(context.Background(),
		`INSERT OR IGNORE INTO app_meta(key,value) VALUES('created_at',?)`, fmt.Sprint(time.Now().UnixMilli()))
	return nil
}

// withTx serializes every write and rolls back on error. State transitions and
// chunk progress MUST share one transaction (docs/05 §4 invariant ②).
func (s *Store) withTx(ctx context.Context, fn func(*sql.Tx) error) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) Bus() *core.EventBus { return s.bus }

func (s *Store) Now() int64 { return s.now() }

func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var v string
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM app_meta WHERE key='schema_version'`).Scan(&v); err != nil {
		return 0, err
	}
	var n int
	_, err := fmt.Sscan(v, &n)
	return n, err
}

func (s *Store) Meta(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM app_meta WHERE key=?`, key).Scan(&v)
	return v, err
}

func (s *Store) SetMeta(ctx context.Context, key, value string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO app_meta(key,value) VALUES(?,?) 
			ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
		return err
	})
}

// Backup produces a consistent single-file copy after a WAL checkpoint
// (docs/06 §9 / udctl backup).
func (s *Store) Backup(ctx context.Context, dest string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, dest)
	return err
}

func (s *Store) Close() error { return s.db.Close() }
