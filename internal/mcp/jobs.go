package mcp

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/inkandale/innkeeper/internal/tier"
)

// JobRoute is an API route that queues a job: a POST whose path takes no
// parameters and that answers 202 with the job.
type JobRoute struct {
	// Name is the route's operation ID, such as start-import.
	Name string
	Path string
	// Description says what the job does and lists the body's fields.
	Description string
	Tier        tier.Tier
}

type startJobIn struct {
	Operation string         `json:"operation" jsonschema:"The job route's operation, from this tool's description"`
	Body      map[string]any `json:"body,omitempty" jsonschema:"The route's JSON body, with the fields its description lists"`
}

// startJob is the start_job tool for callers with tier t. It offers the job
// routes t allows except destructive ones, so its annotations stay honest;
// those keep their own tools, such as push.
func startJob(routes []JobRoute, t tier.Tier) (tool, bool) {
	var offered []JobRoute
	need := tier.Read
	for _, r := range routes {
		if r.Tier == tier.Destructive || !t.Allows(r.Tier) {
			continue
		}
		offered = append(offered, r)
		need = max(need, r.Tier)
	}
	if len(offered) == 0 {
		return tool{}, false
	}
	var desc strings.Builder
	desc.WriteString("Start a job: an import, a sync, an evaluation, or any other route below. Returns the job; get_job tracks it and cancel_job stops it. Operations:")
	names := make([]any, len(offered))
	for i, r := range offered {
		names[i] = r.Name
		fmt.Fprintf(&desc, "\n- %s: %s", r.Name, r.Description)
	}
	tl := def("start_job", desc.String(), need, func(ctx context.Context, c caller, in startJobIn) (*sdk.CallToolResult, error) {
		i := slices.IndexFunc(offered, func(r JobRoute) bool { return r.Name == in.Operation })
		if i < 0 {
			return nil, fmt.Errorf("no job operation %q", in.Operation)
		}
		// A nil map in body would send null, not no body.
		if in.Body == nil {
			return c.call(ctx, "POST", offered[i].Path, nil)
		}
		return c.call(ctx, "POST", offered[i].Path, in.Body)
	})
	tl.meta.InputSchema.(*jsonschema.Schema).Properties["operation"].Enum = names
	return tl, true
}
