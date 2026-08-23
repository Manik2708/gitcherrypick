package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// DiscoveryService owns search, leaderboards, scorecards and rank.
//
// Most of the work is in the repository's one ranked query — the gates are SQL,
// because a post-filtered gate is a gate that leaks into a result count
// (ADR-0008 §1). What lives here is deciding WHO may ask, validating the filter
// set, and the asymmetry between what a hirer sees and what a contributor sees
// of themselves.
type DiscoveryService struct {
	search port.SearchRepository
	skills port.SkillRepository
	saved  port.SavedSearchRepository
	access port.AccessService
}

// NewDiscoveryService wires discovery.
func NewDiscoveryService(
	search port.SearchRepository,
	skills port.SkillRepository,
	saved port.SavedSearchRepository,
	access port.AccessService,
) *DiscoveryService {
	return &DiscoveryService{search: search, skills: skills, saved: saved, access: access}
}

var _ port.DiscoveryService = (*DiscoveryService)(nil)

// Search runs the ranked query for a capable hirer.
func (s *DiscoveryService) Search(ctx context.Context, p domain.Principal, q domain.SearchQuery) (*domain.SearchResults, error) {
	hirer, err := requireHirer(p)
	if err != nil {
		return nil, err
	}
	if err := s.access.RequireHiringCapability(ctx, p); err != nil {
		return nil, err
	}
	if err := s.validateFilters(ctx, q); err != nil {
		return nil, err
	}

	results, err := s.search.Search(ctx, hirer.ID, q)
	if err != nil {
		if errors.Is(err, port.ErrConflict) {
			// The per_page cap. A bad request, not a server failure.
			return nil, fmt.Errorf("%w: %w", err, ErrInvalid)
		}
		return nil, fmt.Errorf("searching: %w", err)
	}
	return results, nil
}

// validateFilters rejects a filter set that could never match.
//
// An unknown key is refused by the CONTROLLER, which parses the query string;
// what this checks is values that parse but cannot be satisfied. A skill
// outside the catalogue is the important one: it would silently return nothing,
// and a recruiter would read that as "nobody has this skill" rather than "you
// typed a skill that does not exist".
func (s *DiscoveryService) validateFilters(ctx context.Context, q domain.SearchQuery) error {
	for _, slug := range q.Skills {
		if _, err := s.skills.BySlug(ctx, slug); err != nil {
			if errors.Is(err, port.ErrNotFound) {
				return fmt.Errorf("%q is not a skill in the catalogue: %w", slug, ErrInvalid)
			}
			return fmt.Errorf("resolving skill %q: %w", slug, err)
		}
	}

	// Skill and overall scores are ceiled at 100 (ADR-0007), so a higher
	// threshold is not a narrow filter — it is an empty one, and saying so
	// beats returning zero results.
	if q.MinSkillScore != nil && *q.MinSkillScore > 100 {
		return fmt.Errorf("min_skill_score is at most 100: %w", ErrInvalid)
	}
	if q.MinOverallScore != nil && *q.MinOverallScore > 100 {
		return fmt.Errorf("min_overall_score is at most 100: %w", ErrInvalid)
	}
	// min_generalist_score is deliberately unbounded — the breadth score has no
	// ceiling (ADR-0007 §5).
	return nil
}

// Leaderboard reads a board.
//
// Boards show EVERYONE, inactive included, and take no include_inactive: the
// board is where a hirer finds who fills a gap in a search (ADR-0008 §1).
func (s *DiscoveryService) Leaderboard(ctx context.Context, p domain.Principal, kind domain.LeaderboardKind, skill string) (*domain.Leaderboard, error) {
	if _, err := requireHirer(p); err != nil {
		return nil, err
	}
	if err := s.access.RequireHiringCapability(ctx, p); err != nil {
		return nil, err
	}

	var skillID *domain.SkillID
	if kind == domain.BoardSkill {
		if skill == "" {
			return nil, fmt.Errorf("kind=skill requires a skill: %w", ErrInvalid)
		}
		resolved, err := s.skills.BySlug(ctx, skill)
		if err != nil {
			if errors.Is(err, port.ErrNotFound) {
				return nil, fmt.Errorf("%q is not a skill in the catalogue: %w", skill, ErrInvalid)
			}
			return nil, fmt.Errorf("resolving skill: %w", err)
		}
		skillID = &resolved.ID
	}

	board, err := s.search.Leaderboard(ctx, kind, skillID, 0)
	if err != nil {
		if errors.Is(err, port.ErrConflict) {
			return nil, fmt.Errorf("%w: %w", err, ErrInvalid)
		}
		return nil, fmt.Errorf("reading leaderboard: %w", err)
	}
	return board, nil
}

