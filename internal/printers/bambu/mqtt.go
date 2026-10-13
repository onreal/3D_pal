package bambu

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"

	"github.com/margarit/printpipe/internal/printer"
)

// PahoMQTT connects per call: the printer allows few concurrent clients, and tool
// calls are rare, so a short-lived connection is simpler than keeping one open.
type PahoMQTT struct {
	Port int // 8883 when zero
}

func (m PahoMQTT) connect(ctx context.Context, c Config) (paho.Client, error) {
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
	cl, err := m.connect(ctx, c)
	if err != nil {
		return err
	}
	defer cl.Disconnect(250)
	if err := wait(ctx, cl.Publish("device/"+c.Serial+"/request", 0, false, payload)); err != nil {
		return mapMQTT(err)
	}
	return nil
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
	cl, err := m.connect(ctx, c)
	if err != nil {
		return Reply{}, err
	}
	defer cl.Disconnect(250)
	got := make(chan Reply, 1)
	// Subscribe before publishing, so the answer cannot be missed.
	sub := cl.Subscribe("device/"+c.Serial+"/report", 0, func(_ paho.Client, msg paho.Message) {
		var env struct {
			Print *Reply `json:"print"`
		}
		if json.Unmarshal(msg.Payload(), &env) == nil && env.Print != nil && env.Print.Command == sent.Print.Command && env.Print.Result != "" {
			select {
			case got <- *env.Print:
			default:
			}
		}
	})
	if err := wait(ctx, sub); err != nil {
		return Reply{}, mapMQTT(err)
	}
	if err := wait(ctx, cl.Publish("device/"+c.Serial+"/request", 0, false, payload)); err != nil {
		return Reply{}, mapMQTT(err)
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

func (m PahoMQTT) Report(ctx context.Context, c Config) (Report, error) {
	cl, err := m.connect(ctx, c)
	if err != nil {
		return Report{}, err
	}
	defer cl.Disconnect(250)
	got := make(chan Report, 1)
	sub := cl.Subscribe("device/"+c.Serial+"/report", 0, func(_ paho.Client, msg paho.Message) {
		var env struct {
			Print *Report `json:"print"`
		}
		if json.Unmarshal(msg.Payload(), &env) == nil && env.Print != nil && env.Print.GcodeState != "" {
			select {
			case got <- *env.Print:
			default:
			}
		}
	})
	if err := wait(ctx, sub); err != nil {
		return Report{}, mapMQTT(err)
	}
	push, _ := json.Marshal(map[string]any{"pushing": map[string]any{"sequence_id": "0", "command": "pushall"}})
	if err := wait(ctx, cl.Publish("device/"+c.Serial+"/request", 0, false, push)); err != nil {
		return Report{}, mapMQTT(err)
	}
	select {
	case r := <-got:
		return r, nil
	case <-ctx.Done():
		return Report{}, ctx.Err()
	case <-time.After(10 * time.Second):
		return Report{}, errors.New("MQTT: connected but the printer sent no status; check the serial number")
	}
}
