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
	reg *sessions.Registry
	ids sync.Map // *mcp.ServerSession → session ID
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

func (t *tracker) initialized(_ context.Context, req *mcp.InitializedRequest) {
	ss := req.Session
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
	}()
}

// middleware records tool calls on the session and puts the caller in the context.
func (t *tracker) middleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		call, ok := req.(*mcp.CallToolRequest)
		if !ok || call.Session == nil {
			return next(ctx, method, req)
		}
		id := t.id(call.Session)
		client := "unknown client"
		if p := call.Session.InitializeParams(); p != nil && p.ClientInfo != nil && p.ClientInfo.Name != "" {
			client = p.ClientInfo.Name
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
