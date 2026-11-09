// Package app assembles the daemon from all internal packages and owns its
// lifecycle. Both `udd` and `udctl daemon` call Run (docs/07 §3).
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/asdfgh129/ud/internal/api"
	"github.com/asdfgh129/ud/internal/config"
	"github.com/asdfgh129/ud/internal/core"
	"github.com/asdfgh129/ud/internal/postproc"
	"github.com/asdfgh129/ud/internal/resolve"
	"github.com/asdfgh129/ud/internal/scheduler"
	"github.com/asdfgh129/ud/internal/security"
	"github.com/asdfgh129/ud/internal/store"
	"github.com/asdfgh129/ud/internal/transport"
	httptransport "github.com/asdfgh129/ud/internal/transport/http"
	"github.com/asdfgh129/ud/internal/version"
)

type Options struct {
	ConfigPath string
	Listen     string
	Dir        string
	DBPath     string
	LogLevel   string
	// DiscoveryOverride redirects the local discovery file (used by tests so
	// they never touch the user's real config directory).
	DiscoveryOverride string
}

// Discovery is the local discovery file written when listen uses port 0
// (ADR-0016: never occupy a fixed 6800 port).
type Discovery struct {
	Addr      string `json:"addr"`
	PID       int    `json:"pid"`
	Version   string `json:"version"`
	StartedAt int64  `json:"startedAt"`
}

func DiscoveryPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "ud", "daemon.json")
}

func ReadDiscovery() (Discovery, error) {
	b, err := os.ReadFile(DiscoveryPath())
	if err != nil {
		return Discovery{}, err
	}
	var d Discovery
	if err := json.Unmarshal(b, &d); err != nil {
		return Discovery{}, err
	}
	return d, nil
}

// Run blocks until ctx is cancelled, then shuts down in order.
func Run(ctx context.Context, opts Options) error {
	cfgPath := opts.ConfigPath
	if cfgPath == "" {
		if _, err := os.Stat("config.yaml"); err == nil {
			cfgPath = "config.yaml"
		}
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	if opts.Listen != "" {
		cfg.Listen = opts.Listen
	}
	if opts.Dir != "" {
		cfg.DownloadDir = config.ExpandHome(opts.Dir)
	}
	if opts.LogLevel != "" {
		cfg.LogLevel = opts.LogLevel
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}
	log := newLogger(cfg.LogLevel)

	dbPath := opts.DBPath
	if dbPath == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			dir = os.TempDir()
		}
		dbPath = filepath.Join(dir, "ud", "state.db")
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.DownloadDir, 0o755); err != nil {
		return err
	}

	bus := core.NewEventBus(512)
	st, err := store.Open(dbPath, bus)
	if err != nil {
		return err
	}
	defer st.Close()
	guard := security.NewSSRFGuard(cfg.Security)
	mgr := config.NewManager(cfg, nil)
	creds := security.NewMemoryCredentialStore()

	resolvers := resolve.NewRegistry()
	resolvers.Register(resolve.NewDirectHTTP(guard))
	resolvers.Register(resolve.Unsupported{})
	transports := transport.NewRegistry()
	transports.Register(httptransport.New(guard))
	postprocs := postproc.NewRegistry()

	sch, err := scheduler.New(scheduler.Options{
		Store: st, Resolvers: resolvers, Transports: transports,
		PostProcessors: postprocs, Guard: guard, Credentials: creds,
		Config: mgr, Bus: bus, Logger: log,
	})
	if err != nil {
		return err
	}
	defer sch.Close()
	recovered, err := sch.Recover(ctx)
	if err != nil {
		return err
	}

	loopback := isLoopbackListen(cfg.Listen)
	srv := api.New(api.Options{
		Control: sch, Config: mgr, Credentials: creds, Bus: bus, Logger: log,
		// Fail closed: any non-loopback listener requires a Bearer token.
		// config.Validate already rejects the token-less case at startup.
		AuthToken: cfg.RemoteToken, RequireAuth: !loopback,
	})
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("listen %s: %w", cfg.Listen, err)
	}
	addr := ln.Addr().String()
	dpath := opts.DiscoveryOverride
	if dpath == "" {
		dpath = DiscoveryPath()
	}
	if err := os.MkdirAll(filepath.Dir(dpath), 0o700); err == nil {
		b, _ := json.Marshal(Discovery{Addr: addr, PID: os.Getpid(),
			Version: version.String(), StartedAt: time.Now().UnixMilli()})
		_ = os.WriteFile(dpath, b, 0o600)
	}
	defer os.Remove(dpath)

	httpSrv := &http.Server{Handler: srv, ReadHeaderTimeout: 10 * time.Second}
	serveErr := make(chan error, 1)
	go func() {
		if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()
	log.Info("daemon ready", "addr", addr, "version", version.String(),
		"db", dbPath, "recoveredTasks", recovered, "discovery", dpath)

	select {
	case <-ctx.Done():
	case err := <-serveErr:
		return err
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Warn("http shutdown", "error", err)
	}
	log.Info("daemon stopped")
	return nil
}

func newLogger(level string) *slog.Logger {
	var l slog.Level
	switch strings.ToLower(level) {
	case "debug":
		l = slog.LevelDebug
	case "warn":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: l}))
}

func isLoopbackListen(listen string) bool {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return true
	}
	return host == "" || host == "127.0.0.1" || host == "localhost" || host == "::1"
}
