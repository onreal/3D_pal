package bambu_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	mqtt "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/mochi-mqtt/server/v2/packets"

	"github.com/margarit/printpipe/internal/printer"
	"github.com/margarit/printpipe/internal/printers/bambu"
)

// fakeA1 is an MQTT broker over TLS that answers like the printer: user bblp with the
// access code, a status report after "pushall", and project_file starts a print.
type fakeA1 struct {
	port     int
	mu       sync.Mutex
	state    string
	commands []string
	verify   bool // reject print commands as unsigned, like firmware outside Developer Mode
	silent   bool // never answer commands
}

func selfSigned(t *testing.T) tls.Certificate {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "BBL Device"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func startFakeA1(t *testing.T, serial, code string) *fakeA1 {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()

	a := &fakeA1{port: port, state: "IDLE"}
	s := mqtt.New(&mqtt.Options{InlineClient: true})
	s.AddHook(new(auth.Hook), &auth.Options{Ledger: &auth.Ledger{Auth: auth.AuthRules{
		{Username: "bblp", Password: auth.RString(code), Allow: true},
	}}})
	s.AddListener(listeners.NewTCP(listeners.Config{ID: "tls", Address: l.Addr().String(),
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{selfSigned(t)}}}))
	s.Subscribe("device/"+serial+"/request", 1, func(_ *mqtt.Client, _ packets.Subscription, pk packets.Packet) {
		var msg struct {
			Print   *struct{ Command string } `json:"print"`
			Pushing *struct{ Command string } `json:"pushing"`
		}
		json.Unmarshal(pk.Payload, &msg)
		a.mu.Lock()
		switch {
		case msg.Pushing != nil && msg.Pushing.Command == "pushall":
			rep, _ := json.Marshal(map[string]any{"print": map[string]any{"command": "push_status", "gcode_state": a.state, "mc_percent": 42, "layer_num": 7, "total_layer_num": 100}})
			a.mu.Unlock()
			s.Publish("device/"+serial+"/report", rep, false, 0)
			return
		case msg.Print != nil:
			a.commands = append(a.commands, msg.Print.Command)
			if msg.Print.Command == "project_file" {
				reply := map[string]any{"command": "project_file", "sequence_id": "0", "result": "success", "reason": ""}
				if a.verify {
					reply = map[string]any{"command": "project_file", "sequence_id": "0", "result": "failed", "reason": "mqtt message verify failed", "err_code": 84033543}
				} else {
					a.state = "RUNNING"
				}
				if !a.silent {
					b, _ := json.Marshal(map[string]any{"print": reply})
					a.mu.Unlock()
					s.Publish("device/"+serial+"/report", b, false, 0)
					return
				}
			}
			if msg.Print.Command == "stop" {
				a.state = "IDLE"
			}
		}
		a.mu.Unlock()
	})
	go s.Serve()
	t.Cleanup(func() { s.Close() })
	time.Sleep(50 * time.Millisecond)
	return a
}

func TestPahoAgainstAPrinterLikeBroker(t *testing.T) {
	a := startFakeA1(t, "SER123", "12345678")
	m := bambu.PahoMQTT{Port: a.port}
	c := bambu.Config{IP: "127.0.0.1", Serial: "SER123", AccessCode: "12345678"}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	rep, err := m.Report(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if rep.GcodeState != "IDLE" || rep.Percent != 42 || rep.TotalLayer != 100 {
		t.Fatalf("%+v", rep)
	}

	p := bambu.New(c, bambu.WithUploader(&fakeFTPS{}), bambu.WithMQTT(m))
	r, err := p.Upload(ctx, printer.Job{Path: "x", Name: "cube_v1.gcode.3mf"})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Start(ctx, r); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	st, err := p.Status(ctx)
	if err != nil || st.State != printer.Printing || st.Layer != 7 {
		t.Fatalf("%+v %v", st, err)
	}
	if err := p.Pause(ctx); err != nil {
		t.Fatal(err)
	}
	if err := p.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	if err := p.Cancel(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	a.mu.Lock()
	got := strings.Join(a.commands, ",")
	a.mu.Unlock()
	if got != "project_file,pause,resume,stop" {
		t.Fatalf("commands %s", got)
	}
}

func TestPahoUnsignedStartIsRefused(t *testing.T) {
	a := startFakeA1(t, "SER123", "12345678")
	a.verify = true
	c := bambu.Config{IP: "127.0.0.1", Serial: "SER123", AccessCode: "12345678"}
	p := bambu.New(c, bambu.WithUploader(&fakeFTPS{}), bambu.WithMQTT(bambu.PahoMQTT{Port: a.port}))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	r, _ := p.Upload(ctx, printer.Job{Path: "x", Name: "cube.gcode.3mf"})
	err := p.Start(ctx, r)
	if !errors.Is(err, printer.ErrRefused) || !strings.Contains(err.Error(), "Developer Mode") {
		t.Fatalf("got %v", err)
	}
}

func TestPahoCommandWithoutAnswer(t *testing.T) {
	defer func(d time.Duration) { bambu.ReplyWait = d }(bambu.ReplyWait)
	bambu.ReplyWait = 300 * time.Millisecond
	a := startFakeA1(t, "SER123", "12345678")
	a.silent = true
	c := bambu.Config{IP: "127.0.0.1", Serial: "SER123", AccessCode: "12345678"}
	_, err := bambu.PahoMQTT{Port: a.port}.Command(context.Background(), c, bambu.StartPayload("x.gcode.3mf"))
	if !errors.Is(err, bambu.ErrNoReply) {
		t.Fatalf("got %v", err)
	}
}

func TestPahoWrongAccessCodeIsErrAuth(t *testing.T) {
	a := startFakeA1(t, "SER123", "12345678")
	_, err := bambu.PahoMQTT{Port: a.port}.Report(context.Background(), bambu.Config{IP: "127.0.0.1", Serial: "SER123", AccessCode: "wrong"})
	if !errors.Is(err, printer.ErrAuth) {
		t.Fatalf("want ErrAuth, got %v", err)
	}
}

func TestPahoNoPrinterIsErrUnreachable(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	port := l.Addr().(*net.TCPAddr).Port
	l.Close() // nothing listens here
	_, err := bambu.PahoMQTT{Port: port}.Report(context.Background(), bambu.Config{IP: "127.0.0.1", Serial: "S", AccessCode: "x"})
	if !errors.Is(err, printer.ErrUnreachable) {
		t.Fatalf("want ErrUnreachable, got %v", err)
	}
}

func TestPahoWrongSerialExplainsItself(t *testing.T) {
	a := startFakeA1(t, "SER123", "12345678")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := bambu.PahoMQTT{Port: a.port}.Report(ctx, bambu.Config{IP: "127.0.0.1", Serial: "OTHER", AccessCode: "12345678"})
	if err == nil || !strings.Contains(err.Error(), "serial") {
		t.Fatalf("got %v", err)
	}
}
