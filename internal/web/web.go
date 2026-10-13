// Package web serves the JSON API, the event stream and the built viewer.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/margarit/printpipe/internal/assembly"
	"github.com/margarit/printpipe/internal/bridge"
	"github.com/margarit/printpipe/internal/config"
	"github.com/margarit/printpipe/internal/events"
	"github.com/margarit/printpipe/internal/jobs"
	"github.com/margarit/printpipe/internal/pipeline"
	"github.com/margarit/printpipe/internal/printer"
	"github.com/margarit/printpipe/internal/project"
	"github.com/margarit/printpipe/internal/sessions"
	"github.com/margarit/printpipe/internal/slicer"
)

//go:embed all:static
var static embed.FS

type Server struct {
	svc      *pipeline.Service
	cfg      *config.Store
	hub      *events.Hub
	assets   fs.FS
	sessions *sessions.Registry
	assembly *assembly.Service
}

// Options are the optional parts of the HTTP surface.
type Options struct {
	// MCP, if set, is served at /mcp (Streamable HTTP; one server, many sessions).
	MCP http.Handler
	// Sessions, if set, backs /api/sessions.
	Sessions *sessions.Registry
	// Assembly, if set, backs the assembly timeline endpoints.
	Assembly *assembly.Service
}

// BridgeHeader marks requests relayed by printpipe bridge for a stdio client.
const BridgeHeader = bridge.Header

// Handler returns the whole HTTP surface. assets overrides the embedded frontend (tests pass nil).
func Handler(svc *pipeline.Service, cfg *config.Store, hub *events.Hub, assets fs.FS, opts Options) http.Handler {
	if assets == nil {
		sub, _ := fs.Sub(static, "static")
		assets = sub
	}
	reg := opts.Sessions
	if reg == nil {
		reg = sessions.NewRegistry()
	}
	s := &Server{svc: svc, cfg: cfg, hub: hub, assets: assets, sessions: reg, assembly: opts.Assembly}
	mux := http.NewServeMux()
	if opts.MCP != nil {
		h := s.trackMCP(opts.MCP)
		for _, m := range []string{"POST", "GET", "DELETE"} { // Streamable HTTP uses these three
			mux.Handle(m+" /mcp", h)
		}
	}
	mux.HandleFunc("GET /api/projects/{name}/versions/{v}/assembly", s.getAssembly)
	mux.HandleFunc("GET /api/projects/{name}/versions/{v}/assembly/parts/{i}", s.assemblyPart)
	mux.HandleFunc("GET /api/sessions", s.listSessions)
	mux.HandleFunc("GET /api/sessions/{id}", s.getSession)
	mux.HandleFunc("DELETE /api/sessions/{id}", s.endSession)

	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	mux.Handle("GET /api/events", hub)

	mux.HandleFunc("GET /api/projects", s.projects)
	mux.HandleFunc("GET /api/projects/{name}/versions", s.versions)
	mux.HandleFunc("GET /api/projects/{name}/model.stl", s.versionFile(project.STL, "model/stl"))
	mux.HandleFunc("GET /api/projects/{name}/model.scad", s.versionFile(project.SCAD, "text/plain; charset=utf-8"))
	mux.HandleFunc("GET /api/projects/{name}/preview.png", s.versionFile(project.PNG, "image/png"))
	mux.HandleFunc("GET /api/projects/{name}/job.gcode.3mf", s.jobFile)
	mux.HandleFunc("GET /api/projects/{name}/params", s.getParams)
	mux.HandleFunc("POST /api/projects/{name}/params", s.postParams)
	mux.HandleFunc("GET /api/projects/{name}/jobs", s.pendingJobs)
	mux.HandleFunc("GET /api/projects/{name}/log", s.projectLog)
	mux.HandleFunc("POST /api/projects/{name}/versions/{v}/estimate", s.startEstimate)
	mux.HandleFunc("GET /api/projects/{name}/versions/{v}/estimate", s.getEstimate)
	mux.HandleFunc("GET /api/projects/{name}/versions/{v}/job.gcode.3mf", s.versionSlice)
	mux.HandleFunc("POST /api/projects/{name}/versions/{v}/restore", s.restore)
	mux.HandleFunc("POST /api/projects/{name}/versions/{v}/job", s.requestJob)

	mux.HandleFunc("GET /api/jobs/{id}", s.getJob)
	mux.HandleFunc("POST /api/jobs/{id}/approve", s.approve)
	mux.HandleFunc("POST /api/jobs/{id}/print", s.printJob)

	mux.HandleFunc("GET /api/settings", s.getSettings)
	mux.HandleFunc("PUT /api/settings", s.putSettings)
	mux.HandleFunc("GET /api/settings/export", s.exportSettings)
	mux.HandleFunc("POST /api/settings/import", s.importSettings)
	mux.HandleFunc("POST /api/profiles/{kind}", s.uploadProfile)
	mux.HandleFunc("GET /api/printers", s.printers)
	mux.HandleFunc("GET /api/printer-kinds", s.printerKinds)
	mux.HandleFunc("POST /api/printers/{id}/test", s.testPrinter)
	mux.HandleFunc("GET /api/printers/{id}/presets", s.presets)
	mux.HandleFunc("POST /api/printers/{id}/presets", s.importPreset)
	mux.HandleFunc("POST /api/printers/{id}/cancel", s.cancel)
	mux.HandleFunc("GET /api/runs", s.runs)

	mux.HandleFunc("GET /", s.frontend)
	return guard(mux)
}

