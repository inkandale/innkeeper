package library

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var sourceKindRE = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

const sourceCols = `id, kind, ref, coalesce(external_id, ''), coalesce(url, ''), linked_by, created_at`

func scanSource(row pgx.CollectableRow) (Source, error) {
	var s Source
	err := row.Scan(&s.ID, &s.Kind, &s.Ref, &s.ExternalID, &s.URL, &s.LinkedBy, &s.CreatedAt)
	return s, err
}

// Sources lists the entity's sources, leaving out removed ones.
func (s *Entities) Sources(ctx context.Context, id int64) ([]Source, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+sourceCols+` FROM `+s.k.SourceTable+`
		WHERE `+s.k.FK+` = $1 AND removed_at IS NULL ORDER BY created_at, id`, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanSource)
}

// AddSource records src as owner-linked, restoring a removed row or taking over an auto one.
func (s *Entities) AddSource(ctx context.Context, id int64, src Source) (Source, error) {
	if !sourceKindRE.MatchString(src.Kind) || src.Ref == "" {
		return Source{}, fmt.Errorf("%w: a source needs a kind and a ref", ErrInvalid)
	}
	rows, err := s.pool.Query(ctx, `INSERT INTO `+s.k.SourceTable+` (`+s.k.FK+`, kind, ref, external_id, url, linked_by)
		VALUES ($1, $2, $3, nullif($4, ''), nullif($5, ''), 'owner')
		ON CONFLICT (`+s.k.FK+`, kind, ref) DO UPDATE SET linked_by = 'owner', removed_at = NULL,
			external_id = coalesce(EXCLUDED.external_id, `+s.k.SourceTable+`.external_id),
			url = coalesce(EXCLUDED.url, `+s.k.SourceTable+`.url)
		RETURNING `+sourceCols, id, src.Kind, src.Ref, src.ExternalID, src.URL)
	if err != nil {
		return Source{}, mapErr(err)
	}
	got, err := pgx.CollectExactlyOneRow(rows, scanSource)
	if err != nil {
		return Source{}, mapErr(err)
	}
	return got, nil
}

// RemoveSource hides one of the entity's sources so detection won't add it back.
func (s *Entities) RemoveSource(ctx context.Context, id, sourceID int64) error {
	tag, err := s.pool.Exec(ctx, `UPDATE `+s.k.SourceTable+` SET removed_at = now()
		WHERE id = $1 AND `+s.k.FK+` = $2 AND removed_at IS NULL`, sourceID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DetectSources records the sources data names as auto-linked; removed and owner rows stay as they are.
func (s *Entities) DetectSources(ctx context.Context, id int64, data json.RawMessage) error {
	return s.recordSources(ctx, s.pool, id, Detect(data, s.k == Lorebook))
}

// RecordSourcesTx records srcs as auto-linked inside the caller's transaction; removed and owner rows stay as they are.
func (s *Entities) RecordSourcesTx(ctx context.Context, tx pgx.Tx, id int64, srcs []Source) error {
	return s.recordSources(ctx, tx, id, srcs)
}

func (s *Entities) recordSources(ctx context.Context, q interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, id int64, srcs []Source) error {
	// An existing row is left as is on purpose, so detection never overwrites what the owner set.
	for _, src := range srcs {
		_, err := q.Exec(ctx, `INSERT INTO `+s.k.SourceTable+` (`+s.k.FK+`, kind, ref, external_id, url, linked_by)
			VALUES ($1, $2, $3, nullif($4, ''), nullif($5, ''), 'auto') ON CONFLICT DO NOTHING`,
			id, src.Kind, src.Ref, src.ExternalID, src.URL)
		if err != nil {
			return mapErr(err)
		}
	}
	return nil
}

// BySource returns the IDs of entities with this source, in ascending order.
func (s *Entities) BySource(ctx context.Context, kind, ref string) ([]int64, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+s.k.FK+` FROM `+s.k.SourceTable+`
		WHERE kind = $1 AND ref = $2 AND removed_at IS NULL ORDER BY 1`, kind, ref)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}
