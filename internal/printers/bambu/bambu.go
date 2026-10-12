// Package bambu drives Bambu Lab printers over the LAN (A1 first): implicit FTPS on
// port 990 to put the job on the SD card, MQTT over TLS on port 8883 to start it and
// read status. The printer must be in LAN Only mode with Developer Mode on.
package bambu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path"
	"strings"
	"sync"

	"github.com/margarit/printpipe/internal/printer"
)

const Kind = "bambu-lan"

func init() {
	printer.Register(printer.Driver{
		Kind:  Kind,
		Label: "Bambu Lab A1 (LAN, Developer Mode)",
		Fields: []printer.Field{
			{Key: "ip", Label: "IP address", Type: "ip", Required: true,
				Help: "Printer touchscreen → WLAN. Reserve it in your router so it doesn't change."},
			{Key: "serial", Label: "Serial number", Type: "text", Required: true,
				Help: "Device info screen, or the label on the printer."},
			{Key: "access_code", Label: "LAN access code", Type: "secret", Required: true,
				Help: "Printer touchscreen → WLAN. Needs LAN Only mode and Developer Mode on."},
		},
		New: func(cfg map[string]string, deps printer.Deps) (printer.Printer, error) {
			c := Config{IP: cfg["ip"], Serial: cfg["serial"], AccessCode: cfg["access_code"]}
			if err := c.Validate(); err != nil {
				return nil, err
			}
			if deps.Runner == nil {
				return nil, errors.New("bambu: a command runner is required for FTPS upload")
			}
			return New(c, WithUploader(&CurlUploader{Runner: deps.Runner}), WithMQTT(&PahoMQTT{})), nil
		},
	})
}

type Config struct {
	IP, Serial, AccessCode string
}

func (c Config) Validate() error {
	if net.ParseIP(c.IP) == nil {
		return fmt.Errorf("bambu: %q is not an IP address", c.IP)
	}
	if strings.TrimSpace(c.Serial) == "" {
		return errors.New("bambu: serial number is required")
	}
	if c.AccessCode == "" {
		return errors.New("bambu: LAN access code is required")
	}
	return nil
}

// Uploader puts a file in the root of the printer's SD card. Implementations map
// failures to printer.ErrUnreachable, ErrAuth or ErrNoStorage.
type Uploader interface {
	Upload(ctx context.Context, c Config, localPath, remoteName string) error
	List(ctx context.Context, c Config) error // connects and logs in; used by Test
}

// MQTT sends commands to device/<serial>/request and reads device/<serial>/report.
type MQTT interface {
	Publish(ctx context.Context, c Config, payload []byte) error
	// Report asks for a full status push and returns the "print" object of the reply.
	Report(ctx context.Context, c Config) (Report, error)
}

// Report is the subset of the printer's push_status message we use.
type Report struct {
	GcodeState  string  `json:"gcode_state"` // IDLE, PREPARE, RUNNING, PAUSE, FINISH, FAILED
	Percent     float64 `json:"mc_percent"`
	Layer       int     `json:"layer_num"`
	TotalLayer  int     `json:"total_layer_num"`
	PrintError  int     `json:"print_error"`
	SubtaskName string  `json:"subtask_name"`
}

type Option func(*Printer)

func WithUploader(u Uploader) Option { return func(p *Printer) { p.ftp = u } }
func WithMQTT(m MQTT) Option         { return func(p *Printer) { p.mqtt = m } }

type Printer struct {
	cfg  Config
	ftp  Uploader
	mqtt MQTT

	mu       sync.Mutex
	uploaded map[string]bool
}

func New(c Config, opts ...Option) *Printer {
	p := &Printer{cfg: c, uploaded: map[string]bool{}}
	for _, o := range opts {
		o(p)
	}
	return p
}

func (p *Printer) Capabilities() printer.Capabilities {
	return printer.Capabilities{
		Model:       "Bambu Lab A1",
		BuildVolume: printer.Volume{X: 256, Y: 256, Z: 256},
		BedShape:    "rect",
		JobFormat:   ".gcode.3mf",
		Slicer:      "bambustudio",
		Features:    []printer.Feature{printer.FeatureStatus, printer.FeatureCancel, printer.FeaturePause},
	}
}

// Test checks FTPS login and the MQTT connection.
func (p *Printer) Test(ctx context.Context) error {
	if err := p.ftp.List(ctx, p.cfg); err != nil {
		return err
	}
	_, err := p.mqtt.Report(ctx, p.cfg)
	return err
}

