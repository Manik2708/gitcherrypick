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
	// What can be disputed is a JUDGEMENT, not a status. An accepted dispute
	// requeues the claim, and refusing the next dispute because the requeue is
	// still in flight would punish the contributor for the platform's own
	// timing (ADR-0007 §6).
	if claim.EvaluatedAt == nil {
		return nil, fmt.Errorf("only a judged claim can be disputed: %w", ErrConflict)
	}

	cooldown, err := s.reevals.Cooldown(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("reading the cooldown: %w", err)
	}
	if !cooldown.CanRequest(s.clock.Now()) {
		return nil, Coded(ErrThrottled, CodeReevaluationCooldown,
			"you may dispute again after %s",
			cooldown.CooldownUntil.Format(time.RFC3339)).
			WithDetail(map[string]any{"cooldown_until": cooldown.CooldownUntil})
	}

	created, err := s.reevals.Create(ctx, &domain.ReevaluationRequest{
		ClaimID: claimID, UserID: id, Reason: reason,
	})
	if err != nil {
		if errors.Is(err, port.ErrConflict) {
			return nil, Coded(ErrConflict, CodeRequestAlreadyOpen,
				"this claim already has an open dispute")
		}
		return nil, fmt.Errorf("creating the dispute: %w", err)
	}
	return created, nil
}

// Status makes the cooldown legible BEFORE a contributor spends a request on a
// refusal.
func (s *ReevaluationService) Status(ctx context.Context, id domain.UserID) (*domain.DisputeStanding, error) {
	cooldown, err := s.reevals.Cooldown(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("reading the cooldown: %w", err)
	}

	standing := domain.DisputeStanding{ClaimsEligible: []domain.ClaimID{}}
	if cooldown != nil {
		standing.Cooldown = *cooldown
	}

	open, err := s.reevals.OpenRequest(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("reading the open dispute: %w", err)
	}
	if open != nil {
		requestID := open.ID
		standing.PendingRequestID = &requestID
	}

	// Nothing is eligible while blocked. Listing claims a contributor cannot
	// act on would be an invitation to a refusal.
	standing.Blocker = standing.BlockedBy(s.clock.Now())
	if standing.Blocker != domain.NotBlocked {
		return &standing, nil
	}

	claims, err := s.claims.ListByUser(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("listing claims: %w", err)
	}
	for _, c := range claims {
		if c.EvaluatedAt != nil {
			standing.ClaimsEligible = append(standing.ClaimsEligible, c.ID)
		}
	}
	return &standing, nil
}
