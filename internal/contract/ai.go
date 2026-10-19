package contract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/protocol"
)

const checkServicesName = "an ai plugin answers each service it offers with a valid reply or a plugin error"

// checkServices sends each service the plugin offers its sample request. A
// provider checked without its connection can only fail, so an error other
// than -32601 counts as an answer.
func checkServices(ctx context.Context, p *pluginhost.Process) error {
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
	return errors.Join(errs...)
}