// Scorecard reads the full evidence dossier.
//
// AssertNotSelf and a hidden contributor both surface as NOT FOUND. A
// distinguishable response would confirm the identity link, which is the one
// thing the self-exclusion is meant to keep quiet.
func (s *DiscoveryService) Scorecard(ctx context.Context, p domain.Principal, target domain.UserID) (*domain.Scorecard, error) {
	hirer, err := requireHirer(p)
	if err != nil {
		return nil, err
	}
	if err := s.access.RequireHiringCapability(ctx, p); err != nil {
		return nil, err
	}
	if err := s.access.AssertNotSelf(ctx, hirer.ID, target); err != nil {
		if errors.Is(err, ErrSelf) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	card, err := s.search.Scorecard(ctx, hirer.ID, target)
	if err != nil {
		if errors.Is(err, port.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("reading scorecard: %w", err)
	}
	return card, nil
}

// MyRank returns the caller's own position.
//
// Only their own (ADR-0005). There is no method here that takes another
// contributor's id, because the number a contributor may see is theirs.
func (s *DiscoveryService) MyRank(ctx context.Context, id domain.UserID) (*domain.Rank, error) {
	rank, err := s.search.Rank(ctx, id)
	if err != nil {
		if errors.Is(err, port.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("reading rank: %w", err)
	}
	return rank, nil
}

// SaveSearch stores a filter set.
//
// Validated on the way in, so a saved search cannot hold filters that would be
// rejected on replay — otherwise a recruiter saves something that works today
// and fails tomorrow for reasons they cannot see.
func (s *DiscoveryService) SaveSearch(ctx context.Context, p domain.Principal, name string, q domain.SearchQuery) (*domain.SavedSearch, error) {
	hirer, err := requireHirer(p)
	if err != nil {
		return nil, err
	}
	if err := s.access.RequireHiringCapability(ctx, p); err != nil {
		return nil, err
	}
	if err := s.validateFilters(ctx, q); err != nil {
		return nil, err
	}

	saved, err := s.saved.Create(ctx, &domain.SavedSearch{
		OrganizationID: hirer.OrganizationID, Name: name, Filters: q, CreatedBy: hirer.ID,
	})
	if err != nil {
		return nil, fmt.Errorf("saving search: %w", err)
	}
	return saved, nil
}

// SavedSearches lists the organization's saved searches.
func (s *DiscoveryService) SavedSearches(ctx context.Context, p domain.Principal) ([]domain.SavedSearch, error) {
	hirer, err := requireHirer(p)
	if err != nil {
		return nil, err
	}
	if err := s.access.RequireHiringCapability(ctx, p); err != nil {
		return nil, err
	}

	out, err := s.saved.ListByOrganization(ctx, hirer.OrganizationID)
	if err != nil {
		return nil, fmt.Errorf("listing saved searches: %w", err)
	}
	return out, nil
}

// ReplaySavedSearch evaluates stored filters AS THE CALLER.
//
// The gates are properties of the requester, not of the saved query: a
// colleague replaying a search still cannot see themselves, and a search saved
// while an org was verified fails if that verification is later revoked. A
// saved search stores a question, never an answer (ADR-0008 §5).
func (s *DiscoveryService) ReplaySavedSearch(ctx context.Context, p domain.Principal, id domain.SavedSearchID) (*domain.SearchResults, error) {
	hirer, err := requireHirer(p)
	if err != nil {
		return nil, err
	}
	if err := s.access.RequireHiringCapability(ctx, p); err != nil {
		return nil, err
	}

	saved, err := s.ownedSavedSearch(ctx, hirer, id)
	if err != nil {
		return nil, err
	}
	return s.Search(ctx, p, saved.Filters)
}

// DeleteSavedSearch removes one the caller's organization owns.
func (s *DiscoveryService) DeleteSavedSearch(ctx context.Context, p domain.Principal, id domain.SavedSearchID) error {
	hirer, err := requireHirer(p)
	if err != nil {
		return err
	}
	if err := s.access.RequireHiringCapability(ctx, p); err != nil {
		return err
	}
	if _, err := s.ownedSavedSearch(ctx, hirer, id); err != nil {
		return err
	}

	if err := s.saved.Delete(ctx, id); err != nil {
		return fmt.Errorf("deleting saved search: %w", err)
	}
	return nil
}

// ownedSavedSearch reads a saved search the caller's organization owns.
//
// Another org's is NOT FOUND. A competitor must not be able to confirm that a
// given id belongs to somebody.
func (s *DiscoveryService) ownedSavedSearch(ctx context.Context, hirer *domain.Hirer, id domain.SavedSearchID) (*domain.SavedSearch, error) {
	saved, err := s.saved.ByID(ctx, id)
	if err != nil {
		if errors.Is(err, port.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("reading saved search: %w", err)
	}
	if saved.OrganizationID != hirer.OrganizationID {
		return nil, ErrNotFound
	}
	return saved, nil
}
