package web_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/margarit/printpipe/internal/config"
	"github.com/margarit/printpipe/internal/events"
	"github.com/margarit/printpipe/internal/pipeline/pipelinetest"
	_ "github.com/margarit/printpipe/internal/printers/bambu"
	"github.com/margarit/printpipe/internal/web"
)

type fixture struct {
	srv *httptest.Server
	env *pipelinetest.Env
	hub *events.Hub
}

func setup(t *testing.T) *fixture {
	env := pipelinetest.New(t)
	hub := events.NewHub()
	env.Events.Next = hub.Publish
	assets := fstest.MapFS{
		"index.html":      {Data: []byte("<!doctype html><title>viewer</title>")},
		"assets/app-1.js": {Data: []byte("console.log(1)")},
	}
	srv := httptest.NewServer(web.Handler(env.Svc, env.Config, hub, assets))
	t.Cleanup(srv.Close)
	return &fixture{srv: srv, env: env, hub: hub}
}

func (f *fixture) do(t *testing.T, method, path string, body any, hdr map[string]string) (*http.Response, []byte) {
	t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case []byte:
		rd = bytes.NewReader(b)
	default:
		raw, _ := json.Marshal(b)
		rd = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, f.srv.URL+path, rd)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp, out
}

func (f *fixture) compile(t *testing.T) {
	t.Helper()
	if _, err := f.env.Svc.Compile(context.Background(), "cube", pipelinetest.Cube, nil); err != nil {
		t.Fatal(err)
	}
}

