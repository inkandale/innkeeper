package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/protocol"
)

// progressEvery is how often a run's progress is saved, which is how often
// the job's event stream looks.
const progressEvery = time.Second

// JobRunner is what a job handler needs to make a plugin's long call, such
// as import.run, and to answer the calls the plugin makes during it.
type JobRunner struct {
	Registry *Registry
	// Plugins runs the plugins. Its core handler must be Runs.Handle.
	Plugins *pluginhost.Manager
	Runs    *pluginhost.Runs
}

// PluginRun is a job's long plugin call, from loading its instance until
// Close.
type PluginRun struct {
	Instance pluginhost.Instance
	runs     *pluginhost.Runs
	job      *jobs.Run
	remove   func()
	saver    progressSaver
}

// OpenRun loads job r's instance, which must declare capability, and
// decodes r's payload into payload.
func (jr JobRunner) OpenRun(ctx context.Context, r *jobs.Run, capability string, payload any) (*PluginRun, error) {
	if r.InstanceID == nil {
		return nil, fmt.Errorf("%s job has no plugin instance", r.Kind)
	}
	inst, err := jr.Registry.InstanceWith(ctx, *r.InstanceID, capability)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(r.Payload, payload); err != nil {
		return nil, fmt.Errorf("%s payload: %w", r.Kind, err)
	}
	pr := &PluginRun{Instance: inst, runs: jr.Runs, job: r}
	// A failed save is left to the lease renewal, which saves the progress again or stops the run.
	pr.saver = progressSaver{every: progressEvery, save: func() { _ = r.SaveProgress(ctx) }}
	return pr, nil
}

// Serve sends the plugin's progress notifications that carry the returned
// token to onProgress, and its other calls to serve, which may be nil,
// until Close. The call's params carry the token.
func (pr *PluginRun) Serve(onProgress func(protocol.Progress), serve protocol.Handler) (token string) {
	token, pr.remove = pr.runs.Add(pr.Instance.ID, func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		switch {
		case method == protocol.NotifyProgress:
			var p protocol.Progress
			if json.Unmarshal(params, &p) == nil {
				onProgress(p)
			}
			return nil, nil
		case serve != nil:
			return serve(ctx, method, params)
		}
		return nil, protocol.MethodNotFound(method)
	}, pr.job.Log)
	return token
}

// Publish sets the job's progress to p and saves it soon after, at most
// once per progressEvery, away from the plugin's read loop.
func (pr *PluginRun) Publish(p any) {
	raw, _ := json.Marshal(p)
	pr.job.SetProgress(raw)
	pr.saver.poke()
}

// Close stops sending the plugin's calls to the run.
func (pr *PluginRun) Close() {
	if pr.remove != nil {
		pr.remove()
	}
}

// progressSaver saves a run's progress soon after it changes, at most once per
// every.
type progressSaver struct {
	every time.Duration
	save  func()

	mu      sync.Mutex
	last    time.Time
	pending bool
}

// poke asks for a save: now, or once every has passed since the last one.
func (p *progressSaver) poke() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pending {
		return
	}
	p.pending = true
	time.AfterFunc(max(p.every-time.Since(p.last), 0), func() {
		p.mu.Lock()
		p.pending, p.last = false, time.Now()
		p.mu.Unlock()
		p.save()
	})
}
