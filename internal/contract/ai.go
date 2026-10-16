package contract

import (
	"context"
	"errors"
	"fmt"

	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/protocol"
)

const checkServicesName = "an ai plugin answers each service it offers with a reply or a plugin error"

// checkServices sends a small request to each service the plugin offers. A
// provider checked without its connection can only fail, so an error other
// than -32601 counts as an answer.
func checkServices(ctx context.Context, p *pluginhost.Process) error {
	requests := map[string]any{
		protocol.MethodAIChat:  protocol.AIChatParams{Messages: []protocol.ChatMessage{{Role: "user", Content: "Say OK."}}},
		protocol.MethodAIEmbed: protocol.AIEmbedParams{Texts: []string{"OK"}},
	}
	var errs []error
	for _, s := range p.Info.Services {
		err := call(ctx, p, s, requests[s], nil)
		var rpcErr *protocol.Error
		if err == nil || errors.As(err, &rpcErr) && rpcErr.Code != protocol.CodeMethodNotFound {
			continue
		}
		errs = append(errs, fmt.Errorf("%s: %w", s, err))
	}
	return errors.Join(errs...)
}
