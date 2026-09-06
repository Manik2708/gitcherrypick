package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// SessionRepository owns refresh-token families.
//
// Refresh tokens rotate: presenting one spends it and issues a successor with
// the same family_id. Presenting a SPENT token means someone is replaying a
// token the legitimate holder already used, and the only safe reading is that
// the family is compromised — so the whole family is revoked, including the
// successor currently in honest use (ADR-0002).
//
// Only hashes are stored. A database read cannot yield a usable token, and
// neither can a backup.
type SessionRepository struct{ db *DB }

// Sessions returns the session repository.
func (db *DB) Sessions() *SessionRepository { return &SessionRepository{db: db} }

var _ port.SessionRepository = (*SessionRepository)(nil)

const sessionColumns = `
	id, family_id,
	coalesce(user_id, hirer_account_id, admin_account_id),
	CASE WHEN user_id IS NOT NULL THEN 'contributor'
	     WHEN hirer_account_id IS NOT NULL THEN 'hirer'
	     ELSE 'admin' END,
	created_at, expires_at, used_at, revoked_at`

// ByRefreshTokenHash reads a session and locks the row.
//
// FOR UPDATE is not what makes rotation safe — Rotate's own
// `WHERE used_at IS NULL` predicate is, because it makes the check and the
// write one statement. Verified: removing FOR UPDATE leaves the
// two-concurrent-refreshes test passing.
//
// It is here to serialise the two transactions EARLY. The loser blocks on the
// read instead of doing the work of minting a successor token and hashing it,
// only to have the UPDATE match no row. Same outcome, less wasted work, and
// the caller sees the conflict at the point it read rather than at the point
// it wrote.
func (r *SessionRepository) ByRefreshTokenHash(ctx context.Context, t port.Tx, hash []byte) (*domain.Session, error) {
	var s domain.Session
	err := r.db.q(t).QueryRow(ctx,
		`SELECT`+sessionColumns+`
		 FROM sessions WHERE refresh_token_hash = $1
		 FOR UPDATE`, hash,
	).Scan(&s.ID, &s.FamilyID, &s.PrincipalID, &s.Kind,
		&s.IssuedAt, &s.ExpiresAt, &s.UsedAt, &s.RevokedAt)
	if err != nil {
		// Not distinguishing "no such token" from anything else: a
		// distinguishable response is an oracle for guessing valid tokens.
		return nil, translate(err, "resolving refresh token")
	}
	return &s, nil
}

