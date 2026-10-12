package mcpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/margarit/printpipe/internal/sessions"
)

// tracker reports this server's sessions to the registry and tags each tool call's
// context with the session that made it.
type tracker struct {
	reg    *sessions.Registry
	ids    sync.Map // *mcp.ServerSession → session ID
	opened sync.Map // *mcp.ServerSession → struct{}
}

// id is the session's MCP session ID over HTTP; a stdio session has none, so it gets one.
func (t *tracker) id(ss *mcp.ServerSession) string {
	if v, ok := t.ids.Load(ss); ok {
		return v.(string)
	}
	id := ss.ID()
	if id == "" {
		b := make([]byte, 6)
		rand.Read(b)
		id = "stdio-" + hex.EncodeToString(b)
	}
	v, _ := t.ids.LoadOrStore(ss, id)
	return v.(string)
}

// initialized is a fallback: newer protocol versions may not send the notification, so
// sessions are opened when their initialize request succeeds (see middleware).
func (t *tracker) initialized(_ context.Context, req *mcp.InitializedRequest) { t.open(req.Session) }

// open registers a session once, and watches for it to close.
func (t *tracker) open(ss *mcp.ServerSession) {
	if ss == nil {
		return
	}
	if _, loaded := t.opened.LoadOrStore(ss, struct{}{}); loaded {
		return
	}
	id := t.id(ss)
	var name, version, title string
	if p := ss.InitializeParams(); p != nil && p.ClientInfo != nil {
		name, version, title = p.ClientInfo.Name, p.ClientInfo.Version, p.ClientInfo.Title
	}
	transport := sessions.HTTP
	if ss.ID() == "" {
		transport = sessions.Stdio
	}
	if t.reg != nil {
		t.reg.Open(id, name, version, title, transport, func() { ss.Close() })
	}
	go func() {
		ss.Wait()
		if t.reg != nil {
			t.reg.Close(id)
		}
		t.ids.Delete(ss)
		t.opened.Delete(ss)
	}()
}

func clientInfo(ss *mcp.ServerSession) (name, version, title string) {
	if ss == nil {
		return
	}
	if p := ss.InitializeParams(); p != nil && p.ClientInfo != nil {
		return p.ClientInfo.Name, p.ClientInfo.Version, p.ClientInfo.Title
	}
	return
}

// middleware lists a session once its client has said who it is (or at its first tool
// call), records tool calls on it, and puts the caller in the context. Sessions that only
// negotiate and leave are never listed.
func (t *tracker) middleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		ss, _ := req.GetSession().(*mcp.ServerSession)
		call, isCall := req.(*mcp.CallToolRequest)
		if !isCall || ss == nil {
			res, err := next(ctx, method, req)
			if name, version, title := clientInfo(ss); name != "" {
				t.open(ss)
				if t.reg != nil {
					t.reg.Identify(t.id(ss), name, version, title)
				}
			}
			return res, err
		}
		t.open(ss)
		id := t.id(ss)
		name, version, title := clientInfo(ss)
		client := name
		if client == "" {
			client = "unknown client"
		} else if t.reg != nil {
			t.reg.Identify(id, name, version, title)
		}
		var args struct {
			Project string `json:"project"`
		}
		if call.Params != nil {
			json.Unmarshal(call.Params.Arguments, &args)
			if t.reg != nil {
				t.reg.Touch(id, call.Params.Name, args.Project)
			}
		}
		return next(sessions.WithCaller(ctx, sessions.Caller{ID: id, Client: client}), method, req)
	}
}
