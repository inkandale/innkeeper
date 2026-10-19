package contract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/protocol"
)

const (
	checkRecordsListName      = "records.list of each type returns valid records or a plugin error"
	checkRecordsRoundTripName = "records.get and records.put round-trip a listed record"
	checkRecordsGoneName      = "records.get and records.delete answer -32004 for a record that doesn't exist"
	checkRecordsImportName    = "records.import from each input returns valid records or a plugin error"
	contractNone              = "innkeeper-contract:none"
)

// checkRecords lists each record type. A plugin checked without its database
// may only fail, so an error other than -32601 counts as an answer, and the
// checks that need a working store run only for types that listed.
func checkRecords(ctx context.Context, p *pluginhost.Process, m *protocol.Manifest, tmp string) []Result {
	var listErrs []error
	listed := map[string][]protocol.RecordSummary{}
	for _, typ := range slices.Sorted(maps.Keys(m.Records)) {
		var res protocol.RecordsListResult
		err := call(ctx, p, protocol.MethodRecordsList, protocol.RecordsListParams{Type: typ}, &res)
		var rpcErr *protocol.Error
		if errors.As(err, &rpcErr) && rpcErr.Code != protocol.CodeMethodNotFound {
			continue
		}
		if err == nil {
			err = validSummaries(res.Records)
			listed[typ] = res.Records
		}
		if err != nil {
			listErrs = append(listErrs, fmt.Errorf("%s: %w", typ, err))
		}
	}
	results := []Result{{checkRecordsListName, errors.Join(listErrs...)}}
	if len(listErrs) > 0 || dead(results[0].Err, p) {
		return results
	}
	return append(results, runChecks(ctx, p, []check{
		{checkRecordsRoundTripName, func(ctx context.Context) error { return checkRecordsRoundTrip(ctx, p, listed) }},
		{checkRecordsGoneName, func(ctx context.Context) error { return checkRecordsGone(ctx, p, listed) }},
		{checkRecordsImportName, func(ctx context.Context) error { return checkRecordsImport(ctx, p, m, tmp) }},
	})...)
}

func validSummaries(list []protocol.RecordSummary) error {
	var errs []error
	seen := map[string]bool{}
	for _, it := range list {
		if seen[it.ID] {
			errs = append(errs, fmt.Errorf("record id %q repeats", it.ID))
		}
		seen[it.ID] = true
		errs = append(errs, it.Validate())
	}
	return errors.Join(errs...)
}

// checkRecordsRoundTrip gets the first record of each type and puts it back
// unchanged. It writes only to the test store the check was given.
func checkRecordsRoundTrip(ctx context.Context, p *pluginhost.Process, listed map[string][]protocol.RecordSummary) error {
	for typ, records := range listed {
		if len(records) == 0 {
			continue
		}
		ref := protocol.RecordRef{Type: typ, ID: records[0].ID}
		var got protocol.RecordsGetResult
		if err := call(ctx, p, protocol.MethodRecordsGet, ref, &got); err != nil {
			return fmt.Errorf("%s: get %s: %w", typ, ref.ID, err)
		}
		var obj map[string]json.RawMessage
		if json.Unmarshal(got.Record, &obj) != nil || obj == nil {
			return fmt.Errorf("%s: get %s: record is not a JSON object", typ, ref.ID)
		}
		var put protocol.RecordsPutResult
		if err := call(ctx, p, protocol.MethodRecordsPut, protocol.RecordsPutParams{Type: typ, ID: ref.ID, Record: got.Record}, &put); err != nil {
			return fmt.Errorf("%s: put %s: %w", typ, ref.ID, err)
		}
		if err := protocol.CheckRecordID(put.ID); err != nil {
			return fmt.Errorf("%s: put %s: %w", typ, ref.ID, err)
		}
	}
	return nil
}

func checkRecordsGone(ctx context.Context, p *pluginhost.Process, listed map[string][]protocol.RecordSummary) error {
	for typ := range listed {
		for _, method := range []string{protocol.MethodRecordsGet, protocol.MethodRecordsDelete} {
			err := call(ctx, p, method, protocol.RecordRef{Type: typ, ID: contractNone}, nil)
			if err := expectCode(err, protocol.CodeRemoteNotFound); err != nil {
				return fmt.Errorf("%s: %s: %w", typ, method, err)
			}
		}
	}
	return nil
}

// checkRecordsImport imports from each input a type takes. Without its
// network or a file it understands, a plugin may only fail, so an error other
// than -32601 counts as an answer.
func checkRecordsImport(ctx context.Context, p *pluginhost.Process, m *protocol.Manifest, tmp string) error {
	imports, err := recordsImports(m, tmp)
	if err != nil {
		return err
	}
	for _, params := range imports {
		var res protocol.RecordsImportResult
		err := call(ctx, p, protocol.MethodRecordsImport, params, &res)
		if answered(err) {
			continue
		}
		if err == nil {
			err = validSummaries(res.Records)
		}
		if err != nil {
			return fmt.Errorf("%s: %w", params.Type, err)
		}
	}
	return nil
}

// recordsImports are one records.import per type and input: a URL that
// can't resolve, or a small text file in tmp.
func recordsImports(m *protocol.Manifest, tmp string) ([]protocol.RecordsImportParams, error) {
	const name = "contract-check.txt"
	path := filepath.Join(tmp, name)
	if err := os.WriteFile(path, []byte("Contract check.\n"), 0o644); err != nil {
		return nil, err
	}
	var out []protocol.RecordsImportParams
	for _, typ := range slices.Sorted(maps.Keys(m.Records)) {
		for _, in := range m.Records[typ].Import {
			params := protocol.RecordsImportParams{Type: typ, URL: "https://example.invalid/contract-check"}
			if in == "file" {
				params = protocol.RecordsImportParams{Type: typ, Path: path, Name: name}
			}
			out = append(out, params)
		}
	}
	return out, nil
}

// driveRecords lists each record type and imports from each input.
func driveRecords(m *protocol.Manifest, tmp string) func(context.Context, caller) []func(context.Context) error {
	return func(_ context.Context, call caller) []func(context.Context) error {
		var out []func(context.Context) error
		for _, typ := range slices.Sorted(maps.Keys(m.Records)) {
			params := protocol.RecordsListParams{Type: typ}
			out = append(out, func(ctx context.Context) error { return call(ctx, protocol.MethodRecordsList, params, nil) })
		}
		// The records suite reports a file it couldn't write.
		imports, _ := recordsImports(m, tmp)
		for _, params := range imports {
			out = append(out, func(ctx context.Context) error { return call(ctx, protocol.MethodRecordsImport, params, nil) })
		}
		return out
	}
}
