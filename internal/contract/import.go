package contract

import (
	"context"
	"encoding/json"

	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/protocol"
)

const checkImportName = "import.run with empty params returns a reply or a plugin error"

// importRunParams starts a first run, as the core does, with no params.
var importRunParams = protocol.ImportRunParams{Run: "contract-check", Params: json.RawMessage(`{}`)}

// checkImport starts an import with empty params. Most importers need a file
// or URL, so an error other than -32601 counts as an answer.
func checkImport(ctx context.Context, p *pluginhost.Process) error {
	err := call(ctx, p, protocol.MethodImportRun, importRunParams, nil)
	if answered(err) {
		return nil
	}
	return err
}

// driveImport starts an import with empty params.
func driveImport(_ context.Context, call caller) []func(context.Context) error {
	return []func(context.Context) error{func(ctx context.Context) error {
		return call(ctx, protocol.MethodImportRun, importRunParams, nil)
	}}
}
