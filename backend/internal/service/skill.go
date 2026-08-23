package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// SkillService is the contributor's view of the catalogue.
type SkillService struct {
	skills port.SkillRepository
}

// NewSkillService wires the catalogue.
func NewSkillService(skills port.SkillRepository) *SkillService {
	return &SkillService{skills: skills}
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
// Deduped by the repository before it reaches the queue: most noise is
// spelling variants and no admin should spend attention on them (ADR-0003).
func (s *SkillService) RequestSkill(ctx context.Context, id domain.UserID, proposedName, rationale string) (domain.RequestID, error) {
	if proposedName == "" || rationale == "" {
		return "", fmt.Errorf("a request needs a name and a rationale: %w", ErrInvalid)
	}

	requestID, err := s.skills.CreateRequest(ctx, id, proposedName, rationale)
	if err != nil {
		if errors.Is(err, port.ErrConflict) {
			// The proposal matches an existing skill. Said plainly, because
			// the contributor can act on it — they claim the existing one.
			return "", fmt.Errorf("%w: %w", err, ErrConflict)
		}
		return "", fmt.Errorf("requesting the skill: %w", err)
	}
	return requestID, nil
}

// MySkills reads the caller's own standings.
func (s *SkillService) MySkills(ctx context.Context, id domain.UserID) ([]domain.UserSkill, error) {
	out, err := s.skills.UserSkills(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("reading skills: %w", err)
	}
	return out, nil
}
