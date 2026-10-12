package events_test

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/margarit/printpipe/internal/events"
)

func TestEventsStreamModelUpdate(t *testing.T) {
	hub := events.NewHub()
	srv := httptest.NewServer(hub)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	for hub.Subscribers() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	hub.Publish(events.Model, map[string]any{"project": "cube", "version": 2})
	sc := bufio.NewScanner(resp.Body)
	var got []string
	for sc.Scan() {
		got = append(got, sc.Text())
		if strings.HasPrefix(sc.Text(), "data: ") {
			break
		}
	}
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, "event: model\ndata: {\"project\":\"cube\",\"version\":2}") {
		t.Fatalf("stream: %q", joined)
	}
}
