package postgres_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
	"github.com/Manik2708/gitcherrypick/backend/internal/repository/postgres"
)

func TestSessionRepositoryRotate(t *testing.T) {
	t.Run("the successor shares the family", func(t *testing.T) {
		// Revocation reaches every descendant only because the family id
		// carries across rotations.
		db, ctx := newDB(t), testContext(t)
		user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		first := mustSignIn(ctx, t, db, user.ID, []byte("token-1"))

		second := mustRotate(ctx, t, db, first, []byte("token-2"))

		if second.FamilyID != first.FamilyID {
			t.Errorf("expected the family carried over, got %s and %s", first.FamilyID, second.FamilyID)
		}
		if second.ID == first.ID {
			t.Error("expected a new session row")
		}
		if n := count(t, db, `SELECT count(*) FROM sessions WHERE family_id = $1`, first.FamilyID); n != 2 {
			t.Errorf("expected 2 rows in the family, found %d", n)
		}
	})

	t.Run("a spent token cannot rotate twice", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		first := mustSignIn(ctx, t, db, user.ID, []byte("token-1"))
		mustRotate(ctx, t, db, first, []byte("token-2"))

		err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			return db.Sessions().Rotate(ctx, tx, first.ID,
				&domain.Session{FamilyID: first.FamilyID, PrincipalID: string(user.ID),
					Kind: domain.KindContributor, ExpiresAt: time.Now().Add(720 * time.Hour)},
				[]byte("token-3"))
		})
		if !errors.Is(err, port.ErrConflict) {
			t.Fatalf("expected replaying a spent token to conflict, got %v", err)
		}
	})

	t.Run("two concurrent refreshes cannot both succeed", func(t *testing.T) {
		// The property FOR UPDATE exists for. Without it both transactions
		// read used_at IS NULL, both issue a successor, and the family silently
		// forks — which is the case reuse detection is supposed to catch.
		db, ctx := newDB(t), testContext(t)
		user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		first := mustSignIn(ctx, t, db, user.ID, []byte("token-1"))

		var wg sync.WaitGroup
		results := make([]error, 2)
		start := make(chan struct{})

		for i := range results {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				results[i] = db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
					// Lock first, exactly as the auth service would.
					s, err := db.Sessions().ByRefreshTokenHash(ctx, tx, []byte("token-1"))
					if err != nil {
						return err
					}
					if s.UsedAt != nil {
						return port.ErrConflict
					}
					return db.Sessions().Rotate(ctx, tx, s.ID,
						&domain.Session{FamilyID: s.FamilyID, PrincipalID: string(user.ID),
							Kind: domain.KindContributor, ExpiresAt: time.Now().Add(720 * time.Hour)},
						[]byte{byte('a' + i)})
				})
			}(i)
		}
		close(start)
		wg.Wait()

		var succeeded int
		for _, err := range results {
			if err == nil {
				succeeded++
			} else if !errors.Is(err, port.ErrConflict) {
				t.Errorf("expected a conflict for the loser, got %v", err)
			}
		}
		if succeeded != 1 {
			t.Fatalf("expected exactly 1 of 2 concurrent refreshes to succeed, got %d", succeeded)
		}
		if n := count(t, db, `SELECT count(*) FROM sessions WHERE family_id = $1`, first.FamilyID); n != 2 {
			t.Errorf("expected the family to hold 2 rows, found %d — it forked", n)
		}
	})
}

func TestSessionRepositoryRevokeFamily(t *testing.T) {
	t.Run("revoking kills the successor currently in use", func(t *testing.T) {
		// Reuse means the family is compromised. The honest holder is signed
		// out too, and that is the intended cost (ADR-0002).
		db, ctx := newDB(t), testContext(t)
		user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")

		first := mustSignIn(ctx, t, db, user.ID, []byte("token-1"))
		second := mustRotate(ctx, t, db, first, []byte("token-2"))
		third := mustRotate(ctx, t, db, second, []byte("token-3"))

		if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			return db.Sessions().RevokeFamily(ctx, tx, first.FamilyID)
		}); err != nil {
			t.Fatalf("revoking: %v", err)
		}

		if n := count(t, db,
			`SELECT count(*) FROM sessions WHERE family_id = $1 AND revoked_at IS NULL`,
			first.FamilyID); n != 0 {
			t.Errorf("expected every session revoked, %d survived", n)
		}
		if n, err := db.Sessions().ActiveCount(ctx, string(user.ID)); err != nil || n != 0 {
			t.Errorf("expected 0 active sessions, got %d (err %v)", n, err)
		}
		_ = third
	})

	t.Run("another family is untouched", func(t *testing.T) {
		// Signing out one device must not sign out the others.
		db, ctx := newDB(t), testContext(t)
		user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")

		laptop := mustSignIn(ctx, t, db, user.ID, []byte("laptop-1"))
		phone := mustSignIn(ctx, t, db, user.ID, []byte("phone-1"))

		if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			return db.Sessions().RevokeFamily(ctx, tx, laptop.FamilyID)
		}); err != nil {
			t.Fatalf("revoking: %v", err)
		}

		if n := count(t, db,
			`SELECT count(*) FROM sessions WHERE family_id = $1 AND revoked_at IS NULL`,
			phone.FamilyID); n != 1 {
			t.Error("revoking one family killed another")
		}
	})

	t.Run("revoked_at records when the family died, not when asked again", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		s := mustSignIn(ctx, t, db, user.ID, []byte("token-1"))

		revoke := func() {
			t.Helper()
			if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
				return db.Sessions().RevokeFamily(ctx, tx, s.FamilyID)
			}); err != nil {
				t.Fatalf("revoking: %v", err)
			}
		}
		revoke()
		var first time.Time
		scanRow(ctx, t, db, []any{&first},
			`SELECT revoked_at FROM sessions WHERE family_id = $1`, s.FamilyID)

		time.Sleep(5 * time.Millisecond)
		revoke()

		var second time.Time
		scanRow(ctx, t, db, []any{&second},
			`SELECT revoked_at FROM sessions WHERE family_id = $1`, s.FamilyID)
		if !first.Equal(second) {
			t.Errorf("revoked_at moved on a second revoke: %s then %s", first, second)
		}
	})
}

