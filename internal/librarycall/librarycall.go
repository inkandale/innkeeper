// Package librarycall answers a plugin's library.call for any kind of run.
package librarycall

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"

	"github.com/inkandale/innkeeper/internal/mcp"
	"github.com/inkandale/innkeeper/internal/plugins"
	"github.com/inkandale/innkeeper/internal/tier"
	"github.com/inkandale/innkeeper/protocol"
)

// tools read the library, not apps or settings.
var tools = []string{"search_cards", "find_similar_cards", "find_related_cards", "get_card", "get_lorebook", "get_history"}

// Serve runs one library tool for instance, if the owner granted it
// library.read. api serves the tools in process; see mcp.Call.
func Serve(ctx context.Context, reg *plugins.Registry, api http.Handler, instance int64, params json.RawMessage) (any, error) {
	var c protocol.LibraryCallParams
	if err := json.Unmarshal(params, &c); err != nil {
		return nil, protocol.InvalidParams(err)
	}
	if !slices.Contains(tools, c.Name) {
		return nil, forbidden(fmt.Sprintf("library.call runs only %v, not %q", tools, c.Name))
	}
	granted, err := reg.HasGrant(ctx, instance, protocol.PermissionLibraryRead)
	if err != nil {
		return nil, err
	}
	if !granted {
		return nil, forbidden("Library access is off for this instance; the owner can turn it on in its settings")
	}
	return mcp.Call(ctx, api, tier.Read, c.Name, c.Arguments)
}

func forbidden(msg string) *protocol.Error {
	return &protocol.Error{Code: protocol.CodeForbidden, Message: msg}
}
