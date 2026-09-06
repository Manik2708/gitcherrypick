package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// SavedSearchRepository stores filter sets.
//
// A saved search stores a QUESTION, never an answer: the filters are replayed
// as the caller, so the ADR-0008 gates apply to whoever runs it rather than to
// whoever saved it. A colleague replaying a search still cannot see themselves.
//
// Org-scoped like shortlists, and for the same reason.
type SavedSearchRepository struct{ db *DB }

// SavedSearches returns the saved-search repository.
func (db *DB) SavedSearches() *SavedSearchRepository { return &SavedSearchRepository{db: db} }

var _ port.SavedSearchRepository = (*SavedSearchRepository)(nil)

// ByID reads one saved search.
func (r *SavedSearchRepository) ByID(ctx context.Context, id domain.SavedSearchID) (*domain.SavedSearch, error) {
	s, err := scanSavedSearch(r.db.pool.QueryRow(ctx,
		`SELECT id, organization_id, name, filters, created_by, created_at
		 FROM saved_searches WHERE id = $1`, string(id)))
	if err != nil {
		return nil, translate(err, fmt.Sprintf("saved search %s", id))
	}
	return s, nil
}

// ListByOrganization reads an org's saved searches.
func (r *SavedSearchRepository) ListByOrganization(ctx context.Context, id domain.OrganizationID) ([]domain.SavedSearch, error) {
	rows, err := r.db.pool.Query(ctx,
		`SELECT id, organization_id, name, filters, created_by, created_at
		 FROM saved_searches WHERE organization_id = $1
		 ORDER BY created_at`, string(id))
	if err != nil {
		return nil, translate(err, fmt.Sprintf("listing saved searches for %s", id))
	}
	defer rows.Close()

	var out []domain.SavedSearch
	for rows.Next() {
		s, err := scanSavedSearch(rows)
		if err != nil {
			return nil, translate(err, "scanning saved search")
		}
		out = append(out, *s)
	}
	return out, translate(rows.Err(), "listing saved searches")
}

// Create stores a filter set.
//
// The filters go in as jsonb, verbatim. Validating them against the ADR-0008
// parameter set is the SERVICE's job — a repository that rejected an unknown
// key would be making a product decision about what a search may ask.
func (r *SavedSearchRepository) Create(ctx context.Context, s *domain.SavedSearch) (*domain.SavedSearch, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generating saved search id: %w", err)
	}

	filters, err := json.Marshal(s.Filters)
	if err != nil {
		return nil, fmt.Errorf("encoding filters: %w", err)
	}

	created, err := scanSavedSearch(r.db.pool.QueryRow(ctx, `
		INSERT INTO saved_searches (id, organization_id, name, filters, created_by)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, organization_id, name, filters, created_by, created_at`,
		id.String(), string(s.OrganizationID), s.Name, string(filters), string(s.CreatedBy)))
	if err != nil {
		return nil, translate(err, "creating saved search")
	}
	return created, nil
}

// Delete removes one.
func (r *SavedSearchRepository) Delete(ctx context.Context, id domain.SavedSearchID) error {
	tag, err := r.db.pool.Exec(ctx, `DELETE FROM saved_searches WHERE id = $1`, string(id))
	if err != nil {
		return translate(err, fmt.Sprintf("deleting saved search %s", id))
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("saved search %s: %w", id, port.ErrNotFound)
	}
	return nil
}

func scanSavedSearch(row rowScanner) (*domain.SavedSearch, error) {
	var (
		s       domain.SavedSearch
		filters []byte
	)
	if err := row.Scan(&s.ID, &s.OrganizationID, &s.Name, &filters, &s.CreatedBy, &s.CreatedAt); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(filters, &s.Filters); err != nil {
		return nil, fmt.Errorf("decoding filters: %w", err)
	}
	return &s, nil
}
