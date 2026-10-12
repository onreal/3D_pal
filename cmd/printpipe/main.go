// Command printpipe runs the print pipeline: MCP over stdio for the agent and HTTP on
// :8080 for the viewer and settings page, in one process.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/margarit/printpipe/internal/config"
	"github.com/margarit/printpipe/internal/events"
	"github.com/margarit/printpipe/internal/jobs"
	"github.com/margarit/printpipe/internal/mcpserver"
	"github.com/margarit/printpipe/internal/pipeline"
	"github.com/margarit/printpipe/internal/project"
	"github.com/margarit/printpipe/internal/runner"
	"github.com/margarit/printpipe/internal/scad"
	"github.com/margarit/printpipe/internal/slicer"
	"github.com/margarit/printpipe/internal/store"
	"github.com/margarit/printpipe/internal/web"

	// Printer drivers and slicers register themselves.
	_ "github.com/margarit/printpipe/internal/printers/bambu"
	_ "github.com/margarit/printpipe/internal/printers/fake"
	_ "github.com/margarit/printpipe/internal/slicers/bambustudio"
	_ "github.com/margarit/printpipe/internal/slicers/fake"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "printpipe:", err)
		os.Exit(1)
	}
}

func run() error {
	data := flag.String("data", envOr("PRINTPIPE_DATA", "/data"), "settings, database, profiles and logs")
	work := flag.String("work", envOr("PRINTPIPE_WORK", "/work"), "workspace: one folder per part")
	addr := flag.String("addr", envOr("PRINTPIPE_ADDR", ":8080"), "HTTP listen address")
	viewerURL := flag.String("viewer-url", envOr("PRINTPIPE_VIEWER_URL", "http://localhost:8080"), "URL the user opens, for messages to the agent")
	useMCP := flag.Bool("mcp", true, "serve MCP on stdin/stdout (off: HTTP only, for development)")
	demo := flag.Bool("demo", os.Getenv("PRINTPIPE_DEMO") == "1", "add a simulated printer and slicer when no printer is configured")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return nil
	}

	for _, d := range []string{*data, filepath.Join(*data, "logs"), filepath.Join(*data, "profiles"), *work} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	// stdout belongs to MCP: everything else goes to the log file (and stderr when not in MCP mode).
	logFile, err := os.OpenFile(filepath.Join(*data, "logs", "printpipe.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer logFile.Close()
	var logOut io.Writer = logFile
	if !*useMCP {
		logOut = io.MultiWriter(logFile, os.Stderr)
	}
	level := new(slog.LevelVar)
	logger := slog.New(slog.NewTextHandler(logOut, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	db, err := store.Open(filepath.Join(*data, "printpipe.db"))
	if err != nil {
		return err
	}
	defer db.Close()

	cfg := config.NewStore(db, filepath.Join(*data, "profiles"))
	cfg.OnSave = func(s config.Settings) { setLevel(level, s.Pipeline.LogLevel) }
	set, err := cfg.Load()
	if err != nil {
		return err
	}
	setLevel(level, set.Pipeline.LogLevel)
	if *demo && len(set.Printers) == 0 {
		set.Printers = []config.Printer{{ID: "sim", Name: "Simulated printer", Kind: "fake", Default: true,
			Config: map[string]string{"print_seconds": "120", "slicer": "fake"}}}
		if err := cfg.Save(set); err != nil {
			return fmt.Errorf("demo printer: %w", err)
		}
		logger.Info("demo mode: added a simulated printer")
	}
	workDir := set.Pipeline.Workspace
	if workDir == "" || workDir == "/work" {
		workDir = *work
	}

	exec := runner.Exec{}
	hub := events.NewHub()
	svc := pipeline.New(pipeline.Deps{
		DB:        db,
		Runner:    exec,
		Config:    cfg,
		Jobs:      jobs.NewStore(db),
		Projects:  project.NewStore(db, workDir),
		SCAD:      scad.New(exec, envOr("OPENSCAD", "openscad")),
		Slicers:   slicer.Registered(slicer.Deps{Runner: exec}),
		Events:    hub,
		Logger:    logger,
		ViewerURL: *viewerURL,
	})
	defer svc.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := &http.Server{Addr: *addr, Handler: web.Handler(svc, cfg, hub, nil), ReadHeaderTimeout: 10 * time.Second}
	httpErr := make(chan error, 1)
	go func() {
		logger.Info("http listening", "addr", *addr, "version", version)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			// Another session may hold the port; MCP keeps working without the viewer.
			logger.Error("http server stopped", "err", err)
			httpErr <- err
		}
	}()

	if *useMCP {
		logger.Info("mcp serving on stdio")
		err = mcpserver.New(svc, version).Run(ctx, &mcp.StdioTransport{})
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, io.EOF) {
			logger.Error("mcp stopped", "err", err)
		}
	} else {
		select {
		case <-ctx.Done():
		case err = <-httpErr:
			return err
		}
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(shutdown)
	logger.Info("stopped")
	return nil
}

func setLevel(v *slog.LevelVar, name string) {
	switch name {
	case "debug":
		v.Set(slog.LevelDebug)
	case "warn":
		v.Set(slog.LevelWarn)
	case "error":
		v.Set(slog.LevelError)
	default:
		v.Set(slog.LevelInfo)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