// guard rejects requests that don't come from this computer's browser: a Host other than
// localhost (DNS rebinding) and cross-origin writes (CSRF).
func guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !localHost(r.Host) {
			writeErr(w, http.StatusForbidden, errors.New("this server only answers requests for localhost"))
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if o := r.Header.Get("Origin"); o != "" {
				u, err := url.Parse(o)
				if err != nil || u.Host != r.Host || !localHost(u.Host) {
					writeErr(w, http.StatusForbidden, errors.New("cross-origin request rejected"))
					return
				}
			}
			if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
				writeErr(w, http.StatusForbidden, errors.New("cross-site request rejected"))
				return
			}
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func localHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ---- helpers -------------------------------------------------------------

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	body := map[string]any{"error": err.Error()}
	var ve *config.ValidationError
	if errors.As(err, &ve) {
		body["problems"] = ve.Problems
	}
	writeJSON(w, code, body)
}

// status picks the HTTP status for a pipeline error.
func status(err error) int {
	var ve *config.ValidationError
	switch {
	case errors.As(err, &ve), errors.Is(err, project.ErrBadName), errors.Is(err, pipeline.ErrTooBig):
		return http.StatusBadRequest
	case errors.Is(err, project.ErrNotFound), errors.Is(err, jobs.ErrNotFound), errors.Is(err, config.ErrUnknownPrinter):
		return http.StatusNotFound
	case errors.Is(err, jobs.ErrPrinted), errors.Is(err, printer.ErrBusy), errors.Is(err, config.ErrNoPrinter), errors.Is(err, jobs.ErrNotApproved), errors.Is(err, jobs.ErrFileChanged):
		return http.StatusConflict
	case errors.Is(err, printer.ErrUnreachable), errors.Is(err, printer.ErrAuth), errors.Is(err, printer.ErrNoStorage):
		return http.StatusBadGateway
	}
	return http.StatusInternalServerError
}

func fail(w http.ResponseWriter, err error) { writeErr(w, status(err), err) }

func projectName(w http.ResponseWriter, r *http.Request) (string, bool) {
	name := r.PathValue("name")
	if err := project.ValidName(name); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return "", false
	}
	return name, true
}

// versionParam reads {v} from the path or ?v= from the query; 0 means latest.
func versionParam(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := r.PathValue("v")
	if raw == "" {
		raw = r.URL.Query().Get("v")
	}
	if raw == "" || raw == "latest" {
		return 0, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("version must be a positive number, got %q", raw))
		return 0, false
	}
	return n, true
}

var jobIDRe = regexp.MustCompile(`^[0-9a-f]{8}$`)

func jobID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if !jobIDRe.MatchString(id) {
		writeErr(w, http.StatusBadRequest, errors.New("job ID must be 8 hex digits"))
		return "", false
	}
	return id, true
}

var printerIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 8<<20))
	if err := dec.Decode(v); err != nil {
		return &config.ValidationError{Problems: []string{"request body is not valid JSON: " + err.Error()}}
	}
	return nil
}

func serveFile(w http.ResponseWriter, r *http.Request, path, contentType string) {
	f, err := os.Open(path)
	if err != nil {
		writeErr(w, http.StatusNotFound, errors.New("file not found"))
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, "", st.ModTime(), f)
}

// ---- projects ------------------------------------------------------------

func (s *Server) projects(w http.ResponseWriter, r *http.Request) {
	ps, err := s.svc.Projects()
	if err != nil {
		fail(w, err)
		return
	}
	if ps == nil {
		ps = []project.Summary{}
	}
	writeJSON(w, 200, ps)
}

func (s *Server) versions(w http.ResponseWriter, r *http.Request) {
	name, ok := projectName(w, r)
	if !ok {
		return
	}
	vs, err := s.svc.Versions(name)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, vs)
}

func (s *Server) versionFile(file, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name, ok := projectName(w, r)
		if !ok {
			return
		}
		n, ok := versionParam(w, r)
		if !ok {
			return
		}
		v, err := s.svc.Version(name, n)
		if err != nil {
			fail(w, err)
			return
		}
		if file == project.STL || file == project.SCAD {
			w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s_v%d%s"`, name, v.Number, filepath.Ext(file)))
		}
		serveFile(w, r, v.Path(file), contentType)
	}
}

// jobFile serves a job's sliced file (?job=ID), or the project's latest requested one.
func (s *Server) jobFile(w http.ResponseWriter, r *http.Request) {
	name, ok := projectName(w, r)
	if !ok {
		return
	}
	if id := r.URL.Query().Get("job"); id != "" {
		if !jobIDRe.MatchString(id) {
			writeErr(w, http.StatusBadRequest, errors.New("job ID must be 8 hex digits"))
			return
		}
		job, err := s.svc.Job(id)
		if err != nil {
			fail(w, err)
			return
		}
		if job.Project != name {
			writeErr(w, http.StatusNotFound, errors.New("no such job in this project"))
			return
		}
		serveFile(w, r, job.FilePath, "application/vnd.ms-package.3dmanufacturing-3dmodel+xml")
		return
	}
	serveFile(w, r, filepath.Join(s.projectDir(name), project.Job), "application/vnd.ms-package.3dmanufacturing-3dmodel+xml")
}

func (s *Server) projectDir(name string) string {
	v, err := s.svc.Version(name, 0)
	if err != nil {
		return ""
	}
	return filepath.Dir(v.Dir)
}

// versionSlice serves the cached sliced file of a version for a printer, for the toolpath preview.
func (s *Server) versionSlice(w http.ResponseWriter, r *http.Request) {
	name, ok := projectName(w, r)
	if !ok {
		return
	}
	n, ok := versionParam(w, r)
	if !ok {
		return
	}
	e, found, err := s.svc.CachedEstimate(name, n, r.URL.Query().Get("printer"))
	if err != nil {
		fail(w, err)
		return
	}
	if !found || e.Status != "done" {
		writeErr(w, http.StatusNotFound, errors.New("this version has not been sliced for that printer yet: estimate it first"))
		return
	}
	serveFile(w, r, e.JobPath, "application/vnd.ms-package.3dmanufacturing-3dmodel+xml")
}

func (s *Server) getParams(w http.ResponseWriter, r *http.Request) {
	name, ok := projectName(w, r)
	if !ok {
		return
	}
	n, ok := versionParam(w, r)
	if !ok {
		return
	}
	ps, err := s.svc.Params(name, n)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, ps)
}

