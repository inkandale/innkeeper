package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/pluginhost"
	"github.com/davidgibbons/innkeeper/internal/tier"
	"github.com/davidgibbons/innkeeper/protocol"
)

// recordCallTimeout bounds a records call; an import may fetch a page.
const recordCallTimeout = 2 * time.Minute

// recordTypeIn names an instance's record type. Huma reads params only from
// exported fields, so the inputs below repeat these rather than embed them.
type recordTypeIn struct {
	ID   int64  `path:"id"`
	Type string `path:"type" doc:"A record type the plugin's manifest declares"`
}

type recordIn struct {
	ID     int64  `path:"id"`
	Type   string `path:"type"`
	Record string `path:"record"`
}

type recordBody struct {
	Record map[string]any `json:"record" doc:"The whole record, fitting the record type's schema"`
}

type createRecordIn struct {
	ID   int64  `path:"id"`
	Type string `path:"type"`
	Body recordBody
}

type replaceRecordIn struct {
	ID     int64  `path:"id"`
	Type   string `path:"type"`
	Record string `path:"record"`
	Body   recordBody
}

type importFileIn struct {
	ID      int64  `path:"id"`
	Type    string `path:"type"`
	Name    string `query:"name" required:"true" doc:"The file's name, such as SKILL.md"`
	RawBody []byte
}

type importURLIn struct {
	ID   int64  `path:"id"`
	Type string `path:"type"`
	Body struct {
		URL string `json:"url" pattern:"^https?://" doc:"An http or https URL"`
	}
}

type recordSummariesOut struct{ Body []protocol.RecordSummary }

type recordOut struct {
	Body struct {
		ID     string          `json:"id"`
		Record json.RawMessage `json:"record"`
	}
}

type recordIDOut struct {
	Body protocol.RecordsPutResult
}

func (s *Server) recordRoutes(api huma.API) {
	tags := []string{"Plugin records"}
	base := "/instances/{id}/records/{type}"
	huma.Register(api, huma.Operation{OperationID: "list-records", Method: http.MethodGet, Path: base,
		Summary: "List a plugin instance's records of one type", Tags: tags},
		func(ctx context.Context, in *recordTypeIn) (*recordSummariesOut, error) {
			var res protocol.RecordsListResult
			if err := s.callRecords(ctx, in, protocol.MethodRecordsList, protocol.RecordsListParams{Type: in.Type}, &res); err != nil {
				return nil, s.fail(err)
			}
			return &recordSummariesOut{Body: s.validSummaries(in.ID, res.Records)}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "get-record", Method: http.MethodGet, Path: base + "/{record}",
		Summary: "Get one of a plugin instance's records", Tags: tags},
		func(ctx context.Context, in *recordIn) (*recordOut, error) {
			if err := checkRecordPath(in.Record); err != nil {
				return nil, err
			}
			var res protocol.RecordsGetResult
			if err := s.callRecords(ctx, &recordTypeIn{in.ID, in.Type}, protocol.MethodRecordsGet, protocol.RecordRef{Type: in.Type, ID: in.Record}, &res); err != nil {
				return nil, s.fail(err)
			}
			out := &recordOut{}
			out.Body.ID, out.Body.Record = in.Record, res.Record
			return out, nil
		})

	huma.Register(api, huma.Operation{Metadata: needs(tier.Write), OperationID: "create-record", Method: http.MethodPost, Path: base,
		Summary: "Add a record to a plugin instance", Tags: tags, DefaultStatus: http.StatusCreated,
		Description: "A 400 carries the plugin's reason for refusing the record."},
		func(ctx context.Context, in *createRecordIn) (*recordIDOut, error) {
			return s.putRecord(ctx, &recordTypeIn{in.ID, in.Type}, "", in.Body.Record)
		})

	// Destructive, like the instance's settings: the old record is gone.
	huma.Register(api, huma.Operation{Metadata: needs(tier.Destructive), OperationID: "replace-record", Method: http.MethodPut,
		Path: base + "/{record}", Summary: "Replace one of a plugin instance's records", Tags: tags,
		Description: "The reply's id may differ from the path's, such as for a renamed skill. A 400 carries the plugin's reason for refusing the record."},
		func(ctx context.Context, in *replaceRecordIn) (*recordIDOut, error) {
			if err := checkRecordPath(in.Record); err != nil {
				return nil, err
			}
			return s.putRecord(ctx, &recordTypeIn{in.ID, in.Type}, in.Record, in.Body.Record)
		})

	huma.Register(api, huma.Operation{Metadata: needs(tier.Destructive), OperationID: "delete-record", Method: http.MethodDelete,
		Path: base + "/{record}", Summary: "Delete one of a plugin instance's records", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *recordIn) (*struct{}, error) {
			if err := checkRecordPath(in.Record); err != nil {
				return nil, err
			}
			return nil, s.fail(s.callRecords(ctx, &recordTypeIn{in.ID, in.Type}, protocol.MethodRecordsDelete, protocol.RecordRef{Type: in.Type, ID: in.Record}, nil))
		})

	// Destructive: an import may replace records with the same IDs.
	huma.Register(api, huma.Operation{Metadata: needs(tier.Destructive), OperationID: "import-record-file", Method: http.MethodPost,
		Path: base + "/import", Summary: "Import records from a file", Tags: tags,
		Description:  "Send the file as application/octet-stream. Returns what was imported. A 400 carries the plugin's reason for refusing the file.",
		MaxBodyBytes: library.MaxBlobBytes, BodyReadTimeout: 2 * time.Minute},
		func(ctx context.Context, in *importFileIn) (*recordSummariesOut, error) {
			if strings.ContainsAny(in.Name, "/\\\x00") {
				return nil, huma.Error400BadRequest("a file name can't hold /, \\, or NUL")
			}
			return s.importRecords(ctx, &recordTypeIn{in.ID, in.Type}, "file", protocol.RecordsImportParams{Type: in.Type, Name: in.Name}, in.RawBody)
		})

	huma.Register(api, huma.Operation{Metadata: needs(tier.Destructive), OperationID: "import-record-url", Method: http.MethodPost,
		Path: base + "/import-url", Summary: "Import records from a URL", Tags: tags,
		Description: "Returns what was imported. A 400 carries the plugin's reason for refusing the URL."},
		func(ctx context.Context, in *importURLIn) (*recordSummariesOut, error) {
			return s.importRecords(ctx, &recordTypeIn{in.ID, in.Type}, "url", protocol.RecordsImportParams{Type: in.Type, URL: in.Body.URL}, nil)
		})
}