func TestSessionRepositoryPrincipals(t *testing.T) {
	t.Run("each principal kind lands in its own column", func(t *testing.T) {
		// ck_session_single_principal permits exactly one, which is what keeps
		// a hirer session from resolving as a contributor.
		db, ctx := newDB(t), testContext(t)
		user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		hirer := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")
		admin := mustCreateAdmin(ctx, t, db)

		mustSignIn(ctx, t, db, user.ID, []byte("contributor"))
		mustSignInAs(ctx, t, db, domain.KindHirer, string(hirer.ID), []byte("hirer"))
		mustSignInAs(ctx, t, db, domain.KindAdmin, string(admin), []byte("admin"))

		for _, c := range []struct {
			column string
			id     string
		}{
			{"user_id", string(user.ID)},
			{"hirer_account_id", string(hirer.ID)},
			{"admin_account_id", string(admin)},
		} {
			if n := count(t, db,
				`SELECT count(*) FROM sessions WHERE `+c.column+` = $1`, c.id); n != 1 {
				t.Errorf("expected 1 session in %s, found %d", c.column, n)
			}
		}
	})

	t.Run("ActiveCount ignores spent and expired sessions", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")

		live := mustSignIn(ctx, t, db, user.ID, []byte("live"))
		spent := mustSignIn(ctx, t, db, user.ID, []byte("spent"))
		mustRotate(ctx, t, db, spent, []byte("spent-successor"))

		expired := mustSignIn(ctx, t, db, user.ID, []byte("expired"))
		if _, err := db.Pool().Exec(ctx,
			`UPDATE sessions SET expires_at = now() - interval '1 hour' WHERE id = $1`,
			string(expired.ID)); err != nil {
			t.Fatalf("expiring: %v", err)
		}

		// live, plus the successor of the spent one.
		n, err := db.Sessions().ActiveCount(ctx, string(user.ID))
		if err != nil {
			t.Fatalf("counting: %v", err)
		}
		if n != 2 {
			t.Errorf("expected 2 usable sessions, got %d", n)
		}
		_ = live
	})

	t.Run("an unknown token is not found", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			_, err := db.Sessions().ByRefreshTokenHash(ctx, tx, []byte("never-issued"))
			return err
		})
		if !errors.Is(err, port.ErrNotFound) {
			t.Fatalf("expected port.ErrNotFound, got %v", err)
		}
	})

	t.Run("a token hash is unique across families", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		mustSignIn(ctx, t, db, user.ID, []byte("token-1"))

		err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			return db.Sessions().Create(ctx, tx,
				&domain.Session{PrincipalID: string(user.ID), Kind: domain.KindContributor,
					ExpiresAt: time.Now().Add(720 * time.Hour)},
				[]byte("token-1"))
		})
		if !errors.Is(err, port.ErrConflict) {
			t.Fatalf("expected a duplicate hash to conflict, got %v", err)
		}
	})
}

// --- helpers -----------------------------------------------------------------

func mustSignIn(ctx context.Context, t *testing.T, db *postgres.DB, user domain.UserID, hash []byte) *domain.Session {
	t.Helper()
	return mustSignInAs(ctx, t, db, domain.KindContributor, string(user), hash)
}

func mustSignInAs(ctx context.Context, t *testing.T, db *postgres.DB, kind domain.PrincipalKind, id string, hash []byte) *domain.Session {
	t.Helper()
	s := &domain.Session{PrincipalID: id, Kind: kind, ExpiresAt: time.Now().Add(720 * time.Hour)}
	if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		return db.Sessions().Create(ctx, tx, s, hash)
	}); err != nil {
		t.Fatalf("signing in: %v", err)
	}
	return s
}

func mustRotate(ctx context.Context, t *testing.T, db *postgres.DB, spent *domain.Session, hash []byte) *domain.Session {
	t.Helper()
	successor := &domain.Session{
		FamilyID:    spent.FamilyID,
		PrincipalID: spent.PrincipalID,
		Kind:        spent.Kind,
		ExpiresAt:   time.Now().Add(720 * time.Hour),
	}
	if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		return db.Sessions().Rotate(ctx, tx, spent.ID, successor, hash)
	}); err != nil {
		t.Fatalf("rotating: %v", err)
	}
	return successor
}
