// Package services routes the service calls one plugin instance makes to
// another, such as ai.chat. It checks that the caller declared the method
// and the provider offers it, forwards the call, and relays its stream.
package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/davidgibbons/innkeeper/internal/pluginhost"
	"github.com/davidgibbons/innkeeper/internal/plugins"
	"github.com/davidgibbons/innkeeper/protocol"
)

// Broker answers service calls for the plugins on one Manager.
type Broker struct {
	Registry *plugins.Registry
	// Plugins runs the providers; its core handler must be Handle's.
	Plugins *pluginhost.Manager
	// Runs carries the providers' notifications back to the call.
	Runs *pluginhost.Runs
}

// Handle answers service calls and sends every other call to next.
func (b *Broker) Handle(next protocol.Handler) protocol.Handler {
	return func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		if _, ok := protocol.Services[method]; !ok {
			return next(ctx, method, params)
		}
		return b.call(ctx, method, params)
	}
}

func (b *Broker) call(ctx context.Context, method string, params json.RawMessage) (any, error) {
	var head protocol.ServiceCall
	var fwd map[string]json.RawMessage
	if err := json.Unmarshal(params, &head); err != nil || json.Unmarshal(params, &fwd) != nil {
		return nil, protocol.NewError(protocol.CodeInvalidParams, "a service call's params must be an object", false)
	}
	if pluginhost.CallerStarting(ctx) {
		return nil, forbidden("call after initialize")
	}
	callerID, err := strconv.ParseInt(pluginhost.CallerInstance(ctx), 10, 64)
	if err != nil {
		return nil, forbidden("only a plugin instance can call a service")
	}
	caller, err := b.Registry.Instance(ctx, callerID)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(caller.Manifest.Uses, method) {
		return nil, forbidden(fmt.Sprintf("plugin %s doesn't declare %s in uses", caller.Manifest.Name, method))
	}
	provider, err := b.Registry.InstanceWith(ctx, head.Instance, protocol.Services[method])
	if errors.Is(err, plugins.ErrIncapable) || errors.Is(err, plugins.ErrNotFound) ||
		errors.Is(err, plugins.ErrBroken) || errors.Is(err, plugins.ErrUnknownPlugin) {
		return nil, forbidden(fmt.Sprintf("instance %d doesn't offer %s", head.Instance, method))
	}
	if err != nil {
		return nil, err
	}
	offered, err := b.Registry.Services(ctx, b.Plugins, provider)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(offered, method) {
		return nil, forbidden(fmt.Sprintf("instance %d doesn't offer %s", head.Instance, method))
	}
	delete(fwd, "instance")
	if head.Run != "" {
		token, remove := b.Runs.Add(provider.ID, relay(protocol.Peer(ctx), head.Run))
		defer remove()
		fwd["run"], _ = json.Marshal(token)
	}
	var res json.RawMessage
	if err := b.Registry.Call(ctx, b.Plugins, provider, method, fwd, &res); err != nil {
		return nil, err
	}
	return res, nil
}

// relay sends the provider's ai.delta notifications to the caller under the
// caller's own token.
func relay(caller *protocol.Conn, run string) protocol.Handler {
	return func(_ context.Context, method string, params json.RawMessage) (any, error) {
		if method != protocol.NotifyAIDelta {
			return nil, protocol.MethodNotFound(method)
		}
		var d protocol.AIDelta
		if err := json.Unmarshal(params, &d); err != nil {
			return nil, protocol.NewError(protocol.CodeInvalidParams, err.Error(), false)
		}
		d.Run = run
		return nil, caller.Notify(method, d)
	}
}

func forbidden(msg string) *protocol.Error {
	return &protocol.Error{Code: protocol.CodeForbidden, Message: msg}
}
