package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// SkillRequestLimit is how many catalogue proposals one contributor may make
// in SkillRequestWindow (ADR-0003).
//
// Deduplicated proposals do NOT count: they never entered the queue, so they
// cost no admin attention, and charging for them would punish somebody for not
// knowing the catalogue.
const (
	SkillRequestLimit  = 3
	SkillRequestWindow = 7 * 24 * time.Hour
)

// SkillService is the contributor's view of the catalogue.
type SkillService struct {
	skills port.SkillRepository
	clock  port.Clock
}

// NewSkillService wires the catalogue.
func NewSkillService(skills port.SkillRepository, clock port.Clock) *SkillService {
	return &SkillService{skills: skills, clock: clock}
}

var _ port.SkillService = (*SkillService)(nil)

// Search resolves a query against names and aliases.
func (s *SkillService) Search(ctx context.Context, query string) ([]port.SkillMatch, error) {
	if query == "" {
		return nil, fmt.Errorf("a search needs a query: %w", ErrInvalid)
	}
	out, err := s.skills.Search(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("searching the catalogue: %w", err)
	}
	return out, nil
}

// RequestSkill proposes a new catalogue entry.
//
// Deduped BEFORE the queue: most noise is spelling variants, and no admin
// should spend attention on them (ADR-0003). Rate limited after that, so a
// contributor cannot flood the queue with genuinely new names.
//
// The order matters. Checking the limit first would let a contributor exhaust
// their quota on three proposals that were all duplicates — spending it on
// requests that never reached anybody.
func (s *SkillService) RequestSkill(ctx context.Context, id domain.UserID, proposedName, rationale string) (domain.RequestID, error) {
	if proposedName == "" || rationale == "" {
		return "", fmt.Errorf("a request needs a name and a rationale: %w", ErrInvalid)
	}

	existing, err := s.skills.MatchSkill(ctx, proposedName)
	switch {
	case err == nil:
		// The contributor can act on this: they claim the existing skill. The
		// match travels as data so a client can offer that directly.
		return "", Coded(ErrConflict, CodeSkillAlreadyExists,
			"%q matches skill %q", proposedName, existing.Slug).
			WithDetail(map[string]any{
				"matched_skill": map[string]string{
					"slug": existing.Slug, "name": existing.Name,
				},
				"message": fmt.Sprintf("'%s' matches the existing skill '%s'.",
					proposedName, existing.Name),
			})
	case !errors.Is(err, port.ErrNotFound):
		return "", fmt.Errorf("matching the proposal: %w", err)
	}

	if err := s.withinRequestLimit(ctx, id); err != nil {
		return "", err
	}

	requestID, err := s.skills.CreateRequest(ctx, id, proposedName, rationale)
	if err != nil {
		return "", fmt.Errorf("requesting the skill: %w", err)
	}
	return requestID, nil
}

// withinRequestLimit enforces the rolling window.
//
// Rolling rather than a fixed weekly reset: a calendar week lets somebody spend
// three on Sunday night and three more on Monday morning, which is six in a
// couple of hours.
func (s *SkillService) withinRequestLimit(ctx context.Context, id domain.UserID) error {
	now := s.clock.Now()

	recent, err := s.skills.RequestsSince(ctx, id, now.Add(-SkillRequestWindow))
	if err != nil {
		return fmt.Errorf("counting recent requests: %w", err)
	}
	if len(recent) < SkillRequestLimit {
		return nil
	}

	// The window lifts when the OLDEST request in it ages out, which is the
	// only honest answer to "when may I try again".
	retryAfter := recent[0].CreatedAt.Add(SkillRequestWindow)

	return Coded(ErrConflict, CodeRateLimited,
		"%d requests in the last %s", len(recent), SkillRequestWindow).
		WithDetail(map[string]any{
			"limit":       SkillRequestLimit,
			"window":      "7d",
			"retry_after": retryAfter.UTC().Format(time.RFC3339),
		})
}

// MySkills reads the caller's own standings.
func (s *SkillService) MySkills(ctx context.Context, id domain.UserID) ([]domain.UserSkill, error) {
	out, err := s.skills.UserSkills(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("reading skills: %w", err)
	}
	return out, nil
}
