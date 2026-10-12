package bambu_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/margarit/printpipe/internal/printer"
	"github.com/margarit/printpipe/internal/printer/printertest"
	"github.com/margarit/printpipe/internal/printers/bambu"
	"github.com/margarit/printpipe/internal/runner"
)

type fakeFTPS struct {
	err      error
	uploaded []string
}

func (f *fakeFTPS) Upload(_ context.Context, _ bambu.Config, _, name string) error {
	if f.err != nil {
		return f.err
	}
	f.uploaded = append(f.uploaded, name)
	return nil
}
func (f *fakeFTPS) List(context.Context, bambu.Config) error {
	if errors.Is(f.err, printer.ErrNoStorage) {
		return nil // login works; the card is the problem
	}
	return f.err
}

type fakeMQTT struct {
	err       error
	state     string
	published [][]byte
}

func (f *fakeMQTT) Publish(_ context.Context, _ bambu.Config, p []byte) error {
	if f.err != nil {
		return f.err
	}
	f.published = append(f.published, p)
	var m struct{ Print struct{ Command string } }
	json.Unmarshal(p, &m)
	switch m.Print.Command {
	case "project_file":
		f.state = "RUNNING"
	case "stop":
		f.state = "IDLE"
	}
	return nil
}
func (f *fakeMQTT) Report(context.Context, bambu.Config) (bambu.Report, error) {
	if f.err != nil {
		return bambu.Report{}, f.err
	}
	if f.state == "" {
		f.state = "IDLE"
	}
	return bambu.Report{GcodeState: f.state}, nil
}

var cfg = bambu.Config{IP: "10.0.0.5", Serial: "TEST", AccessCode: "x"}

func TestBambuDriverContract(t *testing.T) {
	printertest.Run(t, printertest.Harness{
		New: func(t *testing.T) printer.Printer {
			return bambu.New(cfg, bambu.WithUploader(&fakeFTPS{}), bambu.WithMQTT(&fakeMQTT{}))
		},
		Broken: func(t *testing.T, want error) printer.Printer {
			return bambu.New(cfg, bambu.WithUploader(&fakeFTPS{err: want}), bambu.WithMQTT(&fakeMQTT{}))
		},
	})
}

func TestStartPayload(t *testing.T) {
	var m struct {
		Print map[string]any `json:"print"`
	}
	if err := json.Unmarshal(bambu.StartPayload("cube.gcode.3mf"), &m); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"command": "project_file",
		"url":     "file:///sdcard/cube.gcode.3mf",
		"param":   "Metadata/plate_1.gcode",
		"use_ams": false,
	}
	for k, v := range want {
		if m.Print[k] != v {
			t.Errorf("%s = %v, want %v", k, m.Print[k], v)
		}
	}
}

func TestUploadThenStartPublishesProjectFile(t *testing.T) {
	ftp, mq := &fakeFTPS{}, &fakeMQTT{}
	p := bambu.New(cfg, bambu.WithUploader(ftp), bambu.WithMQTT(mq))
	ctx := context.Background()
	r, err := p.Upload(ctx, printer.Job{Path: "/work/cube/v1/job.gcode.3mf", Name: "cube v1.gcode.3mf"})
	if err != nil {
		t.Fatal(err)
	}
	if r.ID != "cube_v1.gcode.3mf" || len(ftp.uploaded) != 1 {
		t.Fatalf("remote %q, uploads %v", r.ID, ftp.uploaded)
	}
	if err := p.Start(ctx, r); err != nil {
		t.Fatal(err)
	}
	if len(mq.published) != 1 || !strings.Contains(string(mq.published[0]), `"file:///sdcard/cube_v1.gcode.3mf"`) {
		t.Fatalf("published %s", mq.published)
	}
}

func TestStartWhilePrintingIsBusy(t *testing.T) {
	mq := &fakeMQTT{state: "RUNNING"}
	p := bambu.New(cfg, bambu.WithUploader(&fakeFTPS{}), bambu.WithMQTT(mq))
	r, _ := p.Upload(context.Background(), printer.Job{Path: "x", Name: "a.gcode.3mf"})
	if err := p.Start(context.Background(), r); !errors.Is(err, printer.ErrBusy) {
		t.Fatalf("want ErrBusy, got %v", err)
	}
}

