package bambu_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

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
	reply     *bambu.Reply // the answer to a command; success when nil
	noReply   bool
}

func (f *fakeMQTT) Command(ctx context.Context, c bambu.Config, p []byte) (bambu.Reply, error) {
	if f.reply != nil && f.reply.Result != "success" {
		f.published = append(f.published, p)
		return *f.reply, nil
	}
	if err := f.Publish(ctx, c, p); err != nil {
		return bambu.Reply{}, err
	}
	if f.noReply {
		return bambu.Reply{}, bambu.ErrNoReply
	}
	return bambu.Reply{Command: "project_file", Result: "success"}, nil
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

func TestStartRefusedByFirmwareExplainsDeveloperMode(t *testing.T) {
	mq := &fakeMQTT{reply: &bambu.Reply{Command: "project_file", Result: "failed", Reason: "mqtt message verify failed", ErrCode: 84033543}}
	p := bambu.New(cfg, bambu.WithUploader(&fakeFTPS{}), bambu.WithMQTT(mq))
	r, _ := p.Upload(context.Background(), printer.Job{Path: "x", Name: "a.gcode.3mf"})
	err := p.Start(context.Background(), r)
	if !errors.Is(err, printer.ErrRefused) || !strings.Contains(err.Error(), "Developer Mode") {
		t.Fatalf("got %v", err)
	}
	mq.reply = &bambu.Reply{Command: "project_file", Result: "failed", Reason: "file not found", ErrCode: 7}
	err = p.Start(context.Background(), r)
	if !errors.Is(err, printer.ErrRefused) || !strings.Contains(err.Error(), "file not found (code 7)") || strings.Contains(err.Error(), "Developer") {
		t.Fatalf("got %v", err)
	}
}

func TestStartWithoutReplyWatchesForThePrint(t *testing.T) {
	defer func(d time.Duration) { bambu.StartWait = d }(bambu.StartWait)
	bambu.StartWait = 0
	mq := &fakeMQTT{noReply: true}
	p := bambu.New(cfg, bambu.WithUploader(&fakeFTPS{}), bambu.WithMQTT(mq))
	r, _ := p.Upload(context.Background(), printer.Job{Path: "x", Name: "a.gcode.3mf"})
	if err := p.Start(context.Background(), r); err != nil { // the fake starts printing
		t.Fatal(err)
	}
	quiet := &silentMQTT{fakeMQTT: fakeMQTT{noReply: true}}
	p = bambu.New(cfg, bambu.WithUploader(&fakeFTPS{}), bambu.WithMQTT(quiet))
	r, _ = p.Upload(context.Background(), printer.Job{Path: "x", Name: "a.gcode.3mf"})
	if err := p.Start(context.Background(), r); !errors.Is(err, printer.ErrRefused) || !strings.Contains(err.Error(), "did not answer") {
		t.Fatalf("got %v", err)
	}
}

// silentMQTT takes the command but never starts printing.
type silentMQTT struct{ fakeMQTT }

func (s *silentMQTT) Command(context.Context, bambu.Config, []byte) (bambu.Reply, error) {
	return bambu.Reply{}, bambu.ErrNoReply
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
		// A port that contains 530 is not a login failure.
		"curl failed: exit status 7\ncurl: (7) Failed to connect to 10.0.0.5 port 45307 after 0 ms: Connection refused": printer.ErrUnreachable,
		"FTPS: 530 Login incorrect": printer.ErrAuth,
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

func TestStatusChannelDownOnALivePrinterIsUnknown(t *testing.T) {
	for _, tc := range []struct {
		err       error
		reachable bool
		want      printer.State
	}{
		{printer.ErrUnreachable, true, printer.Unknown},
		{printer.ErrUnreachable, false, printer.Offline},
		{printer.ErrAuth, true, printer.Offline},
	} {
		p := bambu.New(cfg, bambu.WithMQTT(&fakeMQTT{err: tc.err}),
			bambu.WithReachable(func(context.Context, string) bool { return tc.reachable }))
		st, err := p.Status(context.Background())
		if err == nil || st.State != tc.want || st.Message == "" {
			t.Errorf("%v reachable=%v: got %+v %v, want %s", tc.err, tc.reachable, st, err, tc.want)
		}
	}
}

func TestStartUnconfirmedWhenStatusIsUnreadable(t *testing.T) {
	defer func(d time.Duration) { bambu.StartWait = d }(bambu.StartWait)
	bambu.StartWait = 0
	// Commands go out, but no status can be read: the start may well have worked.
	quiet := &silentMQTT{fakeMQTT: fakeMQTT{err: printer.ErrUnreachable}}
	p := bambu.New(cfg, bambu.WithUploader(&fakeFTPS{}), bambu.WithMQTT(quiet),
		bambu.WithReachable(func(context.Context, string) bool { return true }))
	r, _ := p.Upload(context.Background(), printer.Job{Path: "x", Name: "a.gcode.3mf"})
	err := p.Start(context.Background(), r)
	if !errors.Is(err, printer.ErrUnconfirmed) || errors.Is(err, printer.ErrRefused) || !strings.Contains(err.Error(), "picks the job up") {
		t.Fatalf("got %v", err)
	}
}
