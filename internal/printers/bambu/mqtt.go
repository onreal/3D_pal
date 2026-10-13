package bambu

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"

	"github.com/margarit/printpipe/internal/printer"
)

// PahoMQTT talks to the printer over one MQTT connection per printer, shared by every
// call and kept open while it is in use. The A1's broker takes only a few clients and
// stops answering when clients keep reconnecting, so status comes from the reports the
// printer pushes on that connection (about once a second while printing), not from a new
// connection and a full status request per poll.
type PahoMQTT struct {
	Port int // 8883 when zero
	// IdleClose closes a connection nobody used for this long, freeing the printer's
	// slot; DefaultIdleClose when zero.
	IdleClose time.Duration
}

var (
	// Fresh is how recent the last report must be for Report to answer from it without asking.
	Fresh = 30 * time.Second
	// PushallEvery limits full status requests: the printer is slow to answer them.
	PushallEvery = 20 * time.Second
	// RetryAfter is the pause after a failed connect before the next try; calls in the
	// meantime get the same error at once, so a struggling printer isn't hammered.
	RetryAfter = 10 * time.Second
)

// DefaultIdleClose is how long an unused connection stays open.
const DefaultIdleClose = 2 * time.Minute

var (
	connsMu sync.Mutex
	conns   = map[string]*conn{}
)

// conn is the shared connection to one printer, and what it last reported.
type conn struct {
	m   PahoMQTT
	cfg Config

	dial     sync.Mutex // one connect at a time
	mu       sync.Mutex
	cl       paho.Client // nil when not connected
	state    map[string]any
	heard    time.Time // last message from the printer
	pushall  time.Time // last full status request
	used     time.Time
	failed   time.Time
	failErr  error
	changed  chan struct{} // closed and replaced on every report
	watchers map[*watcher]struct{}
}

type watcher struct{ fn func(map[string]any) }

func (m PahoMQTT) conn(c Config) *conn {
	key := fmt.Sprintf("%s:%d/%s/%x", c.IP, m.Port, c.Serial, sha256.Sum256([]byte(c.AccessCode)))
	connsMu.Lock()
	defer connsMu.Unlock()
	cn := conns[key]
	if cn == nil {
		cn = &conn{m: m, cfg: c, changed: make(chan struct{}), watchers: map[*watcher]struct{}{}}
		conns[key] = cn
	}
	return cn
}

// client returns the open connection, connecting when there is none. Connecting holds
// dial, not mu: the report handler takes mu, and paho may not deliver the subscription's
// acknowledgement while a handler is blocked.
func (cn *conn) client(ctx context.Context) (paho.Client, error) {
	cn.dial.Lock()
	defer cn.dial.Unlock()
	cn.mu.Lock()
	cn.used = time.Now()
	cl, failErr, failed := cn.cl, cn.failErr, cn.failed
	cn.mu.Unlock()
	if cl != nil && cl.IsConnectionOpen() {
		return cl, nil
	}
	if failErr != nil && time.Since(failed) < RetryAfter {
		return nil, failErr
	}
	cl, err := cn.m.connect(ctx, cn.cfg, cn.lost)
	if err == nil {
		if err = wait(ctx, cl.Subscribe("device/"+cn.cfg.Serial+"/report", 0, cn.receive)); err != nil {
			err = mapMQTT(err)
			cl.Disconnect(0)
		}
	}
	cn.mu.Lock()
	defer cn.mu.Unlock()
	if err != nil {
		if ctx.Err() == nil { // a caller's short deadline says nothing about the printer
			cn.failed, cn.failErr = time.Now(), err
		}
		return nil, err
	}
	// A new connection starts without status, and may ask for it at once.
	cn.cl, cn.failErr, cn.state, cn.pushall = cl, nil, nil, time.Time{}
	go cn.closeWhenIdle(cl)
	return cl, nil
}

func (cn *conn) lost(paho.Client, error) {
	cn.mu.Lock()
	cn.cl, cn.state = nil, nil
	cn.mu.Unlock()
}

func (cn *conn) closeWhenIdle(cl paho.Client) {
	idle := cn.m.IdleClose
	if idle == 0 {
		idle = DefaultIdleClose
	}
	for {
		time.Sleep(idle / 4)
		cn.mu.Lock()
		if cn.cl != cl {
			cn.mu.Unlock()
			return
		}
		if time.Since(cn.used) > idle && len(cn.watchers) == 0 {
			cn.cl, cn.state = nil, nil
			cn.mu.Unlock()
			cl.Disconnect(250)
			return
		}
		cn.mu.Unlock()
	}
}

// receive merges a report into the state: the A1 sends a full report after "pushall" and
// only the fields that changed otherwise.
func (cn *conn) receive(_ paho.Client, msg paho.Message) {
	var env struct {
		Print map[string]any `json:"print"`
	}
	if json.Unmarshal(msg.Payload(), &env) != nil || env.Print == nil {
		return
	}
	cn.mu.Lock()
	cn.heard = time.Now()
	if cmd, _ := env.Print["command"].(string); cmd == "push_status" || cmd == "" {
		if cn.state == nil {
			cn.state = map[string]any{}
		}
		for k, v := range env.Print {
			cn.state[k] = v
		}
	}
	close(cn.changed)
	cn.changed = make(chan struct{})
	ws := make([]*watcher, 0, len(cn.watchers))
	for w := range cn.watchers {
		ws = append(ws, w)
	}
	cn.mu.Unlock()
	for _, w := range ws {
		w.fn(env.Print)
	}
}

