package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// ShareLinkRepository owns a contributor's publishable scorecard link.
//
// The only thing a contributor may publish about themselves (ADR-0002). Only
// the hash is stored, so a database read cannot yield a working link.
type ShareLinkRepository struct{ db *DB }

// ShareLinks returns the share-link repository.
func (db *DB) ShareLinks() *ShareLinkRepository { return &ShareLinkRepository{db: db} }

var _ port.ShareLinkRepository = (*ShareLinkRepository)(nil)

// Mint revokes any active link and issues a new one.
//
// Both in the caller's transaction: uq_share_link_active permits one live link
// per contributor, so the revoke and the insert have to be atomic or the
// insert races its own predecessor. A contributor who shared a link and then
// minted another must not find the old one still live.
func (r *ShareLinkRepository) Mint(ctx context.Context, t port.Tx, id domain.UserID, tokenHash []byte) (domain.ShareLinkID, error) {
	q := r.db.q(t)

	if _, err := q.Exec(ctx,
		`UPDATE profile_share_links SET revoked_at = now()
		 WHERE user_id = $1 AND revoked_at IS NULL`, string(id)); err != nil {
		return "", translate(err, "revoking the previous share link")
	}

	linkID, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generating share link id: %w", err)
	}
	if _, err := q.Exec(ctx,
		`INSERT INTO profile_share_links (id, user_id, token_hash) VALUES ($1, $2, $3)`,
		linkID.String(), string(id), tokenHash); err != nil {
		return "", translate(err, "minting the share link")
	}
	return domain.ShareLinkID(linkID.String()), nil
}

// Revoke withdraws a link. Revocation is immediate.
func (r *ShareLinkRepository) Revoke(ctx context.Context, id domain.UserID, linkID domain.ShareLinkID) error {
	tag, err := r.db.pool.Exec(ctx,
		`UPDATE profile_share_links SET revoked_at = now()
		 WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL`,
		string(linkID), string(id))
	if err != nil {
		return translate(err, "revoking the share link")
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("share link %s: %w", linkID, port.ErrNotFound)
	}
	return nil
}

// ResolveToken returns the contributor a live token belongs to, and counts the
// view.
//
// A revoked link is ErrNotFound, indistinguishable from a token that never
// existed — a distinguishable response would confirm that a withdrawn link
// once pointed at somebody.
func (r *ShareLinkRepository) ResolveToken(ctx context.Context, tokenHash []byte) (domain.UserID, error) {
	var id domain.UserID
	err := r.db.pool.QueryRow(ctx, `
		UPDATE profile_share_links
		SET view_count = view_count + 1, last_viewed_at = now()
		WHERE token_hash = $1 AND revoked_at IS NULL
		RETURNING user_id`, tokenHash).Scan(&id)
	if err != nil {
		return "", translate(err, "resolving the share link")
	}
	return id, nil
}