// postParams recompiles the latest version's code with new parameter values (PRD V-9).
func (s *Server) postParams(w http.ResponseWriter, r *http.Request) {
	name, ok := projectName(w, r)
	if !ok {
		return
	}
	var body struct {
		Params map[string]any `json:"params"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, err)
		return
	}
	// A reload or a closed tab must not kill a compile halfway: it finishes and the viewer
	// picks up the new version from the event stream.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 15*time.Minute)
	defer cancel()
	res, err := s.svc.Compile(ctx, name, "", body.Params)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err)
		return
	}
	writeJSON(w, 200, res)
}

func (s *Server) restore(w http.ResponseWriter, r *http.Request) {
	name, ok := projectName(w, r)
	if !ok {
		return
	}
	n, ok := versionParam(w, r)
	if !ok {
		return
	}
	res, err := s.svc.Restore(r.Context(), name, n)
	if err != nil {
		writeErr(w, status(err), err)
		return
	}
	writeJSON(w, 200, res)
}

func (s *Server) pendingJobs(w http.ResponseWriter, r *http.Request) {
	name, ok := projectName(w, r)
	if !ok {
		return
	}
	js, err := s.svc.PendingJobs(name)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, js)
}

func (s *Server) projectLog(w http.ResponseWriter, r *http.Request) {
	name, ok := projectName(w, r)
	if !ok {
		return
	}
	runs, err := s.svc.Runs(pipeline.RunFilter{Project: name}, 200)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, runs)
}

func (s *Server) runs(w http.ResponseWriter, r *http.Request) {
	runs, err := s.svc.Runs(pipeline.RunFilter{Project: r.URL.Query().Get("project"), Session: r.URL.Query().Get("session")}, 200)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, runs)
}

func (s *Server) startEstimate(w http.ResponseWriter, r *http.Request) {
	name, ok := projectName(w, r)
	if !ok {
		return
	}
	n, ok := versionParam(w, r)
	if !ok {
		return
	}
	var body struct {
		PrinterID string `json:"printer_id"`
	}
	if r.ContentLength != 0 {
		if err := decode(r, &body); err != nil {
			fail(w, err)
			return
		}
	}
	e, err := s.svc.StartEstimate(name, n, body.PrinterID)
	if err != nil {
		fail(w, err)
		return
	}
	code := http.StatusOK
	if e.Status == "running" {
		code = http.StatusAccepted
	}
	writeJSON(w, code, e)
}

func (s *Server) getEstimate(w http.ResponseWriter, r *http.Request) {
	name, ok := projectName(w, r)
	if !ok {
		return
	}
	n, ok := versionParam(w, r)
	if !ok {
		return
	}
	e, found, err := s.svc.CachedEstimate(name, n, r.URL.Query().Get("printer"))
	if err != nil {
		fail(w, err)
		return
	}
	if !found {
		writeErr(w, http.StatusNotFound, errors.New("no estimate yet"))
		return
	}
	writeJSON(w, 200, e)
}

// ---- jobs ----------------------------------------------------------------

func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	id, ok := jobID(w, r)
	if !ok {
		return
	}
	job, err := s.svc.Job(id)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, job)
}

// approve is the only way a job becomes printable.
func (s *Server) approve(w http.ResponseWriter, r *http.Request) {
	id, ok := jobID(w, r)
	if !ok {
		return
	}
	job, err := s.svc.Approve(id)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, job)
}

// requestJob slices a version for a printer and creates a job waiting for approval: the
// viewer's Print button, the same as the agent's request_approval. Body: {"printer_id"}.
func (s *Server) requestJob(w http.ResponseWriter, r *http.Request) {
	name, ok := projectName(w, r)
	if !ok {
		return
	}
	n, ok := versionParam(w, r)
	if !ok {
		return
	}
	var body struct {
		PrinterID string `json:"printer_id"`
	}
	if r.ContentLength != 0 {
		if err := decode(r, &body); err != nil {
			fail(w, err)
			return
		}
	}
	// Slicing outlives a closed tab; the job arrives on the event stream.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 15*time.Minute)
	defer cancel()
	a, err := s.svc.RequestApproval(ctx, name, n, body.PrinterID, slicer.Overrides{})
	if err != nil {
		if status(err) == http.StatusInternalServerError {
			writeErr(w, http.StatusUnprocessableEntity, err)
		} else {
			fail(w, err)
		}
		return
	}
	writeJSON(w, 200, a)
}

// printJob sends an approved job to its printer: the viewer's "Send to printer" button.
// The user is the one clicking, so this is the human path the approval gate allows.
func (s *Server) printJob(w http.ResponseWriter, r *http.Request) {
	id, ok := jobID(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Minute)
	defer cancel()
	res, err := s.svc.Print(ctx, id)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, res)
}

// ---- settings and printers -----------------------------------------------

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	set, err := s.cfg.Load()
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, set.Masked())
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var set config.Settings
	if err := decode(r, &set); err != nil {
		fail(w, err)
		return
	}
	if err := s.cfg.Save(set); err != nil {
		fail(w, err)
		return
	}
	saved, err := s.cfg.Load()
	if err != nil {
		fail(w, err)
		return
	}
	s.hub.Publish(events.Settings, map[string]any{"saved_at": time.Now().UTC()})
	writeJSON(w, 200, saved.Masked())
}

func (s *Server) exportSettings(w http.ResponseWriter, r *http.Request) {
	b, err := s.cfg.Export()
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="printpipe-settings-%s.json"`, time.Now().Format("2006-01-02")))
	writeJSON(w, 200, b)
}

