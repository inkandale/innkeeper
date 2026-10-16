package contract

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/davidgibbons/innkeeper/internal/pluginhost"
	"github.com/davidgibbons/innkeeper/internal/plugins"
	"github.com/davidgibbons/innkeeper/internal/services"
	"github.com/davidgibbons/innkeeper/protocol"
)

// Provider is the folder of the plugin that answers the services a checked
// plugin uses. A plugin with uses fails the check without one.
var Provider string

// ProviderConfig is the provider's instance config as JSON. The check
// replaces ${INNKEEPER_FAKE_AI_URL} in it with its model server's URL.
var ProviderConfig string

const fakeAIURL = "${INNKEEPER_FAKE_AI_URL}"

const (
	checkProviderName = "a provider plugin starts behind the service broker"
	checkReplyName    = "a service call reaches the provider and its reply comes back"
	checkDeltasName   = "ai.delta from the provider streams through to the consumer"
	checkToolsName    = "a tool call round-trips through the provider"
	checkCancelName   = "cancelling the consumer's call cancels the provider's request"
)

const consumerID, providerID = 1, 2

// checkUses runs the plugin and the Provider under the real service broker,
// drives the plugin's actions and enrich.run, and checks what reached the
// model server and what came back.
func checkUses(ctx context.Context, m *protocol.Manifest, params protocol.InitializeParams, log *slog.Logger) []Result {
	model := newModelServer()
	defer model.Close()
	h, err := startHarness(ctx, m, params, model.URL+"/v1", log)
	results := []Result{{checkProviderName, err}}
	if err != nil {
		return results
	}
	defer h.mgr.Close()

	var first func(context.Context) error
	for _, drive := range h.drivers(ctx) {
		before := model.requestCount()
		// Only what reached the model server matters, not the consumer's own reply.
		_ = drive(ctx)
		if first == nil && model.requestCount() > before {
			first = drive
		}
		if h.calls.err() == nil && h.deltas.Load() >= 2 && model.toolRoundTrip() == nil {
			break
		}
	}

	replyErr := h.calls.err()
	if replyErr == nil && model.requestCount() == 0 {
		replyErr = errors.New("the provider answered without a request to the model server")
	}
	var deltaErr error
	if n := h.deltas.Load(); n < 2 {
		deltaErr = fmt.Errorf("%d ai.delta reached the consumer; the model streamed 2", n)
	}
	cancelErr := errors.New("no consumer call reached the model server")
	if first != nil {
		cancelErr = checkCancel(ctx, model, first)
	}
	return append(results,
		Result{checkReplyName, replyErr},
		Result{checkDeltasName, deltaErr},
		Result{checkToolsName, model.toolRoundTrip()},
		Result{checkCancelName, cancelErr})
}

// checkCancel holds the model's next reply open, cancels the consumer's call
// once the request arrives, and wants the provider to drop the request.
func checkCancel(ctx context.Context, model *modelServer, drive func(context.Context) error) error {
	model.holdNext()
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = drive(ctx)
	}()
	defer func() {
		cancel()
		<-done
	}()
	select {
	case <-model.holding:
	case <-time.After(callTimeout):
		return fmt.Errorf("the call didn't reach the model server within %s", callTimeout)
	}
	cancel()
	select {
	case <-model.ended:
		return nil
	case <-time.After(callTimeout):
		return fmt.Errorf("the model request was still open %s after the consumer's call was cancelled", callTimeout)
	}
}

// harness runs a consumer and its provider under one Manager whose core is
// the service broker.
type harness struct {
	mgr      *pluginhost.Manager
	consumer pluginhost.Instance
	calls    serviceLog
	deltas   atomic.Int64
}