// Diagnose reports FTPS and MQTT separately for the settings page.
func (p *Printer) Diagnose(ctx context.Context) []printer.Check {
	var out []printer.Check
	ftpErr := p.ftp.List(ctx, p.cfg)
	out = append(out, result("FTPS (port 990)", ftpErr, "logged in"))
	_, mqttErr := p.mqtt.Report(ctx, p.cfg)
	out = append(out, result("MQTT (port 8883)", mqttErr, "connected, status received"))
	return out
}

func result(name string, err error, ok string) printer.Check {
	if err == nil {
		return printer.Check{Name: name, OK: true, Message: ok}
	}
	return printer.Check{Name: name, Message: hint(err)}
}

func hint(err error) string {
	switch {
	case errors.Is(err, printer.ErrAuth):
		return "Not authorized: check the access code, and that LAN Only mode and Developer Mode are on"
	case errors.Is(err, printer.ErrUnreachable):
		return "Unreachable: check the IP, that the printer is on, and that the container can reach your LAN"
	case errors.Is(err, printer.ErrNoStorage):
		return "No SD card, or the card is full"
	}
	return err.Error()
}

func (p *Printer) Upload(ctx context.Context, job printer.Job) (printer.Remote, error) {
	name := path.Base(strings.ReplaceAll(job.Name, " ", "_"))
	if !strings.HasSuffix(name, ".gcode.3mf") {
		return printer.Remote{}, fmt.Errorf("bambu: the A1 prints .gcode.3mf files, got %q", job.Name)
	}
	if err := p.ftp.Upload(ctx, p.cfg, job.Path, name); err != nil {
		return printer.Remote{}, err
	}
	p.mu.Lock()
	p.uploaded[name] = true
	p.mu.Unlock()
	return printer.Remote{ID: name}, nil
}

// StartPayload is the "print project file" command current A1 firmware accepts.
func StartPayload(name string) []byte {
	b, _ := json.Marshal(map[string]any{"print": map[string]any{
		"sequence_id":    "0",
		"command":        "project_file",
		"param":          "Metadata/plate_1.gcode",
		"project_id":     "0",
		"profile_id":     "0",
		"task_id":        "0",
		"subtask_id":     "0",
		"subtask_name":   strings.TrimSuffix(name, ".gcode.3mf"),
		"url":            "file:///sdcard/" + name,
		"md5":            "",
		"timelapse":      false,
		"bed_type":       "auto",
		"bed_levelling":  true,
		"flow_cali":      false,
		"vibration_cali": true,
		"layer_inspect":  false,
		"use_ams":        false,
		"ams_mapping":    []int{},
	}})
	return b
}

func commandPayload(cmd string) []byte {
	b, _ := json.Marshal(map[string]any{"print": map[string]any{"sequence_id": "0", "command": cmd, "param": ""}})
	return b
}

func (p *Printer) Start(ctx context.Context, r printer.Remote) error {
	p.mu.Lock()
	ok := p.uploaded[r.ID]
	p.mu.Unlock()
	if !ok {
		return fmt.Errorf("%s: %w", r.ID, printer.ErrNotUploaded)
	}
	if st, err := p.Status(ctx); err == nil && (st.State == printer.Printing || st.State == printer.Paused) {
		return printer.ErrBusy
	}
	return p.mqtt.Publish(ctx, p.cfg, StartPayload(r.ID))
}

func (p *Printer) Status(ctx context.Context) (printer.Status, error) {
	rep, err := p.mqtt.Report(ctx, p.cfg)
	if err != nil {
		return printer.Status{State: printer.Offline, Message: hint(err)}, err
	}
	st := printer.Status{Progress: rep.Percent / 100, Layer: rep.Layer, TotalLayers: rep.TotalLayer}
	switch rep.GcodeState {
	case "RUNNING", "PREPARE", "SLICING":
		st.State = printer.Printing
	case "PAUSE":
		st.State = printer.Paused
	case "FINISH":
		st.State = printer.Finished
	case "FAILED":
		st.State = printer.Failed
		st.Message = fmt.Sprintf("print error %d", rep.PrintError)
	default:
		st.State = printer.Idle
	}
	return st, nil
}

func (p *Printer) Cancel(ctx context.Context) error {
	st, err := p.Status(ctx)
	if err != nil {
		return err
	}
	if st.State != printer.Printing && st.State != printer.Paused {
		return nil // nothing to cancel
	}
	return p.mqtt.Publish(ctx, p.cfg, commandPayload("stop"))
}

// Pause and Resume are extras the A1 supports.
func (p *Printer) Pause(ctx context.Context) error {
	return p.mqtt.Publish(ctx, p.cfg, commandPayload("pause"))
}
func (p *Printer) Resume(ctx context.Context) error {
	return p.mqtt.Publish(ctx, p.cfg, commandPayload("resume"))
}