func (s *Server) importSettings(w http.ResponseWriter, r *http.Request) {
	var b config.Bundle
	if err := decode(r, &b); err != nil {
		fail(w, err)
		return
	}
	if err := s.cfg.Import(b); err != nil {
		fail(w, err)
		return
	}
	set, _ := s.cfg.Load()
	s.hub.Publish(events.Settings, map[string]any{"saved_at": time.Now().UTC()})
	writeJSON(w, 200, set.Masked())
}

// uploadProfile takes a multipart "file" field, or a raw JSON body with ?name=.
func (s *Server) uploadProfile(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	var name string
	var body []byte
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if ct == "multipart/form-data" {
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		f, hdr, err := r.FormFile("file")
		if err != nil {
			writeErr(w, http.StatusBadRequest, errors.New("send the profile as a file field named \"file\""))
			return
		}
		defer f.Close()
		name = filepath.Base(hdr.Filename)
		body, err = io.ReadAll(io.LimitReader(f, 8<<20))
		if err != nil {
			fail(w, err)
			return
		}
	} else {
		name = r.URL.Query().Get("name")
		var err error
		body, err = io.ReadAll(io.LimitReader(r.Body, 8<<20))
		if err != nil {
			fail(w, err)
			return
		}
	}
	p, err := s.cfg.SaveProfile(kind, name, body)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, p)
}

// presets lists the profiles the printer's slicer ships (Bambu Studio's system presets).
func (s *Server) presets(w http.ResponseWriter, r *http.Request) {
	ps, err := s.svc.SystemPresets(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, ps)
}

// importPreset copies a system preset into the uploaded profiles: {"kind", "name"}.
func (s *Server) importPreset(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	p, err := s.svc.ImportPreset(r.PathValue("id"), req.Kind, req.Name)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, p)
}

func (s *Server) printers(w http.ResponseWriter, r *http.Request) {
	ps, err := s.svc.Printers(r.Context(), r.URL.Query().Get("status") != "0")
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, ps)
}

type kindInfo struct {
	Kind   string          `json:"kind"`
	Label  string          `json:"label"`
	Fields []printer.Field `json:"fields"`
}

func (s *Server) printerKinds(w http.ResponseWriter, r *http.Request) {
	out := []kindInfo{}
	for _, d := range printer.Drivers() {
		out = append(out, kindInfo{Kind: d.Kind, Label: d.Label, Fields: d.Fields})
	}
	writeJSON(w, 200, map[string]any{
		"printers":       out,
		"slicers":        slicer.Kinds(),
		"profile_kinds":  config.ProfileKinds,
		"preview_angles": config.PreviewAngles,
		"display_modes":  config.DisplayModes,
		"log_levels":     config.LogLevels,
		"support_types":  config.SupportTypes,
	})
}

