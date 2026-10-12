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

	"github.com/margarit/printpipe/internal/bridge"
	"github.com/margarit/printpipe/internal/config"
	"github.com/margarit/printpipe/internal/events"
	"github.com/margarit/printpipe/internal/jobs"
	"github.com/margarit/printpipe/internal/mcpserver"
	"github.com/margarit/printpipe/internal/pipeline"
	"github.com/margarit/printpipe/internal/project"
	"github.com/margarit/printpipe/internal/runner"
	"github.com/margarit/printpipe/internal/scad"
	"github.com/margarit/printpipe/internal/sessions"
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
	if len(os.Args) > 1 && os.Args[1] == "bridge" {
		if err := runBridge(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "printpipe bridge:", err)
			os.Exit(1)
		}
		return
	}
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
	mcpMode := flag.String("mcp", "auto", "serve MCP on stdin/stdout: true, false (HTTP only), or auto (only when stdin is a pipe, as with docker run -i)")
	demo := flag.Bool("demo", os.Getenv("PRINTPIPE_DEMO") == "1", "add a simulated printer and slicer when no printer is configured")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return nil
	}
	useMCP, err := mcpEnabled(*mcpMode)
	if err != nil {
		return err
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
	// Echo to a terminal during development; in the container stderr already goes to this file.
	if st, err := os.Stderr.Stat(); err == nil && st.Mode()&os.ModeCharDevice != 0 && !useMCP {
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	exec := runner.Exec{}
	compiler := scad.New(exec, envOr("OPENSCAD", "openscad"))
	if b := compiler.UseBestBackend(ctx); b != "" {
		logger.Info("openscad geometry backend", "backend", b)
	} else {
		logger.Info("openscad has no manifold backend; complex models compile slowly")
	}
	hub := events.NewHub()
	reg := sessions.NewRegistry()
	reg.OnChange = func(kind string, info sessions.Info) {
		hub.Publish(events.Session, map[string]any{"kind": kind, "session": info})
		logger.Info("session "+kind, "id", info.ID, "client", info.Client, "transport", info.Transport, "calls", info.Calls)
	}
	svc := pipeline.New(pipeline.Deps{
		DB:        db,
		Runner:    exec,
		Config:    cfg,
		Jobs:      jobs.NewStore(db),
		Projects:  project.NewStore(db, workDir),
		SCAD:      compiler,
		Slicers:   slicer.Registered(slicer.Deps{Runner: exec}),
		Events:    hub,
		Logger:    logger,
		ViewerURL: *viewerURL,
	})
	defer svc.Close()

	// One MCP server for every session: Streamable HTTP at /mcp (many clients at once),
	// and stdio when the container was started with docker run -i.
	mcpServer := mcpserver.New(svc, version, reg)
	mcpHTTP := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpServer },
		&mcp.StreamableHTTPOptions{SessionTimeout: 12 * time.Hour, Logger: logger})
	srv := &http.Server{Addr: *addr, Handler: web.Handler(svc, cfg, hub, nil, web.Options{MCP: mcpHTTP, Sessions: reg}),
		ReadHeaderTimeout: 10 * time.Second}
	httpErr := make(chan error, 1)
	go func() {
		logger.Info("http listening", "addr", *addr, "version", version)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			// Another session may hold the port; MCP keeps working without the viewer.
			logger.Error("http server stopped", "err", err)
			httpErr <- err
		}
	}()

	if useMCP {
		logger.Info("mcp serving on stdio")
		err = mcpServer.Run(ctx, &mcp.StdioTransport{})
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, io.EOF) {
			logger.Error("mcp stopped", "err", err)
		}
	} else {
		logger.Info("serving the viewer and MCP over HTTP", "viewer", *viewerURL, "mcp", *viewerURL+"/mcp")
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

// runBridge relays a stdio MCP client to the server's /mcp endpoint.
func runBridge(args []string) error {
	fs := flag.NewFlagSet("bridge", flag.ContinueOnError)
	url := fs.String("url", envOr("PRINTPIPE_MCP_URL", "http://127.0.0.1:8080/mcp"), "the server's MCP endpoint")
	pidfile := fs.String("pidfile", "", "write the process ID here, so a wrapper can stop the bridge")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *pidfile != "" {
		if err := os.WriteFile(*pidfile, []byte(fmt.Sprint(os.Getpid())), 0o644); err != nil {
			return err
		}
		defer os.Remove(*pidfile)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return bridge.Run(ctx, os.Stdin, os.Stdout, *url, version)
}

// mcpEnabled decides whether to serve MCP. "auto" serves it only when stdin is a pipe:
// an MCP client runs docker run -i; a plain docker run has no stdin and would exit at once.
func mcpEnabled(mode string) (bool, error) {
	switch mode {
	case "true", "on", "1":
		return true, nil
	case "false", "off", "0":
		return false, nil
	case "auto", "":
		st, err := os.Stdin.Stat()
		if err != nil {
			return false, nil
		}
		return st.Mode()&os.ModeNamedPipe != 0 || st.Mode()&os.ModeSocket != 0, nil
	}
	return false, fmt.Errorf("-mcp must be true, false or auto, got %q", mode)
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
