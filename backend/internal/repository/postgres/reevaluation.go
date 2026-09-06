package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// ReevaluationRepository owns disputes and the escalating cooldown.
//
// The dispute flow is not only a complaints channel: every ACCEPTED request is
// a human-ranked disagreement on real evidence, which is the labelled set
// calibration said could not exist before launch (ADR-0007 §6).
//
// The escalation arithmetic lives in domain.Cooldown, not here. It is pure
// arithmetic, so keeping it out of SQL means it can be unit-tested without a
// database — and there is exactly one implementation of the rule rather than
// one in Go and one in an UPDATE.
type ReevaluationRepository struct{ db *DB }

// Reevaluations returns the dispute repository.
func (db *DB) Reevaluations() *ReevaluationRepository { return &ReevaluationRepository{db: db} }

var _ port.ReevaluationRepository = (*ReevaluationRepository)(nil)

// Create records a dispute.
func (r *ReevaluationRepository) Create(ctx context.Context, req *domain.ReevaluationRequest) (*domain.ReevaluationRequest, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generating request id: %w", err)
	}

	var created domain.ReevaluationRequest
	err = r.db.pool.QueryRow(ctx, `
		INSERT INTO reevaluation_requests (id, claim_id, user_id, reason, status)
		VALUES ($1, $2, $3, $4, 'pending')
		RETURNING id, claim_id, user_id, reason, status, reviewed_by, reviewed_at,
		          coalesce(decision_note, ''), created_at`,
		id.String(), string(req.ClaimID), string(req.UserID), req.Reason,
	).Scan(&created.ID, &created.ClaimID, &created.UserID, &created.Reason, &created.Status,
		&created.ReviewedBy, &created.ReviewedAt, &created.Decision, &created.CreatedAt)
	if err != nil {
		return nil, translate(err, "creating re-evaluation request")
	}

	// A zero cooldown row, created with the first dispute and untouched by an
	// acceptance: being right costs nothing (ADR-0007 §6). Materialising it
	// here rather than on the first REJECTION means the throttle state is
	// readable from the moment a contributor starts disputing, instead of
	// appearing only once they have been wrong.
	if _, err := r.db.pool.Exec(ctx, `
		INSERT INTO reevaluation_cooldowns (user_id, rejection_count, tier)
		VALUES ($1, 0, 0)
		ON CONFLICT (user_id) DO NOTHING`, string(req.UserID)); err != nil {
		return nil, translate(err, "opening the cooldown record")
	}
	return &created, nil
}

// Pending drains the admin queue, oldest first.
func (r *ReevaluationRepository) Pending(ctx context.Context) ([]domain.ReevaluationRequest, error) {
	return r.ByStatus(ctx, "pending")
}

// ByStatus drains the admin queue for one status.
//
// An admin reviewing what they decided last week needs the same list the
// pending queue gives them, which is why this is one query rather than a
// pending-only read plus a separate history.
func (r *ReevaluationRepository) ByStatus(ctx context.Context, status string) ([]domain.ReevaluationRequest, error) {
	rows, err := r.db.pool.Query(ctx, `
		SELECT rr.id, rr.claim_id, rr.user_id, u.display_name, rr.reason, rr.status,
		       rr.reviewed_by, rr.reviewed_at, coalesce(rr.decision_note, ''), rr.created_at
		FROM reevaluation_requests rr
		JOIN users u ON u.id = rr.user_id
		WHERE rr.status = $1::reevaluation_status
		ORDER BY rr.created_at`, status)
	if err != nil {
		return nil, translate(err, fmt.Sprintf("listing %s re-evaluations", status))
	}
	defer rows.Close()

	var out []domain.ReevaluationRequest
	for rows.Next() {
		var req domain.ReevaluationRequest
		if err := rows.Scan(&req.ID, &req.ClaimID, &req.UserID, &req.DisplayName,
			&req.Reason, &req.Status, &req.ReviewedBy, &req.ReviewedAt,
			&req.Decision, &req.CreatedAt); err != nil {
			return nil, translate(err, "scanning re-evaluation request")
		}
		out = append(out, req)
	}
	return out, translate(rows.Err(), fmt.Sprintf("listing %s re-evaluations", status))
}

