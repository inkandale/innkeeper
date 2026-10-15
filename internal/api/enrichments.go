package api

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"

	"github.com/danielgtaylor/huma/v2"

	"github.com/davidgibbons/innkeeper/internal/enrich"
	"github.com/davidgibbons/innkeeper/internal/jobs"
	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/plugins"
	"github.com/davidgibbons/innkeeper/internal/tier"
	"github.com/davidgibbons/innkeeper/protocol"
)

const scoreInstanceSetting = "score_instance"

// scoreInstance is the enrich instance whose values the grid, filters, and
// sort use: the score_instance setting while it names an enrich instance,
// else the only enrich instance, else 0.
func (s *Server) scoreInstance(ctx context.Context) (int64, error) {
	v, err := s.Registry.Setting(ctx, scoreInstanceSetting)
	if err != nil {
		return 0, err
	}
	insts, err := s.Registry.InstancesWith(ctx, "enrich")
	if err != nil {
		return 0, err
	}
	if id, err := strconv.ParseInt(v, 10, 64); err == nil &&
		slices.ContainsFunc(insts, func(i plugins.InstanceInfo) bool { return i.ID == id }) {
		return id, nil
	}
	if len(insts) == 1 {
		return insts[0].ID, nil
	}
	return 0, nil
}

// withScores sets q's score instance.
func (s *Server) withScores(ctx context.Context, q *library.Query) error {
	id, err := s.scoreInstance(ctx)
	q.ScoreInstance = id
	return err
}

// searchIDs is library.SearchIDs with the score instance, for a bulk
// selection.
func (s *Server) searchIDs(ctx context.Context, k *library.Kind, q library.Query) ([]int64, error) {
	if err := s.withScores(ctx, &q); err != nil {
		return nil, err
	}
	return s.Lib.SearchIDs(ctx, k, q)
}

func toEnrichmentOut(e protocol.Enrichment) enrichmentOut {
	return enrichmentOut{Type: e.Type, Label: e.Label, Range: e.Range, Grid: e.Grid}
}

// enrichmentValue is a stored value with its key's declaration.
type enrichmentValue struct {
	library.Enrichment
	enrichmentOut
	InstanceName string `json:"instance_name,omitempty" doc:"Empty when the instance has been deleted."`
}

type cardEnrichmentsIn struct {
	ID      int64 `path:"id"`
	History bool  `query:"history" doc:"Also return older values, each after the one that replaced it."`
}

type cardEnrichmentsOut struct{ Body []enrichmentValue }

type enrichmentKey struct {
	Key string `json:"key"`
	enrichmentOut
}

type enrichmentKeysOut struct {
	Body struct {
		InstanceID *int64          `json:"instance_id" doc:"The score instance, or null when there is none."`
		Keys       []enrichmentKey `json:"keys"`
	}
}

type startEnrichIn struct {
	Body struct {
		InstanceID int64          `json:"instance_id" doc:"An enrich instance"`
		CardIDs    []int64        `json:"card_ids,omitempty" maxItems:"10000" doc:"The cards to enrich. Send this or query."`
		Query      *library.Query `json:"query,omitempty" doc:"Enriches every card the query matches, as POST /bulk selects them. Send this or card_ids."`
	}
}

