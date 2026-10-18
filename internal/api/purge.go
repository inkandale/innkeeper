package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/danielgtaylor/huma/v2"

	"github.com/inkandale/innkeeper/internal/bulk"
	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/tier"
)

type purgeIn struct {
	Body struct {
		Query   library.Query `json:"query" doc:"Selects the cards, as in POST /cards/search. Paging is ignored. It needs a filter or text."`
		Confirm string        `json:"confirm" doc:"How many cards the query matches, as digits. A count that has changed is refused with the new one."`
	}
}

func (s *Server) purgeRoutes(api huma.API) {
	huma.Register(api, huma.Operation{Metadata: needs(tier.Destructive), OperationID: "purge-cards", Method: http.MethodPost, Path: "/cards/purge",
		Summary: "Delete the cards a query matches, for good",
		Description: "Queues a job that deletes the matching cards with their history, sources, and collection places. " +
			"This can't be undone. A card bound to a target app is kept, and the job's progress lists it. " +
			"Lorebooks stay. The next blob cleanup removes images nothing else uses.",
		Tags: []string{"Cards"}, DefaultStatus: http.StatusAccepted},
		func(ctx context.Context, in *purgeIn) (*jobResp, error) {
			q := in.Body.Query
			if q.Filter == nil && q.Text == "" {
				return nil, huma.Error400BadRequest("purge needs a filter or text; to select every card, filter on archived")
			}
			ids, err := s.searchIDs(ctx, library.Card, q)
			if err != nil {
				return nil, s.fail(err)
			}
			if len(ids) == 0 {
				return nil, huma.Error400BadRequest("the query matches no cards")
			}
			if in.Body.Confirm != strconv.Itoa(len(ids)) {
				return nil, huma.Error422UnprocessableEntity(fmt.Sprintf("confirm must be %d, the number of cards the query matches", len(ids)))
			}
			return s.enqueue(ctx, jobs.Spec{Kind: bulk.PurgeKind, Payload: bulk.PurgePayload{CardIDs: ids}})
		})
}
