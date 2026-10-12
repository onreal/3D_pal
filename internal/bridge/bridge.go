// Package bridge relays an MCP client that only speaks stdio (Claude Desktop) to the
// server's Streamable HTTP endpoint, so it joins the same server as every other session.
// It passes JSON-RPC messages through untouched: the client's own initialize, with its
// name and version, reaches the server.
package bridge

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Header marks the bridge's requests, so the server lists the session as bridged.
const Header = "X-Printpipe-Bridge"

type headerTransport struct {
	base    http.RoundTripper
	version string
}

func (t headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set(Header, t.version)
	r.Header.Set("User-Agent", "printpipe-bridge/"+t.version)
	return t.base.RoundTrip(r)
}

// Run relays messages between in/out (the stdio client) and endpoint until either side closes.
func Run(ctx context.Context, in io.ReadCloser, out io.WriteCloser, endpoint, version string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	down, err := (&mcp.IOTransport{Reader: in, Writer: out}).Connect(ctx)
	if err != nil {
		return err
	}
	defer down.Close()
	up, err := (&mcp.StreamableClientTransport{
		Endpoint:   endpoint,
		HTTPClient: &http.Client{Transport: headerTransport{base: http.DefaultTransport, version: version}},
	}).Connect(ctx)
	if err != nil {
		return err
	}
	defer up.Close() // ends the HTTP session (DELETE), so the server lists it as closed

	var once sync.Once
	var first error
	pump := func(from, to mcp.Connection) {
		for {
			msg, err := from.Read(ctx)
			if err == nil {
				err = to.Write(ctx, msg)
			}
			if err != nil {
				once.Do(func() { first = err; cancel() })
				return
			}
		}
	}
	go pump(down, up)
	go pump(up, down)
	<-ctx.Done()
	if first == nil || errors.Is(first, io.EOF) || errors.Is(first, context.Canceled) {
		return nil
	}
	return first
}
