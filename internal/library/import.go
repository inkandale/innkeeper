package library

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

const maxSlugLen = 60

// Slugify turns a name into a slug: lowercase ASCII letters and digits in
// runs joined by single hyphens.
//
// ponytail: other letters are dropped, so a name in another script becomes
// "untitled"; transliterate if that gets common.
func Slugify(name string) string {
	var b strings.Builder
	gap := false
	for _, r := range strings.ToLower(name) {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			gap = true
			continue
		}
		if gap && b.Len() > 0 {
			b.WriteByte('-')
		}
		gap = false
		b.WriteRune(r)
	}
	s := strings.TrimRight(b.String()[:min(b.Len(), maxSlugLen)], "-")
	if s == "" {
		return "untitled"
	}
	return s
}

// freeSlug returns base, or base-N with N one more than the highest suffix
// in use.
func (s *Entities) freeSlug(ctx context.Context, base string) (string, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT coalesce(max(CASE WHEN slug = $1 THEN 1
			ELSE substring(slug from '-([0-9]{1,9})$')::int END), 0)
		FROM `+s.k.table+` WHERE slug = $1 OR slug ~ ('^' || $1 || '-[0-9]{1,9}$')`, base).Scan(&n)
	if err != nil || n == 0 {
		return base, err
	}
	return fmt.Sprintf("%s-%d", base, n+1), nil
}

// Import adds an entity from a plugin, with a slug made from name, unless an
// entity with the same provenance has ever had this content. It returns the
// entity's ID and whether it created it, so importing the same item twice,
// or again after the owner edited it, adds nothing.
//
// ponytail: a source item whose content changed is imported as a new entity,
// not a new revision of the old one.
func (s *Entities) Import(ctx context.Context, provenance, name string, e Edit) (int64, bool, error) {
	r, err := s.prepare(e)
	if err != nil {
		return 0, false, err
	}
	var id int64
	err = s.pool.QueryRow(ctx, `SELECT e.id FROM `+s.k.table+` e WHERE e.provenance = $1 AND EXISTS (
		SELECT 1 FROM `+s.k.revTable+` r WHERE r.`+s.k.fk+` = e.id AND r.content_hash = $2)
		ORDER BY e.id LIMIT 1`, provenance, r.contentHash).Scan(&id)
	if err == nil {
		return id, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, false, err
	}
	// Retried because another import can take the same slug first.
	for range 5 {
		slug, err := s.freeSlug(ctx, Slugify(name))
		if err != nil {
			return 0, false, err
		}
		ent, _, err := s.Create(ctx, slug, provenance, e)
		if errors.Is(err, ErrSlugTaken) {
			continue
		}
		if err != nil {
			return 0, false, err
		}
		return ent.ID, true, nil
	}
	return 0, false, ErrSlugTaken
}