func (s *Server) enrichmentRoutes(api huma.API) {
	tags := []string{"Enrichments"}
	huma.Register(api, huma.Operation{Metadata: needs(tier.Write), OperationID: "start-enrich-run", Method: http.MethodPost,
		Path: "/enrichments/runs", Summary: "Compute enrichments for cards", Tags: tags, DefaultStatus: http.StatusAccepted,
		Description: "Queues an enrich job that sends each card's head revision to the instance's plugin, 20 at a time, and stores the values it returns. " +
			"Its progress counts cards done and failed, with a warning for each value dropped and each card that failed. Cancel the job to stop the run."},
		func(ctx context.Context, in *startEnrichIn) (*jobResp, error) {
			b := in.Body
			if (len(b.CardIDs) > 0) == (b.Query != nil) {
				return nil, huma.Error400BadRequest("send card_ids or query")
			}
			if _, err := s.usable(ctx, b.InstanceID, "enrich"); err != nil {
				return nil, err
			}
			ids := b.CardIDs
			if b.Query != nil {
				var err error
				if ids, err = s.searchIDs(ctx, library.Card, *b.Query); err != nil {
					return nil, s.fail(err)
				}
			}
			if len(ids) == 0 {
				return nil, huma.Error400BadRequest("the query matches no cards")
			}
			if len(ids) > enrich.MaxCards {
				return nil, huma.Error400BadRequest(fmt.Sprintf("a run takes at most %d cards; this one has %d", enrich.MaxCards, len(ids)))
			}
			return s.enqueue(ctx, jobs.Spec{Kind: enrich.Kind, InstanceID: &b.InstanceID, Payload: enrich.Payload{CardIDs: ids}})
		})

	huma.Register(api, huma.Operation{OperationID: "get-card-enrichments", Method: http.MethodGet, Path: "/cards/{id}/enrichments",
		Summary: "Get a card's enrichments", Tags: tags,
		Description: "Every instance's current value of each key, by instance and key. A stale value is about a revision that is no longer the head."},
		func(ctx context.Context, in *cardEnrichmentsIn) (*cardEnrichmentsOut, error) {
			if _, err := s.Lib.Cards.Get(ctx, in.ID); err != nil {
				return nil, s.fail(err)
			}
			vals, err := s.Lib.Enrichments(ctx, in.ID, in.History)
			if err != nil {
				return nil, s.fail(err)
			}
			insts, err := s.Registry.ListInstances(ctx)
			if err != nil {
				return nil, s.fail(err)
			}
			out := &cardEnrichmentsOut{Body: []enrichmentValue{}}
			for _, v := range vals {
				ev := enrichmentValue{Enrichment: v, enrichmentOut: enrichmentOut{Label: v.Key}}
				if i := slices.IndexFunc(insts, func(i plugins.InstanceInfo) bool { return i.ID == v.InstanceID }); i >= 0 {
					ev.InstanceName = insts[i].Name
					if m := s.Registry.Manifests()[insts[i].Plugin]; m != nil {
						if d, ok := m.Enrichments[v.Key]; ok {
							ev.enrichmentOut = toEnrichmentOut(d)
						}
					}
				}
				out.Body = append(out.Body, ev)
			}
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "list-enrichment-keys", Method: http.MethodGet, Path: "/enrichments/keys",
		Summary: "List the score instance's enrichment keys", Tags: tags,
		Description: "The keys the score instance's plugin declares: those that enrichment filters and sort can use. The grid key comes first, then the rest by label."},
		func(ctx context.Context, _ *struct{}) (*enrichmentKeysOut, error) {
			out := &enrichmentKeysOut{}
			out.Body.Keys = []enrichmentKey{}
			id, err := s.scoreInstance(ctx)
			if err != nil {
				return nil, s.fail(err)
			}
			if id == 0 {
				return out, nil
			}
			info, err := s.Registry.GetInstance(ctx, id)
			if err != nil {
				return nil, s.fail(err)
			}
			out.Body.InstanceID = &id
			for k, e := range s.Registry.Manifests()[info.Plugin].Enrichments {
				out.Body.Keys = append(out.Body.Keys, enrichmentKey{k, toEnrichmentOut(e)})
			}
			slices.SortFunc(out.Body.Keys, func(a, b enrichmentKey) int {
				if a.Grid != b.Grid {
					if a.Grid {
						return -1
					}
					return 1
				}
				return cmp.Compare(a.Label, b.Label)
			})
			return out, nil
		})
}

// optionalID is a body field that may be absent, null, or an ID.
type optionalID struct {
	Sent bool
	ID   *int64
}

func (o *optionalID) UnmarshalJSON(b []byte) error {
	o.Sent = true
	return json.Unmarshal(b, &o.ID)
}

func (o optionalID) Schema(r huma.Registry) *huma.Schema {
	return &huma.Schema{Type: "integer", Format: "int64", Nullable: true}
}

// setScoreInstance stores the score_instance setting: an enrich instance, or
// none for nil.
func (s *Server) setScoreInstance(ctx context.Context, id *int64) error {
	if id == nil {
		return s.Registry.SetSetting(ctx, scoreInstanceSetting, "")
	}
	if _, err := s.Registry.InstanceWith(ctx, *id, "enrich"); err != nil && !errors.Is(err, plugins.ErrBroken) {
		if errors.Is(err, plugins.ErrIncapable) {
			return huma.Error400BadRequest(err.Error())
		}
		return s.fail(err)
	}
	return s.Registry.SetSetting(ctx, scoreInstanceSetting, strconv.FormatInt(*id, 10))
}
