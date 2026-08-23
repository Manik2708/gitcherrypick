package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// AdminService owns the queues only an administrator may drain.
//
// Three of them: hirer verification, skill requests, and re-evaluation
// disputes. Each is a human decision the platform cannot make for itself, and
// each is final — re-deciding is a conflict rather than an overwrite, because
// these are records of what an admin concluded rather than mutable state.
type AdminService struct {
	admins  port.AdminRepository
	skills  port.SkillRepository
	orgs    port.OrganizationRepository
	reevals port.ReevaluationRepository
	claims  port.ClaimRepository
	broker  port.Broker
	tx      port.TxManager
}

// NewAdminService wires the admin queues.
func NewAdminService(
	admins port.AdminRepository,
	skills port.SkillRepository,
	orgs port.OrganizationRepository,
	reevals port.ReevaluationRepository,
	claims port.ClaimRepository,
	broker port.Broker,
	tx port.TxManager,
) *AdminService {
	return &AdminService{admins: admins, skills: skills, orgs: orgs,
		reevals: reevals, claims: claims, broker: broker, tx: tx}
}

var _ port.AdminService = (*AdminService)(nil)

// PendingVerifications drains the verification queue.
func (s *AdminService) PendingVerifications(ctx context.Context, p domain.Principal) ([]port.VerificationRequest, error) {
	if _, err := requireAdmin(p); err != nil {
		return nil, err
	}
	out, err := s.admins.PendingVerifications(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing verifications: %w", err)
	}
	return out, nil
}

// DecideVerification approves or rejects a hirer or organization.
func (s *AdminService) DecideVerification(ctx context.Context, p domain.Principal, id domain.RequestID, approve bool, reason string) error {
	admin, err := requireAdmin(p)
	if err != nil {
		return err
	}
	// A rejection the applicant cannot read produces a resubmission of the
	// same thing.
	if !approve && reason == "" {
		return fmt.Errorf("a rejection must carry a reason: %w", ErrInvalid)
	}

	err = s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		return s.admins.DecideVerification(ctx, tx, id, admin, approve, reason)
	})
	if err != nil {
		if errors.Is(err, port.ErrConflict) {
			return fmt.Errorf("this request has already been decided: %w", ErrConflict)
		}
		return fmt.Errorf("deciding verification: %w", err)
	}
	return nil
}

// PendingSkillRequests drains the catalogue queue.
func (s *AdminService) PendingSkillRequests(ctx context.Context, p domain.Principal, status string) ([]port.SkillRequest, error) {
	if _, err := requireAdmin(p); err != nil {
		return nil, err
	}
	out, err := s.skills.PendingRequests(ctx, status)
	if err != nil {
		return nil, fmt.Errorf("listing skill requests: %w", err)
	}
	return out, nil
}

// DecideSkillRequest approves a proposed skill into the catalogue, or rejects
// it.
//
// Approval is the sharpest privilege on the platform: a bad slug fragments a
// population forever, because standing is per skill and two spellings never
// merge. Creating the skill and closing the request happen in ONE transaction —
// a skill with no request that produced it, or a request marked approved with
// no skill, are both states nothing could explain.
func (s *AdminService) DecideSkillRequest(ctx context.Context, p domain.Principal, id domain.RequestID, approve bool, skill *domain.Skill, reason string) (*domain.Skill, error) {
	admin, err := requireAdmin(p)
	if err != nil {
		return nil, err
	}
	if !approve && reason == "" {
		return nil, fmt.Errorf("a rejection must carry a reason: %w", ErrInvalid)
	}
	if approve && skill == nil {
		return nil, fmt.Errorf("approving requires the skill to create: %w", ErrInvalid)
	}

	var created *domain.Skill
	err = s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		if approve {
			var err error
			created, err = s.skills.Create(ctx, tx, skill)
			if err != nil {
				return err
			}
		}
		return s.skills.DecideRequest(ctx, tx, id, admin, approve, reason, created)
	})
	if err != nil {
		if errors.Is(err, port.ErrConflict) {
			// Either the slug or an alias collides with an existing skill, or
			// the request was already decided. Both are conflicts the admin
			// must see rather than a 500.
			return nil, fmt.Errorf("%w: %w", err, ErrConflict)
		}
		return nil, fmt.Errorf("deciding skill request: %w", err)
	}
	return created, nil
}

// PendingReevaluations drains the dispute queue.
func (s *AdminService) PendingReevaluations(ctx context.Context, p domain.Principal) ([]domain.ReevaluationRequest, error) {
	if _, err := requireAdmin(p); err != nil {
		return nil, err
	}
	out, err := s.reevals.Pending(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing re-evaluations: %w", err)
	}
	return out, nil
}

// DecideReevaluation accepts or rejects a dispute.
//
// Accepting RE-QUEUES the claim: the contributor's argument was accepted, so
// the evidence is judged again. That re-queue joins the same transaction as the
// decision — a dispute marked accepted with no job would leave the contributor
// told they were right and nothing happening.
//
// Rejecting advances the cooldown, which the repository handles because the
// arithmetic lives in domain.Cooldown. Acceptance touches neither counter.
func (s *AdminService) DecideReevaluation(ctx context.Context, p domain.Principal, id domain.RequestID, accept bool, reason string) error {
	admin, err := requireAdmin(p)
	if err != nil {
		return err
	}
	if !accept && reason == "" {
		return fmt.Errorf("a rejection must carry a reason: %w", ErrInvalid)
	}

	pending, err := s.reevals.Pending(ctx)
	if err != nil {
		return fmt.Errorf("reading the dispute queue: %w", err)
	}
	var target *domain.ReevaluationRequest
	for i := range pending {
		if pending[i].ID == id {
			target = &pending[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("this dispute is not pending: %w", ErrConflict)
	}

	err = s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		if err := s.reevals.Decide(ctx, tx, id, admin, accept, reason); err != nil {
			return err
		}
		if !accept {
			return nil
		}
		claim, err := s.claims.ByID(ctx, target.ClaimID)
		if err != nil {
			return err
		}
		if err := s.claims.SetStatus(ctx, tx, target.ClaimID, domain.ClaimQueued); err != nil {
			return err
		}
		return s.broker.Publish(ctx, tx, port.Message{
			Kind: "claims.live", ClaimID: target.ClaimID, Version: claim.Version,
		})
	})
	if err != nil {
		if errors.Is(err, port.ErrConflict) {
			return fmt.Errorf("this dispute has already been decided: %w", ErrConflict)
		}
		return fmt.Errorf("deciding re-evaluation: %w", err)
	}
	return nil
}