// Create writes a new session, starting a family when none is given.
func (r *SessionRepository) Create(ctx context.Context, t port.Tx, s *domain.Session, refreshTokenHash []byte) error {
	id := s.ID
	if id == "" {
		generated, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("generating session id: %w", err)
		}
		id = domain.SessionID(generated.String())
	}
	familyID := s.FamilyID
	if familyID == "" {
		// A sign-in starts a family. A rotation passes the existing one, which
		// is what makes revocation reach every descendant.
		generated, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("generating family id: %w", err)
		}
		familyID = generated.String()
	}

	userID, hirerID, adminID := principalColumns(s.Kind, s.PrincipalID)
	if _, err := r.db.q(t).Exec(ctx, `
		INSERT INTO sessions
		    (id, user_id, hirer_account_id, admin_account_id, family_id, refresh_token_hash, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		string(id), userID, hirerID, adminID, familyID, refreshTokenHash, s.ExpiresAt); err != nil {
		return translate(err, "creating session")
	}

	s.ID = id
	s.FamilyID = familyID
	return nil
}

// Rotate spends the presented session and inserts its successor.
//
// One transaction, both writes. A successor without the predecessor being
// marked used would let the old token be replayed forever; marking used
// without a successor would sign the caller out on a legitimate refresh.
//
// The UPDATE carries the used_at IS NULL predicate rather than trusting the
// caller's earlier read. This is the actual guarantee: the check and the write
// are one statement, so nothing can change between them even if the caller
// never locked. Two concurrent refreshes therefore cannot both succeed, and
// the loser is told the token is already spent — which is reuse.
func (r *SessionRepository) Rotate(ctx context.Context, t port.Tx, spent domain.SessionID, successor *domain.Session, hash []byte) error {
	q := r.db.q(t)

	tag, err := q.Exec(ctx, `
		UPDATE sessions SET used_at = now()
		WHERE id = $1 AND used_at IS NULL AND revoked_at IS NULL`, string(spent))
	if err != nil {
		return translate(err, fmt.Sprintf("spending session %s", spent))
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("session %s is already spent or revoked: %w", spent, port.ErrConflict)
	}

	return r.Create(ctx, t, successor, hash)
}

// RevokeFamily kills every session sharing a family id.
//
// Called on reuse detection AND on logout. A deliberate sign-out has to
// invalidate a refresh token stolen beforehand, which revoking only the
// presented session would not do.
//
// Already-revoked rows are left alone so revoked_at records when the family
// actually died rather than when someone last asked.
func (r *SessionRepository) RevokeFamily(ctx context.Context, t port.Tx, familyID string) error {
	_, err := r.db.q(t).Exec(ctx,
		`UPDATE sessions SET revoked_at = now() WHERE family_id = $1 AND revoked_at IS NULL`,
		familyID)
	return translate(err, fmt.Sprintf("revoking family %s", familyID))
}

// CloseFamily retires every session in a family the holder signed out of.
//
// Spent as well as revoked. A signed-out token is not collateral damage from
// someone else's replay — it was retired by the person holding it, and
// stamping used_at is what records the difference.
func (r *SessionRepository) CloseFamily(ctx context.Context, t port.Tx, familyID string) error {
	_, err := r.db.q(t).Exec(ctx, `
		UPDATE sessions
		SET revoked_at = coalesce(revoked_at, $2), used_at = coalesce(used_at, $2)
		WHERE family_id = $1`, familyID, r.db.now())
	return translate(err, fmt.Sprintf("closing family %s", familyID))
}

// FamilyLive reports whether a family still has an unrevoked session.
//
// Read on every authenticated request, because an access token that outlived
// its own sign-out is not a session (ADR-0002).
func (r *SessionRepository) FamilyLive(ctx context.Context, familyID string) (bool, error) {
	var live bool
	if err := r.db.pool.QueryRow(ctx, `
		SELECT EXISTS (
		    SELECT 1 FROM sessions
		    WHERE family_id = $1 AND revoked_at IS NULL AND expires_at > $2
		)`, familyID, r.db.now()).Scan(&live); err != nil {
		return false, translate(err, fmt.Sprintf("checking family %s", familyID))
	}
	return live, nil
}

// ActiveCount reports how many sessions a principal can still use.
//
// Unrevoked, unexpired, and unspent: a rotated predecessor is none of the
// caller's business, since it can no longer be presented.
func (r *SessionRepository) ActiveCount(ctx context.Context, principalID string) (int, error) {
	var n int
	err := r.db.pool.QueryRow(ctx, `
		SELECT count(*) FROM sessions
		WHERE coalesce(user_id, hirer_account_id, admin_account_id) = $1
		  AND revoked_at IS NULL AND used_at IS NULL AND expires_at > now()`,
		principalID).Scan(&n)
	if err != nil {
		return 0, translate(err, fmt.Sprintf("counting sessions for %s", principalID))
	}
	return n, nil
}

// principalColumns maps a principal onto the three mutually exclusive columns
// ck_session_single_principal permits exactly one of.
func principalColumns(kind domain.PrincipalKind, id string) (user, hirer, admin *string) {
	switch kind {
	case domain.KindContributor:
		return &id, nil, nil
	case domain.KindHirer:
		return nil, &id, nil
	case domain.KindAdmin:
		return nil, nil, &id
	}
	return nil, nil, nil
}

// ActiveFamily returns the family a principal's live session belongs to.
//
// The most recent unrevoked, unexpired session. A principal signed in on
// several devices has several families; logout revokes the one the request
// arrived on, which is the newest this query can see without the token.
func (r *SessionRepository) ActiveFamily(ctx context.Context, principalID string) (string, error) {
	var familyID string
	err := r.db.pool.QueryRow(ctx, `
		SELECT family_id FROM sessions
		WHERE coalesce(user_id, hirer_account_id, admin_account_id) = $1
		  AND revoked_at IS NULL AND expires_at > now()
		ORDER BY created_at DESC
		LIMIT 1`, principalID).Scan(&familyID)
	if err != nil {
		return "", translate(err, fmt.Sprintf("reading the active family for %s", principalID))
	}
	return familyID, nil
}
