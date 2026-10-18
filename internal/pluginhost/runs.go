package pluginhost

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/inkandale/innkeeper/protocol"
)

// Runs sends the calls a plugin makes during a long call, such as
// import.run's notifications, to the run whose token they carry. One Runs
// serves every kind of run on a Manager.
type Runs struct {
	log  *slog.Logger
	mu   sync.Mutex
	runs map[string]run
}

type run struct {
	instance string
	handler  protocol.Handler
	log      func(level, message string)
}

func NewRuns(log *slog.Logger) *Runs {
	return &Runs{log: log, runs: map[string]run{}}
}

// Add sends calls that carry the returned token, from instance's plugin
// process, to h until remove is called, and log notices to log. Every plugin
// on a Manager shares one core handler, so the token is random and only
// instance may use it.
func (r *Runs) Add(instance string, h protocol.Handler, log func(level, message string)) (token string, remove func()) {
	token = rand.Text()
	r.mu.Lock()
	r.runs[token] = run{instance, h, log}
	r.mu.Unlock()
	return token, func() {
		r.mu.Lock()
		delete(r.runs, token)
		r.mu.Unlock()
	}
}

// LogFor is where the run with token sends its log notices, so a call made
// on the run's behalf can log to it too. An unknown token's lines are dropped.
func (r *Runs) LogFor(token string) func(level, message string) {
	r.mu.Lock()
	rn, ok := r.runs[token]
	r.mu.Unlock()
	if !ok {
		return func(string, string) {}
	}
	return rn.log
}

// Handle is the core handler for a Manager whose plugins make calls for runs.
func (r *Runs) Handle(ctx context.Context, method string, params json.RawMessage) (any, error) {
	var head struct {
		Run string `json:"run"`
	}
	_ = json.Unmarshal(params, &head)
	r.mu.Lock()
	rn, ok := r.runs[head.Run]
	r.mu.Unlock()
	if !ok || rn.instance != CallerInstance(ctx) {
		r.log.Warn("plugin call for a run that is not active", "method", method, "instance", CallerInstance(ctx))
		return nil, &protocol.Error{Code: protocol.CodeInvalidParams, Message: "no active run with this token"}
	}
	if method == protocol.NotifyLog {
		var l protocol.Log
		if err := json.Unmarshal(params, &l); err != nil {
			return nil, &protocol.Error{Code: protocol.CodeInvalidParams, Message: err.Error()}
		}
		rn.log(l.Level, l.Message)
		return nil, nil
	}
	return rn.handler(ctx, method, params)
}

type instanceKey struct{}

// fromInstance marks the context of calls from instance's plugin process.
func fromInstance(ctx context.Context, instance string) context.Context {
	return context.WithValue(ctx, instanceKey{}, instance)
}

// CallerInstance is the instance whose plugin made the call, or "".
func CallerInstance(ctx context.Context) string {
	id, _ := ctx.Value(instanceKey{}).(string)
	return id
}

type startingKey struct{}

// CallerStarting reports whether the call came from a plugin still being
// started, while its Manager is locked. A handler that uses the Manager
// must refuse such a call, or it waits for the start to time out.
func CallerStarting(ctx context.Context) bool {
	starting, _ := ctx.Value(startingKey{}).(bool)
	return starting
}
