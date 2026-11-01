package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/inkandale/innkeeper/internal/librarycall"
	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/internal/tier"
	"github.com/inkandale/innkeeper/protocol"
)

// uiEventBuffer is how many events wait for a slow browser before the
// plugin's notifications block.
const uiEventBuffer = 256

type uiCallIn struct {
	ID   int64 `path:"id"`
	Body struct {
		Method string          `json:"method" minLength:"1" maxLength:"64"`
		Params json.RawMessage `json:"params,omitempty"`
	}
}

type uiMessage struct {
	event string
	data  json.RawMessage
}

func (s *Server) uiRoutes(api huma.API) {
	huma.Register(api, huma.Operation{Metadata: needs(tier.Write), OperationID: "call-plugin-ui", Method: http.MethodPost,
		Path: "/instances/{id}/ui/call", Summary: "Call a plugin panel's backend", Tags: []string{"Plugins"},
		Description: "Server-sent events: an event per ui.event the plugin sends, then one result or error.",
		Responses: map[string]*huma.Response{"200": {Description: "A stream of events",
			Content: map[string]*huma.MediaType{"text/event-stream": {}}}}},
		func(ctx context.Context, in *uiCallIn) (*huma.StreamResponse, error) {
			inst, err := s.Registry.InstanceWith(ctx, in.ID, protocol.CapabilityUI)
			if err != nil {
				return nil, s.fail(err)
			}
			return &huma.StreamResponse{Body: func(hc huma.Context) { s.streamUICall(hc, inst, in) }}, nil
		})
}

// streamUICall runs the call in a goroutine and writes its messages here, so
// a slow browser never blocks the plugin. Leaving cancels ctx, which ends the
// goroutine and sends the plugin the cancel notification.
func (s *Server) streamUICall(hc huma.Context, inst pluginhost.Instance, in *uiCallIn) {
	ctx, cancel := context.WithCancel(hc.Context())
	defer cancel()
	messages := make(chan uiMessage, uiEventBuffer)
	token, remove := s.Runs.Add(inst.ID, func(callCtx context.Context, method string, params json.RawMessage) (any, error) {
		switch method {
		case protocol.NotifyUIEvent:
			var e protocol.UIEvent
			if err := json.Unmarshal(params, &e); err != nil {
				return nil, protocol.InvalidParams(err)
			}
			data := compact(e.Data)
			if len(data) > protocol.MaxUIEvent {
				s.Log.Warn("dropped an oversized ui event", "instance", inst.ID)
				return nil, nil
			}
			// callCtx lasts as long as the plugin's connection, and a
			// notification blocks that connection's read loop.
			select {
			case messages <- uiMessage{"event", data}:
			case <-ctx.Done():
			}
			return nil, nil
		case protocol.MethodLibraryCall:
			return librarycall.Serve(callCtx, s.Registry, s.inProcessAPI(), in.ID, params)
		}
		return nil, protocol.MethodNotFound(method)
	}, func(level, message string) { s.Log.Info("plugin ui", "instance", inst.ID, "level", level, "message", message) })
	defer remove()
	go func() {
		var res json.RawMessage
		err := s.Registry.Call(ctx, s.Plugins, inst, protocol.MethodUICall,
			protocol.UICallParams{Run: token, Method: in.Body.Method, Params: in.Body.Params}, &res)
		final := uiMessage{"result", compact(res)}
		if err != nil {
			final = uiMessage{"error", s.uiError(inst, err)}
		}
		select {
		case messages <- final:
		case <-ctx.Done():
		}
	}()
	s.writeUIStream(hc, messages)
}

// uiError is what the panel sees. Only a plugin's own message reaches it.
func (s *Server) uiError(inst pluginhost.Instance, err error) json.RawMessage {
	msg := "the plugin failed"
	var rpcErr *protocol.Error
	if errors.As(err, &rpcErr) {
		msg = rpcErr.Message
	} else if !errors.Is(err, context.Canceled) {
		s.Log.Warn("plugin ui call failed", "instance", inst.ID, "err", err)
	}
	raw, _ := json.Marshal(map[string]string{"message": msg})
	return raw
}

// compact puts JSON on one line, because a newline would end an SSE data
// field. An empty result becomes null.
func compact(raw json.RawMessage) json.RawMessage {
	var b bytes.Buffer
	if json.Compact(&b, raw) != nil {
		return json.RawMessage(`null`)
	}
	return b.Bytes()
}

// writeUIStream writes messages until the final one, the client goes away,
// or the server stops.
func (s *Server) writeUIStream(hc huma.Context, messages <-chan uiMessage) {
	hc.SetHeader("Content-Type", "text/event-stream")
	hc.SetHeader("Cache-Control", "no-cache")
	hc.SetHeader("X-Accel-Buffering", "no")
	w := hc.BodyWriter()
	flusher, _ := w.(http.Flusher)
	send := func(format string, args ...any) bool {
		if _, err := fmt.Fprintf(w, format, args...); err != nil {
			return false
		}
		if flusher != nil {
			flusher.Flush()
		}
		return true
	}
	ping := time.NewTicker(pingInterval)
	defer ping.Stop()
	for {
		select {
		case m := <-messages:
			if !send("event: %s\ndata: %s\n\n", m.event, m.data) || m.event != "event" {
				return
			}
		case <-ping.C:
			if !send(": ping\n\n") {
				return
			}
		case <-hc.Context().Done():
			return
		case <-s.Stopping:
			return
		}
	}
}