func startHarness(ctx context.Context, m *protocol.Manifest, params protocol.InitializeParams, modelURL string, log *slog.Logger) (*harness, error) {
	if Provider == "" {
		return nil, fmt.Errorf("plugin %s uses %s; set INNKEEPER_PROVIDER_PLUGIN to the folder of a plugin that offers them",
			m.Name, strings.Join(m.Uses, ", "))
	}
	pm, err := protocol.LoadManifest(Provider)
	if err != nil {
		return nil, fmt.Errorf("provider: %w", err)
	}
	var providerConfig map[string]any
	raw := strings.ReplaceAll(cmp.Or(ProviderConfig, "{}"), fakeAIURL, modelURL)
	if err := json.Unmarshal([]byte(raw), &providerConfig); err != nil {
		return nil, fmt.Errorf("INNKEEPER_PROVIDER_CONFIG: %w", err)
	}
	providerParams := params
	providerParams.Config, providerParams.Secrets = providerConfig, checkSecrets(pm)
	consumerParams := params
	consumerParams.Config = withProvider(m, params.Config)

	h := &harness{consumer: pluginhost.Instance{ID: fmt.Sprint(consumerID), Version: "1", Manifest: m, Params: consumerParams}}
	reg := instances{
		consumerID: h.consumer,
		providerID: {ID: fmt.Sprint(providerID), Version: "1", Manifest: pm, Params: providerParams},
	}
	runs := pluginhost.NewRuns(log)
	broker := &services.Broker{Registry: reg, Runs: runs}
	core := broker.Handle(func(ctx context.Context, method string, raw json.RawMessage) (any, error) {
		if method != protocol.NotifyAIDelta {
			return noCore(ctx, method, raw)
		}
		res, err := runs.Handle(ctx, method, raw)
		if err == nil {
			h.deltas.Add(1)
		}
		return res, err
	})
	h.mgr = pluginhost.NewManager(h.calls.record(core), time.Minute, log)
	broker.Plugins = h.mgr
	if err := h.mgr.Start(ctx, reg[providerID]); err != nil {
		h.mgr.Close()
		return nil, fmt.Errorf("provider: %w", err)
	}
	return h, nil
}

// withProvider is config with every instance field that offers a method in
// m's uses set to the provider.
func withProvider(m *protocol.Manifest, config map[string]any) map[string]any {
	out := maps.Clone(config)
	if out == nil {
		out = map[string]any{}
	}
	props, _ := m.Config["properties"].(map[string]any)
	for key, v := range props {
		prop, _ := v.(map[string]any)
		offers, _ := prop["x-offers"].(string)
		if prop["format"] == "instance" && slices.Contains(m.Uses, offers) {
			out[key] = providerID
		}
	}
	return out
}

// drivers are the consumer calls that may use a service: each action on
// the sample entity, then enrich.run on the sample card.
func (h *harness) drivers(ctx context.Context) []func(context.Context) error {
	var out []func(context.Context) error
	if h.consumer.Manifest.Has("action") {
		var list protocol.ActionListResult
		if err := h.call(ctx, protocol.MethodActionList, nil, &list); err == nil {
			for _, a := range list.Actions {
				params := actionRunParams(a)
				out = append(out, func(ctx context.Context) error {
					return h.call(ctx, protocol.MethodActionRun, params, nil)
				})
			}
		}
	}
	if h.consumer.Manifest.Has("enrich") {
		out = append(out, func(ctx context.Context) error {
			return h.call(ctx, protocol.MethodEnrichRun, enrichRunParams(), nil)
		})
	}
	return out
}

func (h *harness) call(ctx context.Context, method string, params, result any) error {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	return h.mgr.Call(ctx, h.consumer, method, params, result)
}

// serviceLog records how the broker answered the consumer's service calls.
type serviceLog struct {
	mu      sync.Mutex
	replied bool
	last    error
}

func (l *serviceLog) record(next protocol.Handler) protocol.Handler {
	return func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		res, err := next(ctx, method, params)
		if _, ok := protocol.Services[method]; ok {
			l.mu.Lock()
			if err == nil {
				l.replied = true
			} else {
				l.last = err
			}
			l.mu.Unlock()
		}
		return res, err
	}
}

// err is nil once a service call got a reply.
func (l *serviceLog) err() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	switch {
	case l.replied:
		return nil
	case l.last != nil:
		return fmt.Errorf("the broker answered the consumer with an error: %w", l.last)
	}
	return errors.New("the consumer made no service call from its actions or enrich.run")
}

// instances is the broker's registry for a check: the consumer and its
// provider.
type instances map[int64]pluginhost.Instance

func (r instances) Instance(_ context.Context, id int64) (pluginhost.Instance, error) {
	inst, ok := r[id]
	if !ok {
		return pluginhost.Instance{}, plugins.ErrNotFound
	}
	return inst, nil
}

func (r instances) InstanceWith(ctx context.Context, id int64, capability string) (pluginhost.Instance, error) {
	inst, err := r.Instance(ctx, id)
	if err == nil && !inst.Manifest.Has(capability) {
		err = fmt.Errorf("%w: plugin %s can't %s", plugins.ErrIncapable, inst.Manifest.Name, capability)
	}
	return inst, err
}

func (r instances) Services(ctx context.Context, mgr *pluginhost.Manager, inst pluginhost.Instance) ([]string, error) {
	info, err := mgr.Info(ctx, inst)
	return info.Services, err
}

func (r instances) Call(ctx context.Context, mgr *pluginhost.Manager, inst pluginhost.Instance, method string, params, result any) error {
	return mgr.Call(ctx, inst, method, params, result)
}