func TestRejectsNon3MF(t *testing.T) {
	p := bambu.New(cfg, bambu.WithUploader(&fakeFTPS{}), bambu.WithMQTT(&fakeMQTT{}))
	if _, err := p.Upload(context.Background(), printer.Job{Path: "x", Name: "a.gcode"}); err == nil {
		t.Fatal("plain .gcode must be rejected")
	}
}

func TestStatusMapping(t *testing.T) {
	for state, want := range map[string]printer.State{
		"IDLE": printer.Idle, "PREPARE": printer.Printing, "RUNNING": printer.Printing,
		"PAUSE": printer.Paused, "FINISH": printer.Finished, "FAILED": printer.Failed,
	} {
		p := bambu.New(cfg, bambu.WithUploader(&fakeFTPS{}), bambu.WithMQTT(&fakeMQTT{state: state}))
		if st, _ := p.Status(context.Background()); st.State != want {
			t.Errorf("%s → %s, want %s", state, st.State, want)
		}
	}
}

func TestDiagnoseReportsEachPort(t *testing.T) {
	p := bambu.New(cfg, bambu.WithUploader(&fakeFTPS{err: printer.ErrAuth}), bambu.WithMQTT(&fakeMQTT{}))
	checks := p.Diagnose(context.Background())
	if len(checks) != 2 || checks[0].OK || !checks[1].OK || !strings.Contains(checks[0].Message, "Not authorized") {
		t.Fatalf("%+v", checks)
	}
}

func TestCurlMapsErrors(t *testing.T) {
	cases := map[string]error{
		"curl failed: exit status 67\ncurl: (67) Access denied: 530":     printer.ErrAuth,
		"curl failed: exit status 7\ncurl: (7) Failed to connect":        printer.ErrUnreachable,
		"curl failed: exit status 28\ncurl: (28) Connection timed out":   printer.ErrUnreachable,
		"curl failed: exit status 25\ncurl: (25) Failed FTP upload: 550": printer.ErrNoStorage,
	}
	for msg, want := range cases {
		f := &runner.Fake{OnRun: func(string, []string) ([]byte, error) { return nil, errors.New(msg) }}
		u := &bambu.CurlUploader{Runner: f}
		if err := u.Upload(context.Background(), cfg, "/tmp/x", "x.gcode.3mf"); !errors.Is(err, want) {
			t.Errorf("%q → %v, want %v", msg, err, want)
		}
	}
}

func TestCurlKeepsAccessCodeOffCommandLine(t *testing.T) {
	f := &runner.Fake{}
	u := &bambu.CurlUploader{Runner: f}
	u.Upload(context.Background(), bambu.Config{IP: "10.0.0.5", Serial: "S", AccessCode: "secret123"}, "/tmp/x", "x.gcode.3mf")
	line := strings.Join(f.Calls[0], " ")
	if strings.Contains(line, "secret123") {
		t.Fatalf("access code on the command line: %s", line)
	}
	if !strings.Contains(line, "ftps://10.0.0.5:990/x.gcode.3mf") || !strings.Contains(line, "--ssl-reqd") {
		t.Fatalf("args: %s", line)
	}
}

func TestRegisteredDriverValidatesConfig(t *testing.T) {
	d, ok := printer.Lookup(bambu.Kind)
	if !ok {
		t.Fatal("not registered")
	}
	if _, err := d.New(map[string]string{"ip": "nope", "serial": "S", "access_code": "x"}, printer.Deps{Runner: &runner.Fake{}}); err == nil {
		t.Fatal("bad IP accepted")
	}
	if _, err := d.New(map[string]string{"ip": "10.0.0.5", "serial": "S", "access_code": "x"}, printer.Deps{Runner: &runner.Fake{}}); err != nil {
		t.Fatal(err)
	}
}