// importRecords sends records.import, with file written to blob_tmp as params'
// path when there is one. input is what the record type must import from.
func (s *Server) importRecords(ctx context.Context, in *recordTypeIn, input string, params protocol.RecordsImportParams, file []byte) (*recordSummariesOut, error) {
	inst, err := s.recordsInstance(ctx, in)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(inst.Manifest.Records[in.Type].Import, input) {
		return nil, huma.Error400BadRequest(fmt.Sprintf("record type %s doesn't import from a %s", in.Type, input))
	}
	if input == "file" {
		if params.Path, err = writeTmp(inst.Params.BlobTmp, file); err != nil {
			return nil, err
		}
		defer os.Remove(params.Path)
	}
	var res protocol.RecordsImportResult
	if err := s.callRecordsOn(ctx, inst, protocol.MethodRecordsImport, params, &res); err != nil {
		return nil, s.recordsFail(err)
	}
	return &recordSummariesOut{Body: s.validSummaries(in.ID, res.Records)}, nil
}

// checkRecordPath refuses a record ID from the URL that a plugin could
// mistake for a path, such as an encoded ../secret.
func checkRecordPath(id string) error {
	if err := protocol.CheckRecordID(id); err != nil {
		return huma.Error400BadRequest(err.Error())
	}
	return nil
}

// recordsInstance is the instance in, if its plugin keeps in.Type.
func (s *Server) recordsInstance(ctx context.Context, in *recordTypeIn) (pluginhost.Instance, error) {
	inst, err := s.usable(ctx, in.ID, "records")
	if err != nil {
		return inst, err
	}
	if _, ok := inst.Manifest.Records[in.Type]; !ok {
		return inst, huma.Error404NotFound(fmt.Sprintf("plugin %s has no record type %q", inst.Manifest.Name, in.Type))
	}
	return inst, nil
}

// callRecords sends method to the instance in names, after checking its type.
func (s *Server) callRecords(ctx context.Context, in *recordTypeIn, method string, params, result any) error {
	inst, err := s.recordsInstance(ctx, in)
	if err != nil {
		return err
	}
	return s.callRecordsOn(ctx, inst, method, params, result)
}

func (s *Server) callRecordsOn(ctx context.Context, inst pluginhost.Instance, method string, params, result any) error {
	callCtx, cancel := context.WithTimeout(ctx, recordCallTimeout)
	defer cancel()
	return s.Registry.Call(callCtx, s.Plugins, inst, method, params, result)
}

func (s *Server) putRecord(ctx context.Context, in *recordTypeIn, id string, record map[string]any) (*recordIDOut, error) {
	raw, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	out := &recordIDOut{}
	err = s.callRecords(ctx, in, protocol.MethodRecordsPut, protocol.RecordsPutParams{Type: in.Type, ID: id, Record: raw}, &out.Body)
	if err == nil {
		err = protocol.CheckRecordID(out.Body.ID)
	}
	return out, s.recordsFail(err)
}

// recordsFail is fail, except that the owner sees why the plugin refused a
// record or an import: the owner sent it, and records.put and records.import
// need write or above.
func (s *Server) recordsFail(err error) error {
	var rpcErr *protocol.Error
	if errors.As(err, &rpcErr) && rpcErr.Code == protocol.CodeInvalidParams {
		return huma.Error400BadRequest(rpcErr.Message)
	}
	return s.fail(err)
}

// validSummaries drops and logs the summaries the UI couldn't show.
func (s *Server) validSummaries(instance int64, list []protocol.RecordSummary) []protocol.RecordSummary {
	out := []protocol.RecordSummary{}
	for _, it := range list {
		if err := it.Validate(); err != nil {
			s.Log.Warn("skip invalid record", "instance", instance, "err", err)
			continue
		}
		out = append(out, it)
	}
	return out
}

// writeTmp writes an upload to a new file directly inside blob_tmp.
func writeTmp(dir string, body []byte) (string, error) {
	f, err := os.CreateTemp(dir, "upload-*")
	if err != nil {
		return "", err
	}
	_, err = f.Write(body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}
