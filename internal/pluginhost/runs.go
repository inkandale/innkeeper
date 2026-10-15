package pluginhost

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/davidgibbons/innkeeper/protocol"
)

// Runs sends the calls a plugin makes during a long call, such as
// import.run's notifications, to the run whose token they carry. One Runs
// serves every kind of run on a Manager.
type Runs struct {
	log  *slog.Logger
	mu   sync.Mutex
	runs map[string]protocol.Handler
}

func NewRuns(log *slog.Logger) *Runs {
	return &Runs{log: log, runs: map[string]protocol.Handler{}}
}

// Add sends calls carrying token to h until remove is called.
func (r *Runs) Add(token string, h protocol.Handler) (remove func()) {
	r.mu.Lock()
	r.runs[token] = h
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		delete(r.runs, token)
		r.mu.Unlock()
	}
}

// Handle is the core handler for a Manager whose plugins make calls for runs.
func (r *Runs) Handle(ctx context.Context, method string, params json.RawMessage) (any, error) {
	var head struct {
		Run string `json:"run"`
	}
	_ = json.Unmarshal(params, &head)
	r.mu.Lock()
	h := r.runs[head.Run]
	r.mu.Unlock()
	if h == nil {
		r.log.Warn("plugin call for a run that is not active", "method", method, "run", head.Run)
		return nil, &protocol.Error{Code: protocol.CodeInvalidParams, Message: "no active run " + head.Run}
	}
	return h(ctx, method, params)
}
