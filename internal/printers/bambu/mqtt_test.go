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
	"strconv"
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
// access code, a full status report after "pushall", a report of just the changed fields
// when its state changes, and project_file starts a print.
type fakeA1 struct {
	port     int
	mu       sync.Mutex
	state    string
	commands []string
	verify   bool // reject print commands as unsigned, like firmware outside Developer Mode
	silent   bool // never answer commands
	pushalls int
	connects int
	push     func(fields map[string]any) // sends a report of changed fields, as the printer does
}

// countConnects counts client connections to the fake printer.
type countConnects struct {
	mqtt.HookBase
	a *fakeA1
}

func (h *countConnects) ID() string { return "count-connects" }
func (h *countConnects) Provides(b byte) bool {
	return b == mqtt.OnConnect
}
func (h *countConnects) OnConnect(*mqtt.Client, packets.Packet) error {
	h.a.mu.Lock()
	h.a.connects++
	h.a.mu.Unlock()
	return nil
}

func (a *fakeA1) counts() (connects, pushalls int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.connects, a.pushalls
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
	s.AddHook(&countConnects{a: a}, nil)
	a.push = func(fields map[string]any) {
		fields["command"] = "push_status"
		b, _ := json.Marshal(map[string]any{"print": fields})
		s.Publish("device/"+serial+"/report", b, false, 0)
	}
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
			a.pushalls++
			rep, _ := json.Marshal(map[string]any{"print": map[string]any{"command": "push_status", "gcode_state": a.state, "mc_percent": 42, "layer_num": 7, "total_layer_num": 100, "mc_remaining_time": 12, "subtask_name": "cube_v1"}})
			a.mu.Unlock()
			s.Publish("device/"+serial+"/report", rep, false, 0)
			return
		case msg.Print != nil:
			a.commands = append(a.commands, msg.Print.Command)
			before := a.state
			defer func() {
				a.mu.Lock()
				st := a.state
				a.mu.Unlock()
				if st != before {
					delta, _ := json.Marshal(map[string]any{"print": map[string]any{"command": "push_status", "gcode_state": st}})
					s.Publish("device/"+serial+"/report", delta, false, 0)
				}
			}()
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
	if err != nil || st.State != printer.Printing || st.Layer != 7 || st.Remaining != 720 || st.JobName != "cube_v1" {
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

func TestPahoSharesOneConnectionAndListensForReports(t *testing.T) {
	a := startFakeA1(t, "SHARE1", "12345678")
	m := bambu.PahoMQTT{Port: a.port}
	c := bambu.Config{IP: "127.0.0.1", Serial: "SHARE1", AccessCode: "12345678"}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for i := 0; i < 5; i++ {
		if _, err := m.Report(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	if conns, pushes := a.counts(); conns != 1 || pushes != 1 {
		t.Fatalf("5 status reads made %d connections and %d full status requests, want 1 and 1", conns, pushes)
	}
	// The printer reports only what changed; the status merges it in without asking again.
	a.push(map[string]any{"layer_num": 8, "mc_percent": 50})
	deadline := time.Now().Add(5 * time.Second)
	for {
		rep, err := m.Report(ctx, c)
		if err != nil {
			t.Fatal(err)
		}
		if rep.Layer == 8 && rep.Percent == 50 && rep.TotalLayer == 100 && rep.GcodeState == "IDLE" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("delta not merged: %+v", rep)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := m.Publish(ctx, c, []byte(`{"print":{"command":"pause"}}`)); err != nil {
		t.Fatal(err)
	}
	if conns, pushes := a.counts(); conns != 1 || pushes != 1 {
		t.Fatalf("%d connections, %d full status requests", conns, pushes)
	}
}

func TestPahoClosesAnIdleConnectionAndReconnects(t *testing.T) {
	a := startFakeA1(t, "IDLE1", "12345678")
	m := bambu.PahoMQTT{Port: a.port, IdleClose: 200 * time.Millisecond}
	c := bambu.Config{IP: "127.0.0.1", Serial: "IDLE1", AccessCode: "12345678"}
	if _, err := m.Report(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	time.Sleep(600 * time.Millisecond)
	if _, err := m.Report(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if conns, _ := a.counts(); conns != 2 {
		t.Fatalf("connections %d, want 2 (closed when idle, then reopened)", conns)
	}
}

func TestPahoWaitsBeforeRetryingAFailedConnect(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	m := bambu.PahoMQTT{Port: port}
	c := bambu.Config{IP: "127.0.0.1", Serial: "RETRY1", AccessCode: "x"}
	if _, err := m.Report(context.Background(), c); !errors.Is(err, printer.ErrUnreachable) {
		t.Fatalf("got %v", err)
	}
	// Something listens now, but within RetryAfter the last error stands without a new try.
	l2, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Skip("port taken again")
	}
	defer l2.Close()
	accepted := make(chan struct{}, 1)
	go func() {
		if cn, err := l2.Accept(); err == nil {
			accepted <- struct{}{}
			cn.Close()
		}
	}()
	if _, err := m.Report(context.Background(), c); !errors.Is(err, printer.ErrUnreachable) {
		t.Fatalf("got %v", err)
	}
	select {
	case <-accepted:
		t.Fatal("reconnected within RetryAfter")
	case <-time.After(100 * time.Millisecond):
	}
}