func (cn *conn) publish(ctx context.Context, cl paho.Client, payload []byte) error {
	if err := wait(ctx, cl.Publish("device/"+cn.cfg.Serial+"/request", 0, false, payload)); err != nil {
		cn.mu.Lock()
		if cn.cl == cl { // a dead connection: drop it so the next call reconnects
			cn.cl, cn.state = nil, nil
		}
		cn.mu.Unlock()
		cl.Disconnect(0)
		return mapMQTT(err)
	}
	return nil
}

func (m PahoMQTT) connect(ctx context.Context, c Config, lost paho.ConnectionLostHandler) (paho.Client, error) {
	port := m.Port
	if port == 0 {
		port = 8883
	}
	id := make([]byte, 6)
	rand.Read(id)
	opts := paho.NewClientOptions().
		AddBroker(fmt.Sprintf("tls://%s:%d", c.IP, port)).
		SetClientID("printpipe-" + hex.EncodeToString(id)).
		SetUsername("bblp").
		SetPassword(c.AccessCode).
		// The printer presents a self-signed certificate.
		SetTLSConfig(&tls.Config{InsecureSkipVerify: true}).
		SetConnectTimeout(10 * time.Second).
		SetKeepAlive(30 * time.Second).
		SetConnectionLostHandler(lost).
		SetAutoReconnect(false).
		SetConnectRetry(false)
	cl := paho.NewClient(opts)
	tok := cl.Connect()
	if err := wait(ctx, tok); err != nil {
		return nil, mapMQTT(err)
	}
	return cl, nil
}

func wait(ctx context.Context, tok paho.Token) error {
	select {
	case <-tok.Done():
		return tok.Error()
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(15 * time.Second):
		return fmt.Errorf("timed out: %w", printer.ErrUnreachable)
	}
}

func mapMQTT(err error) error {
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "not authorized"), strings.Contains(msg, "bad user name or password"):
		return fmt.Errorf("MQTT login: %w", printer.ErrAuth)
	case errors.Is(err, printer.ErrUnreachable),
		strings.Contains(msg, "connection refused"), strings.Contains(msg, "i/o timeout"),
		strings.Contains(msg, "no route to host"), strings.Contains(msg, "network is unreachable"),
		strings.Contains(msg, "timeout"):
		return fmt.Errorf("MQTT connect: %w", printer.ErrUnreachable)
	}
	return fmt.Errorf("MQTT: %w", err)
}

func (m PahoMQTT) Publish(ctx context.Context, c Config, payload []byte) error {
	cn := m.conn(c)
	cl, err := cn.client(ctx)
	if err != nil {
		return err
	}
	return cn.publish(ctx, cl, payload)
}

// ReplyWait is how long Command waits for the printer's answer.
var ReplyWait = 15 * time.Second

func (m PahoMQTT) Command(ctx context.Context, c Config, payload []byte) (Reply, error) {
	var sent struct {
		Print struct {
			Command string `json:"command"`
		} `json:"print"`
	}
	json.Unmarshal(payload, &sent)
	cn := m.conn(c)
	cl, err := cn.client(ctx)
	if err != nil {
		return Reply{}, err
	}
	got := make(chan Reply, 1)
	// Listen before publishing, so the answer cannot be missed.
	w := &watcher{fn: func(p map[string]any) {
		var r Reply
		b, _ := json.Marshal(p)
		if json.Unmarshal(b, &r) == nil && r.Command == sent.Print.Command && r.Result != "" {
			select {
			case got <- r:
			default:
			}
		}
	}}
	cn.mu.Lock()
	cn.watchers[w] = struct{}{}
	cn.mu.Unlock()
	defer func() {
		cn.mu.Lock()
		delete(cn.watchers, w)
		cn.mu.Unlock()
	}()
	if err := cn.publish(ctx, cl, payload); err != nil {
		return Reply{}, err
	}
	select {
	case r := <-got:
		return r, nil
	case <-ctx.Done():
		return Reply{}, ctx.Err()
	case <-time.After(ReplyWait):
		return Reply{}, ErrNoReply
	}
}

// Report returns the printer's status from its recent reports, asking for a full one only
// when there is none or the printer went quiet.
func (m PahoMQTT) Report(ctx context.Context, c Config) (Report, error) {
	cn := m.conn(c)
	cl, err := cn.client(ctx)
	if err != nil {
		return Report{}, err
	}
	deadline := time.After(10 * time.Second)
	for {
		cn.mu.Lock()
		rep, known := cn.report()
		if known && time.Since(cn.heard) < Fresh {
			cn.mu.Unlock()
			return rep, nil
		}
		ask := time.Since(cn.pushall) >= PushallEvery
		if ask {
			cn.pushall = time.Now()
		}
		changed := cn.changed
		cn.mu.Unlock()
		if ask {
			push, _ := json.Marshal(map[string]any{"pushing": map[string]any{"sequence_id": "0", "command": "pushall"}})
			if err := cn.publish(ctx, cl, push); err != nil {
				return Report{}, err
			}
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return Report{}, ctx.Err()
		case <-deadline:
			if known { // quiet, but what it said last still stands
				return rep, nil
			}
			return Report{}, errors.New("MQTT: connected but the printer sent no status; check the serial number")
		}
	}
}

// report decodes the merged state; known is false until the printer told its print state.
func (cn *conn) report() (Report, bool) {
	if cn.state == nil || cn.state["gcode_state"] == nil {
		return Report{}, false
	}
	var r Report
	b, _ := json.Marshal(cn.state)
	json.Unmarshal(b, &r)
	return r, r.GcodeState != ""
}