func (s *Server) testPrinter(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !printerIDRe.MatchString(id) {
		writeErr(w, http.StatusBadRequest, errors.New("bad printer ID"))
		return
	}
	checks, err := s.svc.TestPrinter(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	ok := true
	for _, c := range checks {
		ok = ok && c.OK
	}
	writeJSON(w, 200, map[string]any{"ok": ok, "checks": checks})
}

func (s *Server) cancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !printerIDRe.MatchString(id) {
		writeErr(w, http.StatusBadRequest, errors.New("bad printer ID"))
		return
	}
	if err := s.svc.Cancel(r.Context(), id); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// ---- assembly ----------------------------------------------------------------

// getAssembly returns the version's assembly parts, building them on first request
// (202 while building; an assembly event follows).
func (s *Server) getAssembly(w http.ResponseWriter, r *http.Request) {
	if s.assembly == nil {
		writeErr(w, http.StatusNotFound, errors.New("assembly mode is not enabled"))
		return
	}
	name, ok := projectName(w, r)
	if !ok {
		return
	}
	n, ok := versionParam(w, r)
	if !ok {
		return
	}
	v, err := s.svc.Version(name, n)
	if err != nil {
		fail(w, err)
		return
	}
	res := s.assembly.Start(v)
	code := http.StatusOK
	if res.Status == "running" {
		code = http.StatusAccepted
	}
	writeJSON(w, code, res)
}

func (s *Server) assemblyPart(w http.ResponseWriter, r *http.Request) {
	name, ok := projectName(w, r)
	if !ok {
		return
	}
	n, ok := versionParam(w, r)
	if !ok {
		return
	}
	i, err := strconv.Atoi(r.PathValue("i"))
	if err != nil || i < 0 || i > 999 {
		writeErr(w, http.StatusBadRequest, errors.New("part index must be a number"))
		return
	}
	v, err := s.svc.Version(name, n)
	if err != nil {
		fail(w, err)
		return
	}
	serveFile(w, r, assembly.PartFile(v, i), "model/stl")
}

// ---- sessions --------------------------------------------------------------

// sessionWriter catches the session ID the MCP handler assigns on initialize.
type sessionWriter struct {
	http.ResponseWriter
	onHeader func(h http.Header)
	done     bool
}

func (w *sessionWriter) WriteHeader(code int) {
	if !w.done {
		w.done = true
		w.onHeader(w.Header())
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *sessionWriter) Write(b []byte) (int, error) {
	if !w.done {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

func (w *sessionWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *sessionWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// trackMCP records how each new MCP session arrived: its remote address, user agent,
// and whether printpipe bridge relays it for a stdio client.
func (s *Server) trackMCP(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Mcp-Session-Id") != "" {
			h.ServeHTTP(w, r)
			return
		}
		transport := sessions.HTTP
		if r.Header.Get(BridgeHeader) != "" {
			transport = sessions.Bridge
		}
		h.ServeHTTP(&sessionWriter{ResponseWriter: w, onHeader: func(hd http.Header) {
			if id := hd.Get("Mcp-Session-Id"); id != "" {
				s.sessions.Transport(id, transport, r.RemoteAddr, r.UserAgent())
			}
		}}, r)
	})
}

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.sessions.List())
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	info, ok := s.sessions.Get(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, errors.New("no such session"))
		return
	}
	runs, err := s.svc.Runs(pipeline.RunFilter{Session: info.ID}, 100)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"session": info, "runs": runs})
}

// endSession closes a session from the viewer; its client reconnects with a new one.
func (s *Server) endSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.sessions.Get(id); !ok {
		writeErr(w, http.StatusNotFound, errors.New("no such session"))
		return
	}
	if !s.sessions.End(id) {
		writeErr(w, http.StatusConflict, errors.New("the session is already closed"))
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// ---- frontend ------------------------------------------------------------

// frontend serves the built single-page app; unknown paths get index.html so /settings works.
func (s *Server) frontend(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeErr(w, http.StatusNotFound, errors.New("no such endpoint"))
		return
	}
	p := strings.TrimPrefix(filepath.Clean("/"+r.URL.Path), "/")
	if p != "" {
		if st, err := fs.Stat(s.assets, p); err == nil && !st.IsDir() {
			if strings.HasPrefix(p, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			http.ServeFileFS(w, r, s.assets, p)
			return
		}
	}
	index, err := fs.ReadFile(s.assets, "index.html")
	if err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><meta charset="utf-8"><title>printpipe</title><p>The viewer was not built into this binary. Run <code>npm run build</code> in web/ and rebuild, or use the Docker image.</p>`)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(index)
}
