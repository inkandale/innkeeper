package contract

import (
	"context"
	"errors"

	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/protocol"
)

const checkUIName = "ui.call of an unknown method returns -32601"

func checkUI(ctx context.Context, p *pluginhost.Process) error {
	err := call(ctx, p, protocol.MethodUICall, protocol.UICallParams{Run: "contract", Method: "innkeeper-contract-none"}, nil)
	var rpcErr *protocol.Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != protocol.CodeMethodNotFound {
		return errors.New("an unknown method must return -32601")
	}
	return nil
}
