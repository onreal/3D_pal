// Package sessions tracks the agent sessions connected over MCP, so the viewer can show
// which client holds each one and follow its work.
package sessions

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Transports a session can arrive by.
const (
	HTTP   = "http"   // Streamable HTTP at /mcp (Claude Code, other HTTP clients)
	Bridge = "bridge" // printpipe bridge: a stdio client relayed to /mcp (Claude Desktop)
	Stdio  = "stdio"  // the container started by the client with docker run -i
)

type Info struct {
	ID          string    `json:"id"`
	Client      string    `json:"client"`         // clientInfo.name from initialize
	Version     string    `json:"client_version"` // clientInfo.version
	Title       string    `json:"client_title,omitempty"`
	Transport   string    `json:"transport"`
	RemoteAddr  string    `json:"remote_addr,omitempty"`
	UserAgent   string    `json:"user_agent,omitempty"`
	ConnectedAt time.Time `json:"connected_at"`
	LastSeen    time.Time `json:"last_seen"`
	Calls       int       `json:"calls"`
	LastTool    string    `json:"last_tool,omitempty"`
	Project     string    `json:"project,omitempty"`
	Open        bool      `json:"open"`
	ClosedAt    time.Time `json:"closed_at,omitempty"`
}

// Event types the registry publishes.
const (
	Opened  = "opened"
	Updated = "updated"
	Closed  = "closed"
)

type Registry struct {
	mu      sync.Mutex
	byID    map[string]*Info
	pending map[string]Info // HTTP details seen before the session finished initializing
	closers map[string]func()
	now     func() time.Time
	keep    int // closed sessions kept for the list
	// OnChange is told about every change: Opened, Updated or Closed.
	OnChange func(kind string, info Info)
}

func NewRegistry() *Registry {
	return &Registry{byID: map[string]*Info{}, pending: map[string]Info{}, closers: map[string]func(){}, now: time.Now, keep: 20}
}

// WithClock replaces the clock (tests).
func (r *Registry) WithClock(now func() time.Time) *Registry { r.now = now; return r }

func (r *Registry) notify(kind string, i *Info) {
	if r.OnChange != nil {
		r.OnChange(kind, *i)
	}
}

// Transport records how an HTTP session arrived; it may come before or after Open.
func (r *Registry) Transport(id, transport, remote, userAgent string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if i, ok := r.byID[id]; ok {
		i.Transport, i.RemoteAddr, i.UserAgent = transport, remote, userAgent
		r.notify(Updated, i)
		return
	}
	r.pending[id] = Info{Transport: transport, RemoteAddr: remote, UserAgent: userAgent}
}

// Open registers a session once its client has initialized. closer ends it on request.
func (r *Registry) Open(id, client, version, title, transport string, closer func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	i := &Info{ID: id, Client: client, Version: version, Title: title, Transport: transport, ConnectedAt: now, LastSeen: now, Open: true}
	if p, ok := r.pending[id]; ok {
		i.Transport, i.RemoteAddr, i.UserAgent = p.Transport, p.RemoteAddr, p.UserAgent
		delete(r.pending, id)
	}
	if i.Client == "" {
		i.Client = "unknown client"
	}
	r.byID[id] = i
	if closer != nil {
		r.closers[id] = closer
	}
	r.notify(Opened, i)
}

// Touch records a tool call on a session.
func (r *Registry) Touch(id, tool, project string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	i, ok := r.byID[id]
	if !ok || !i.Open {
		return
	}
	i.LastSeen = r.now()
	i.Calls++
	i.LastTool = tool
	if project != "" {
		i.Project = project
	}
	r.notify(Updated, i)
}

// Close marks a session closed; the newest closed ones stay listed for a while.
func (r *Registry) Close(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	i, ok := r.byID[id]
	if !ok || !i.Open {
		return
	}
	i.Open, i.ClosedAt = false, r.now()
	delete(r.closers, id)
	r.notify(Closed, i)
	r.prune()
}

func (r *Registry) prune() {
	var closed []*Info
	for _, i := range r.byID {
		if !i.Open {
			closed = append(closed, i)
		}
	}
	if len(closed) <= r.keep {
		return
	}
	sort.Slice(closed, func(a, b int) bool { return closed[a].ClosedAt.After(closed[b].ClosedAt) })
	for _, i := range closed[r.keep:] {
		delete(r.byID, i.ID)
	}
}

// End asks a session's transport to close it (an admin action). It reports whether the session was open.
func (r *Registry) End(id string) bool {
	r.mu.Lock()
	closer, ok := r.closers[id]
	r.mu.Unlock()
	if !ok {
		return false
	}
	closer()
	r.Close(id)
	return true
}

func (r *Registry) Get(id string) (Info, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	i, ok := r.byID[id]
	if !ok {
		return Info{}, false
	}
	return *i, true
}

// List returns open sessions first (newest activity first), then recently closed ones.
func (r *Registry) List() []Info {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Info, 0, len(r.byID))
	for _, i := range r.byID {
		out = append(out, *i)
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].Open != out[b].Open {
			return out[a].Open
		}
		if out[a].Open {
			return out[a].LastSeen.After(out[b].LastSeen)
		}
		return out[a].ClosedAt.After(out[b].ClosedAt)
	})
	return out
}

// Open sessions count.
func (r *Registry) OpenCount() int {
	n := 0
	for _, i := range r.List() {
		if i.Open {
			n++
		}
	}
	return n
}

type ctxKey struct{}

// Caller identifies the session behind a tool call.
type Caller struct {
	ID     string `json:"id"`
	Client string `json:"client"`
}

func WithCaller(ctx context.Context, c Caller) context.Context {
	return context.WithValue(ctx, ctxKey{}, c)
}

// From returns the session behind ctx, if a tool call started it.
func From(ctx context.Context) (Caller, bool) {
	c, ok := ctx.Value(ctxKey{}).(Caller)
	return c, ok
}