// OpenRequest returns the dispute a contributor already has in flight.
func (r *ReevaluationRepository) OpenRequest(ctx context.Context, id domain.UserID) (*domain.ReevaluationRequest, error) {
	var req domain.ReevaluationRequest
	err := r.db.pool.QueryRow(ctx, `
		SELECT rr.id, rr.claim_id, rr.user_id, u.display_name, rr.reason, rr.status,
		       rr.reviewed_by, rr.reviewed_at, coalesce(rr.decision_note, ''), rr.created_at
		FROM reevaluation_requests rr
		JOIN users u ON u.id = rr.user_id
		WHERE rr.user_id = $1 AND rr.status = 'pending'
		ORDER BY rr.created_at
		LIMIT 1`, string(id)).
		Scan(&req.ID, &req.ClaimID, &req.UserID, &req.DisplayName,
			&req.Reason, &req.Status, &req.ReviewedBy, &req.ReviewedAt,
			&req.Decision, &req.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, translate(err, fmt.Sprintf("reading the open dispute for %s", id))
	}
	return &req, nil
}

// Decide records the outcome and, ON REJECTION ONLY, advances the cooldown.
//
// Passing the verdict in keeps that asymmetry in one place. Acceptance touches
// neither counter: a contributor who is repeatedly right is never throttled,
// and they are exactly the population whose disputes are worth most.
//
// The cooldown row is read FOR UPDATE, advanced in Go by domain.Cooldown, and
// written back — all inside the caller's transaction, so two rejections
// decided concurrently cannot both read the same count and each advance it by
// one.
func (r *ReevaluationRepository) Decide(ctx context.Context, t port.Tx, id domain.RequestID, by domain.AdminID, accept bool, reason string) error {
	q := r.db.q(t)

	status := "rejected"
	if accept {
		status = "accepted"
	}

	var userID string
	err := q.QueryRow(ctx, `
		UPDATE reevaluation_requests
		SET status = $2::reevaluation_status, reviewed_by = $3, reviewed_at = now(),
		    decision_note = NULLIF($4, ''), updated_at = now()
		WHERE id = $1 AND status = 'pending'
		RETURNING user_id`,
		string(id), status, string(by), reason).Scan(&userID)
	if err != nil {
		if isNotFound(err) {
			return fmt.Errorf("re-evaluation request %s is not pending: %w", id, port.ErrConflict)
		}
		return translate(err, fmt.Sprintf("deciding re-evaluation %s", id))
	}
	if accept {
		return nil
	}

	cooldown, err := r.lockCooldown(ctx, q, domain.UserID(userID))
	if err != nil {
		return err
	}
	cooldown.RecordRejection(r.db.now())

	if _, err := q.Exec(ctx, `
		INSERT INTO reevaluation_cooldowns (user_id, rejection_count, tier, cooldown_until, updated_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (user_id) DO UPDATE
		  SET rejection_count = EXCLUDED.rejection_count,
		      tier            = EXCLUDED.tier,
		      cooldown_until  = EXCLUDED.cooldown_until,
		      updated_at      = now()`,
		userID, cooldown.RejectionCount, cooldown.Tier, cooldown.CooldownUntil); err != nil {
		return translate(err, "recording the cooldown")
	}
	return nil
}

// Cooldown reads a contributor's throttle state.
//
// A contributor who has never disputed has no row, and that is reported as a
// zero-valued Cooldown rather than not-found: "never disputed" and "disputed
// and cleared" are the same answer to the only question a caller asks.
func (r *ReevaluationRepository) Cooldown(ctx context.Context, id domain.UserID) (*domain.Cooldown, error) {
	c := domain.Cooldown{UserID: id}
	err := r.db.pool.QueryRow(ctx, `
		SELECT rejection_count, tier, cooldown_until
		FROM reevaluation_cooldowns WHERE user_id = $1`, string(id),
	).Scan(&c.RejectionCount, &c.Tier, &c.CooldownUntil)
	if err != nil {
		if isNotFound(err) {
			return &c, nil
		}
		return nil, translate(err, fmt.Sprintf("reading cooldown for %s", id))
	}
	return &c, nil
}

// lockCooldown reads the row FOR UPDATE, returning a zero value when absent.
func (r *ReevaluationRepository) lockCooldown(ctx context.Context, q querier, id domain.UserID) (*domain.Cooldown, error) {
	c := domain.Cooldown{UserID: id}
	err := q.QueryRow(ctx, `
		SELECT rejection_count, tier, cooldown_until
		FROM reevaluation_cooldowns WHERE user_id = $1
		FOR UPDATE`, string(id),
	).Scan(&c.RejectionCount, &c.Tier, &c.CooldownUntil)
	if err != nil && !isNotFound(err) {
		return nil, translate(err, "locking cooldown")
	}
	return &c, nil
}
