package contract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/protocol"
)

const checkServicesName = "a plugin answers each service it offers with a valid reply or a plugin error"

// checkServices sends each service the plugin offers its sample request. A
// provider checked without its connection can only fail, so an error other
// than -32601 counts as an answer. It adds no result when the instance
// reports no services.
func checkServices(ctx context.Context, p *pluginhost.Process) []Result {
	if len(p.Info.Services) == 0 {
		return nil
	}
	var errs []error
	for _, s := range p.Info.Services {
		sample := protocol.ServiceSamples[s]
		var reply json.RawMessage
		err := call(ctx, p, s, sample.Params, &reply)
		switch {
		case answered(err):
		case err != nil:
			errs = append(errs, fmt.Errorf("%s: %w", s, err))
		default:
			if err := sample.Check(reply); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", s, err))
			}
		}
	}
	return []Result{{checkServicesName, errors.Join(errs...)}}
}
