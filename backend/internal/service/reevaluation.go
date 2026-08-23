package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// ReevaluationService is the contributor's side of disputes.
type ReevaluationService struct {
	reevals port.ReevaluationRepository
	claims  port.ClaimRepository
	clock   port.Clock
}

// NewReevaluationService wires disputes.
func NewReevaluationService(
	reevals port.ReevaluationRepository,
	claims port.ClaimRepository,
	clock port.Clock,
) *ReevaluationService {
	return &ReevaluationService{reevals: reevals, claims: claims, clock: clock}
}

var _ port.ReevaluationService = (*ReevaluationService)(nil)

// Request disputes a judgement.
//
// Refused while cooling down, WITH the expiry date, so a contributor knows
// when they may try again rather than only that they may not (ADR-0007 §6).
func (s *ReevaluationService) Request(ctx context.Context, id domain.UserID, claimID domain.ClaimID, reason string) (*domain.ReevaluationRequest, error) {
	if reason == "" {
		return nil, fmt.Errorf("a dispute needs a reason: %w", ErrInvalid)
	}

	claim, err := s.claims.ByID(ctx, claimID)
	if err != nil {
		return nil, ErrNotFound
	}
	if claim.UserID != id {
		return nil, ErrNotFound
	}
	if claim.Status != domain.ClaimEvaluated {
		return nil, fmt.Errorf("only an evaluated claim can be disputed: %w", ErrConflict)
	}

	cooldown, err := s.reevals.Cooldown(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("reading the cooldown: %w", err)
	}
	if !cooldown.CanRequest(s.clock.Now()) {
		return nil, fmt.Errorf("you may dispute again after %s: %w",
			cooldown.CooldownUntil.Format(time.RFC3339), ErrConflict)
	}

	created, err := s.reevals.Create(ctx, &domain.ReevaluationRequest{
		ClaimID: claimID, UserID: id, Reason: reason,
	})
	if err != nil {
		if errors.Is(err, port.ErrConflict) {
			return nil, fmt.Errorf("this claim already has an open dispute: %w", ErrConflict)
		}
		return nil, fmt.Errorf("creating the dispute: %w", err)
	}
	return created, nil
}

// Status makes the cooldown legible BEFORE a contributor spends a request on a
// refusal.
func (s *ReevaluationService) Status(ctx context.Context, id domain.UserID) (*domain.Cooldown, error) {
	c, err := s.reevals.Cooldown(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("reading the cooldown: %w", err)
	}
	return c, nil
}