func TestSettingsNeverReturnsAccessCode(t *testing.T) {
	f := setup(t)
	set := config.Defaults()
	set.Printers = []config.Printer{{ID: "a1", Name: "A1", Kind: "bambu-lan", Default: true,
		Config: map[string]string{"ip": "192.168.1.50", "serial": "SER", "access_code": "secret99"}}}
	resp, body := f.do(t, "PUT", "/api/settings", set, nil)
	if resp.StatusCode != 200 || strings.Contains(string(body), "secret99") {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	resp, body = f.do(t, "GET", "/api/settings", nil, nil)
	if strings.Contains(string(body), "secret99") || !strings.Contains(string(body), config.Mask) {
		t.Fatalf("GET leaked or did not mask: %s", body)
	}
	// A PUT carrying the mask keeps the stored code.
	var got config.Settings
	json.Unmarshal(body, &got)
	got.Printers[0].Name = "Workshop"
	if resp, body = f.do(t, "PUT", "/api/settings", got, nil); resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	stored, _ := f.env.Config.Load()
	if stored.Printers[0].Config["access_code"] != "secret99" || stored.Printers[0].Name != "Workshop" {
		t.Fatalf("%+v", stored.Printers[0])
	}
}

func TestInvalidSettingsListProblems(t *testing.T) {
	f := setup(t)
	set := config.Defaults()
	set.Pipeline.ApprovalRequired = false
	resp, body := f.do(t, "PUT", "/api/settings", set, nil)
	if resp.StatusCode != 400 || !strings.Contains(string(body), `"problems"`) || !strings.Contains(string(body), "approval cannot be turned off") {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
}

func TestCrossOriginWriteIsRejected(t *testing.T) {
	f := setup(t)
	f.compile(t)
	a, _ := f.env.Svc.RequestApproval(context.Background(), "cube", 0, "", zeroOverrides)
	resp, _ := f.do(t, "POST", "/api/jobs/"+a.Job.ID+"/approve", nil, map[string]string{"Origin": "https://evil.example"})
	if resp.StatusCode != 403 {
		t.Fatalf("got %d", resp.StatusCode)
	}
	if j, _ := f.env.Svc.Job(a.Job.ID); j.Approved {
		t.Fatal("cross-origin request approved the job")
	}
	resp, _ = f.do(t, "POST", "/api/jobs/"+a.Job.ID+"/approve", nil, map[string]string{"Sec-Fetch-Site": "cross-site"})
	if resp.StatusCode != 403 {
		t.Fatalf("sec-fetch-site: got %d", resp.StatusCode)
	}
	// Same origin works.
	resp, _ = f.do(t, "POST", "/api/jobs/"+a.Job.ID+"/approve", nil, map[string]string{"Origin": f.srv.URL})
	if resp.StatusCode != 200 {
		t.Fatalf("same origin: got %d", resp.StatusCode)
	}
}

func TestNonLocalHostIsRejected(t *testing.T) {
	f := setup(t)
	req, _ := http.NewRequest("GET", f.srv.URL+"/api/settings", nil)
	req.Host = "attacker.example:8080" // DNS rebinding
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != 403 {
		t.Fatalf("got %d", resp.StatusCode)
	}
}

func TestApproveRejectsBadJobID(t *testing.T) {
	f := setup(t)
	for _, id := range []string{"xyz", "123", "deadbeef00", "DEADBEEF"} {
		if resp, _ := f.do(t, "POST", "/api/jobs/"+id+"/approve", nil, nil); resp.StatusCode != 400 {
			t.Errorf("%s: got %d", id, resp.StatusCode)
		}
	}
	if resp, _ := f.do(t, "POST", "/api/jobs/deadbeef/approve", nil, nil); resp.StatusCode != 404 {
		t.Errorf("unknown job: got %d", resp.StatusCode)
	}
}

func TestEventsStreamModelUpdate(t *testing.T) {
	f := setup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", f.srv.URL+"/api/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	for f.hub.Subscribers() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	f.compile(t)
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if sc.Text() == "event: model" {
			sc.Scan()
			if !strings.Contains(sc.Text(), `"project":"cube"`) {
				t.Fatalf("data: %s", sc.Text())
			}
			return
		}
	}
	t.Fatal("no model event")
}

func TestProjectPathCannotEscapeWork(t *testing.T) {
	f := setup(t)
	for _, p := range []string{"/api/projects/..%2Fdata/model.stl", "/api/projects/..%2F..%2Fetc/model.scad", "/api/projects/a%20b/versions"} {
		resp, _ := f.do(t, "GET", p, nil, nil)
		if resp.StatusCode != 400 && resp.StatusCode != 404 {
			t.Errorf("%s: got %d", p, resp.StatusCode)
		}
		if resp.StatusCode == 200 {
			t.Errorf("%s served a file", p)
		}
	}
	resp, _ := f.do(t, "GET", "/api/projects/..%2Fdata/model.stl", nil, nil)
	if resp.StatusCode != 400 {
		t.Fatalf("got %d", resp.StatusCode)
	}
}

func TestModelAndParamsFlow(t *testing.T) {
	f := setup(t)
	f.compile(t)
	resp, body := f.do(t, "GET", "/api/projects/cube/model.stl", nil, nil)
	if resp.StatusCode != 200 || !bytes.HasPrefix(body, []byte("solid")) {
		t.Fatalf("%d %.20s", resp.StatusCode, body)
	}
	resp, body = f.do(t, "GET", "/api/projects/cube/params", nil, nil)
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"name":"size"`) {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	resp, body = f.do(t, "POST", "/api/projects/cube/params", map[string]any{"params": map[string]any{"size": 25}}, nil)
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"version":2`) {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	resp, body = f.do(t, "POST", "/api/projects/cube/params", map[string]any{"params": map[string]any{"nope": 1}}, nil)
	if resp.StatusCode != 422 || !strings.Contains(string(body), "unknown parameter") {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	resp, body = f.do(t, "GET", "/api/projects/cube/versions", nil, nil)
	var vs []map[string]any
	json.Unmarshal(body, &vs)
	if resp.StatusCode != 200 || len(vs) != 2 {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	resp, body = f.do(t, "GET", "/api/projects/cube/model.stl?v=1", nil, nil)
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Disposition"), "cube_v1.stl") {
		t.Fatalf("%d %v", resp.StatusCode, resp.Header)
	}
}

func TestEstimateEndpointsAndToolpathFile(t *testing.T) {
	f := setup(t)
	f.compile(t)
	if resp, _ := f.do(t, "GET", "/api/projects/cube/versions/1/estimate", nil, nil); resp.StatusCode != 404 {
		t.Fatalf("no estimate yet: got %d", resp.StatusCode)
	}
	resp, body := f.do(t, "POST", "/api/projects/cube/versions/1/estimate", map[string]any{}, nil)
	if resp.StatusCode != 202 && resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		resp, body = f.do(t, "GET", "/api/projects/cube/versions/1/estimate", nil, nil)
		if resp.StatusCode == 200 && strings.Contains(string(body), `"status":"done"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("estimate never finished: %s", body)
		}
		time.Sleep(20 * time.Millisecond)
	}
	resp, body = f.do(t, "POST", "/api/projects/cube/versions/1/estimate", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("cached estimate should return 200, got %d", resp.StatusCode)
	}
	resp, body = f.do(t, "GET", "/api/projects/cube/versions/1/job.gcode.3mf", nil, nil)
	if resp.StatusCode != 200 || !bytes.HasPrefix(body, []byte("PK")) {
		t.Fatalf("%d %.10q", resp.StatusCode, body)
	}
}

func TestApproveThenJobIsPrintable(t *testing.T) {
	f := setup(t)
	f.compile(t)
	a, err := f.env.Svc.RequestApproval(context.Background(), "cube", 0, "", zeroOverrides)
	if err != nil {
		t.Fatal(err)
	}
	resp, body := f.do(t, "GET", "/api/projects/cube/jobs", nil, nil)
	if !strings.Contains(string(body), a.Job.ID) {
		t.Fatalf("pending jobs: %s", body)
	}
	resp, body = f.do(t, "POST", "/api/jobs/"+a.Job.ID+"/approve", nil, nil)
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"approved":true`) {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	if _, err := f.env.Svc.Print(context.Background(), a.Job.ID); err != nil {
		t.Fatal(err)
	}
	resp, body = f.do(t, "GET", "/api/projects/cube/job.gcode.3mf?job="+a.Job.ID, nil, nil)
	if resp.StatusCode != 200 || !bytes.HasPrefix(body, []byte("PK")) {
		t.Fatalf("%d", resp.StatusCode)
	}
	resp, body = f.do(t, "GET", "/api/projects/cube/log", nil, nil)
	for _, tool := range []string{"compile_model", "request_approval", "approve", "print_job"} {
		if !strings.Contains(string(body), tool) {
			t.Errorf("log is missing %s: %s", tool, body)
		}
	}
}

func TestProfileUploadAndPrinterKinds(t *testing.T) {
	f := setup(t)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "Bambu PLA Basic.json")
	fw.Write([]byte(`{"name":"Bambu PLA Basic"}`))
	mw.Close()
	resp, body := f.do(t, "POST", "/api/profiles/filament", buf.Bytes(), map[string]string{"Content-Type": mw.FormDataContentType()})
	if resp.StatusCode != 200 || !strings.Contains(string(body), "Bambu PLA Basic.json") {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	resp, body = f.do(t, "POST", "/api/profiles/filament?name=x.json", []byte("not json"), nil)
	if resp.StatusCode != 400 {
		t.Fatalf("bad profile: %d %s", resp.StatusCode, body)
	}
	resp, body = f.do(t, "GET", "/api/printer-kinds", nil, nil)
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"bambu-lan"`) || !strings.Contains(string(body), `"secret"`) {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	resp, body = f.do(t, "POST", "/api/printers/sim/test", nil, nil)
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"ok":true`) {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	resp, body = f.do(t, "GET", "/api/printers", nil, nil)
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"state":"idle"`) {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
}

func TestExportImport(t *testing.T) {
	f := setup(t)
	resp, body := f.do(t, "GET", "/api/settings/export", nil, nil)
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("%d", resp.StatusCode)
	}
	resp, body = f.do(t, "POST", "/api/settings/import", body, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	resp, _ = f.do(t, "POST", "/api/settings/import", []byte(`{"format":"other"}`), nil)
	if resp.StatusCode != 400 {
		t.Fatalf("bad import: %d", resp.StatusCode)
	}
}

func TestFrontendRoutes(t *testing.T) {
	f := setup(t)
	for _, p := range []string{"/", "/settings"} {
		resp, body := f.do(t, "GET", p, nil, nil)
		if resp.StatusCode != 200 || !strings.Contains(string(body), "<title>viewer</title>") {
			t.Errorf("%s: %d %s", p, resp.StatusCode, body)
		}
	}
	resp, body := f.do(t, "GET", "/assets/app-1.js", nil, nil)
	if resp.StatusCode != 200 || string(body) != "console.log(1)" || !strings.Contains(resp.Header.Get("Cache-Control"), "immutable") {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	if resp, _ := f.do(t, "GET", "/api/nope", nil, nil); resp.StatusCode != 404 {
		t.Fatalf("unknown API path: %d", resp.StatusCode)
	}
}
