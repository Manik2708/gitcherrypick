package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
	"github.com/Manik2708/gitcherrypick/backend/internal/repository/postgres"
)

func TestHirerRepositoryRegister(t *testing.T) {
	t.Run("creates the org, seat, membership and verification request together", func(t *testing.T) {
		// ADR-0002: an account with no pending request would never be verified
		// by anything, and a request with no account is a queue item an admin
		// cannot act on.
		db, ctx := newDB(t), testContext(t)

		h := mustRegister(ctx, t, db, "pat@unknown.example", "Pat Ferreira", "Unknown Ltd", "unknown-ltd")

		if h.OrgRole != domain.RoleOwner {
			t.Errorf("expected the registrant to own the org, got %q", h.OrgRole)
		}
		if n := count(t, db, `SELECT count(*) FROM organization_members WHERE hirer_account_id = $1`, string(h.ID)); n != 1 {
			t.Errorf("expected 1 membership, found %d", n)
		}
		if n := count(t, db,
			`SELECT count(*) FROM verification_requests WHERE organization_id = $1 AND status = 'pending'`,
			string(h.OrganizationID)); n != 1 {
			t.Errorf("expected 1 pending verification request, found %d", n)
		}
	})

	t.Run("the request names the ORGANIZATION, not the seat", func(t *testing.T) {
		// Per-organization is what makes approval lift every seat at once, and
		// ck_verification_single_subject forbids naming both.
		db, ctx := newDB(t), testContext(t)
		h := mustRegister(ctx, t, db, "pat@unknown.example", "Pat", "Unknown Ltd", "unknown-ltd")

		var hirerID *string
		var orgID *string
		scanRow(ctx, t, db, []any{&hirerID, &orgID},
			`SELECT hirer_account_id, organization_id FROM verification_requests WHERE organization_id = $1`,
			string(h.OrganizationID))
		if hirerID != nil {
			t.Error("the request names a seat; approval would then not lift the org")
		}
		if orgID == nil {
			t.Error("the request names no organization")
		}
	})

	t.Run("a duplicate email conflicts and rolls back the org", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		mustRegister(ctx, t, db, "pat@unknown.example", "Pat", "First Ltd", "first-ltd")

		err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			_, err := db.Hirers().Register(ctx, tx,
				&domain.Hirer{Email: "pat@unknown.example", DisplayName: "Impostor", AuthProvider: domain.ProviderEmail},
				&domain.Organization{Name: "Second Ltd", Slug: "second-ltd"},
				[]byte("hash"))
			return err
		})
		if !errors.Is(err, port.ErrConflict) {
			t.Fatalf("expected port.ErrConflict, got %v", err)
		}
		if n := count(t, db, `SELECT count(*) FROM organizations WHERE slug = 'second-ltd'`); n != 0 {
			t.Errorf("the rolled-back organization survived: %d row(s)", n)
		}
	})

	t.Run("a google seat is stored without a password", func(t *testing.T) {
		// ck_hirer_password_only_for_email. An OAuth account with a hash would
		// mean two ways in, one of which nobody set.
		db, ctx := newDB(t), testContext(t)

		var h *domain.Hirer
		err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			var err error
			h, err = db.Hirers().Register(ctx, tx,
				&domain.Hirer{Email: "hank@acme.com", DisplayName: "Hank", AuthProvider: domain.ProviderGoogle},
				&domain.Organization{Name: "Acme Corp", Slug: "acme"},
				[]byte("this must be ignored"))
			return err
		})
		if err != nil {
			t.Fatalf("registering a google seat: %v", err)
		}
		if n := count(t, db,
			`SELECT count(*) FROM hirer_accounts WHERE id = $1 AND password_hash IS NULL`, string(h.ID)); n != 1 {
			t.Error("a google seat must have no password hash")
		}
	})
}

func TestHirerRepositoryReads(t *testing.T) {
	t.Run("email lookup is case-insensitive", func(t *testing.T) {
		// email is citext, so this is the database's rule rather than a second
		// one kept in sync with the unique constraint.
		db, ctx := newDB(t), testContext(t)
		mustRegister(ctx, t, db, "Hank@Acme.com", "Hank", "Acme", "acme")

		got, err := db.Hirers().ByEmail(ctx, "hank@acme.COM")
		if err != nil {
			t.Fatalf("looking up: %v", err)
		}
		if got.DisplayName != "Hank" {
			t.Errorf("expected Hank, got %q", got.DisplayName)
		}
	})

	t.Run("an unknown email is not found", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		_, err := db.Hirers().ByEmail(ctx, "nobody@nowhere.example")
		if !errors.Is(err, port.ErrNotFound) {
			t.Fatalf("expected port.ErrNotFound, got %v", err)
		}
	})

	t.Run("PasswordHash returns nothing for an oauth seat", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		var h *domain.Hirer
		_ = db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			var err error
			h, err = db.Hirers().Register(ctx, tx,
				&domain.Hirer{Email: "hank@acme.com", DisplayName: "Hank", AuthProvider: domain.ProviderGoogle},
				&domain.Organization{Name: "Acme", Slug: "acme"}, nil)
			return err
		})
		if _, err := db.Hirers().PasswordHash(ctx, h.ID); !errors.Is(err, port.ErrNotFound) {
			t.Fatalf("expected no password path for an oauth seat, got %v", err)
		}
	})
}

func TestHirerRepositorySharesGitHubIdentity(t *testing.T) {
	t.Run("detects the shared identity AssertNotSelf exists for", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		dave := mustCreateContributor(ctx, t, db, "Dave Whitfield", 100004, "dwhit")
		other := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		hirer := mustRegister(ctx, t, db, "dave.hiring@example.com", "Dave Whitfield", "Acme", "acme")
		giveHirerGitHubIdentity(ctx, t, db, hirer.ID, 100004, "dwhit")

		shares, err := db.Hirers().SharesGitHubIdentity(ctx, hirer.ID, dave.ID)
		if err != nil {
			t.Fatalf("checking: %v", err)
		}
		if !shares {
			t.Error("expected the shared identity to be detected")
		}

		shares, err = db.Hirers().SharesGitHubIdentity(ctx, hirer.ID, other.ID)
		if err != nil {
			t.Fatalf("checking: %v", err)
		}
		if shares {
			t.Error("the exclusion must be identity-scoped, not a general hide")
		}
	})

	t.Run("a hirer with no github identity shares nothing", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		alice := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		hirer := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")

		shares, err := db.Hirers().SharesGitHubIdentity(ctx, hirer.ID, alice.ID)
		if err != nil {
			t.Fatalf("checking: %v", err)
		}
		if shares {
			t.Error("expected no shared identity")
		}
	})
}

func TestOrganizationRepositoryInvitations(t *testing.T) {
	t.Run("an invited seat inherits the organization's verification", func(t *testing.T) {
		// The whole point of verifying an org rather than a person
		// (ADR-0008 §3a).
		db, ctx := newDB(t), testContext(t)
		owner := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")
		admin := mustCreateAdmin(ctx, t, db)
		mustVerify(ctx, t, db, owner.OrganizationID, admin)

		hash := []byte("invitation-hash")
		id := mustInvite(ctx, t, db, owner, "rita@acme.com", hash)

		var invited *domain.Hirer
		err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			var err error
			invited, err = db.Organizations().AcceptInvitation(ctx, tx, id,
				&domain.Hirer{DisplayName: "Rita Sandoval", AuthProvider: domain.ProviderEmail}, []byte("hash"))
			return err
		})
		if err != nil {
			t.Fatalf("accepting: %v", err)
		}

		org, err := db.Hirers().Organization(ctx, invited.OrganizationID)
		if err != nil {
			t.Fatalf("reading the org: %v", err)
		}
		if !org.IsVerified() {
			t.Error("the invited seat's organization should be verified")
		}
		if n := count(t, db,
			`SELECT count(*) FROM verification_requests WHERE hirer_account_id = $1`, string(invited.ID)); n != 0 {
			t.Error("an invited seat must not create its own verification request")
		}
	})

	t.Run("a token is single-use", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		owner := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")
		hash := []byte("invitation-hash")
		id := mustInvite(ctx, t, db, owner, "rita@acme.com", hash)

		accept := func(name string) error {
			return db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
				_, err := db.Organizations().AcceptInvitation(ctx, tx, id,
					&domain.Hirer{DisplayName: name, AuthProvider: domain.ProviderEmail}, []byte("hash"))
				return err
			})
		}
		if err := accept("Rita Sandoval"); err != nil {
			t.Fatalf("first acceptance: %v", err)
		}
		if err := accept("Someone Else"); !errors.Is(err, port.ErrNotFound) {
			t.Fatalf("expected a replayed token to be refused, got %v", err)
		}
		if n := count(t, db, `SELECT count(*) FROM hirer_accounts WHERE display_name = 'Someone Else'`); n != 0 {
			t.Error("a replayed token created a seat")
		}
	})

	t.Run("an expired invitation cannot be accepted", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		owner := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")

		hash := []byte("stale-hash")
		err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			_, err := db.Organizations().CreateInvitation(ctx, tx, owner.OrganizationID,
				"slow@acme.com", domain.RoleMember, owner.ID, hash, time.Now().Add(-time.Hour))
			return err
		})
		if err != nil {
			t.Fatalf("inviting: %v", err)
		}

		var id domain.RequestID
		scanRow(ctx, t, db, []any{&id},
			`SELECT id FROM organization_invitations WHERE email = 'slow@acme.com'`)

		err = db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			_, err := db.Organizations().AcceptInvitation(ctx, tx, id,
				&domain.Hirer{DisplayName: "Slow Coach", AuthProvider: domain.ProviderEmail}, []byte("hash"))
			return err
		})
		if !errors.Is(err, port.ErrNotFound) {
			t.Fatalf("expected an expired invitation to be refused, got %v", err)
		}
	})
}

func TestOrganizationRepositoryVerify(t *testing.T) {
	t.Run("verifying lifts every seat, including one invited beforehand", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		owner := mustRegister(ctx, t, db, "pat@unknown.example", "Pat", "Unknown Ltd", "unknown-ltd")
		admin := mustCreateAdmin(ctx, t, db)

		hash := []byte("invitation-hash")
		id := mustInvite(ctx, t, db, owner, "colleague@unknown.example", hash)
		err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			_, err := db.Organizations().AcceptInvitation(ctx, tx, id,
				&domain.Hirer{DisplayName: "Colleague Chen", AuthProvider: domain.ProviderEmail}, []byte("hash"))
			return err
		})
		if err != nil {
			t.Fatalf("accepting: %v", err)
		}

		org, _ := db.Hirers().Organization(ctx, owner.OrganizationID)
		if org.IsVerified() {
			t.Fatal("the org should start unverified")
		}

		mustVerify(ctx, t, db, owner.OrganizationID, admin)

		org, err = db.Hirers().Organization(ctx, owner.OrganizationID)
		if err != nil {
			t.Fatalf("reading: %v", err)
		}
		if !org.IsVerified() {
			t.Error("expected the organization verified")
		}
		members, err := db.Hirers().Members(ctx, owner.OrganizationID)
		if err != nil {
			t.Fatalf("listing members: %v", err)
		}
		if len(members) != 2 {
			t.Errorf("expected both seats, got %d", len(members))
		}
		if n := count(t, db,
			`SELECT count(*) FROM verification_requests WHERE organization_id = $1 AND status = 'approved'`,
			string(owner.OrganizationID)); n != 1 {
			t.Error("expected the verification request closed")
		}
	})

	t.Run("verifying twice conflicts", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		owner := mustRegister(ctx, t, db, "pat@unknown.example", "Pat", "Unknown Ltd", "unknown-ltd")
		admin := mustCreateAdmin(ctx, t, db)
		mustVerify(ctx, t, db, owner.OrganizationID, admin)

		err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			return db.Organizations().VerifyOrganization(ctx, tx, owner.OrganizationID, admin, "")
		})
		if !errors.Is(err, port.ErrConflict) {
			t.Fatalf("expected port.ErrConflict, got %v", err)
		}
	})

	t.Run("payment verification is separate and does not gate", func(t *testing.T) {
		// ADR-0002 §5: missing payment evidence is disclosed to the
		// contributor at contact time rather than blocking the org.
		db, ctx := newDB(t), testContext(t)
		owner := mustRegister(ctx, t, db, "sam@tinystudio.example", "Sam", "Tiny Studio", "tiny-studio")
		admin := mustCreateAdmin(ctx, t, db)
		mustVerify(ctx, t, db, owner.OrganizationID, admin)

		org, err := db.Hirers().Organization(ctx, owner.OrganizationID)
		if err != nil {
			t.Fatalf("reading: %v", err)
		}
		if !org.IsVerified() {
			t.Error("expected identity verification")
		}
		if org.PaymentVerified() {
			t.Error("payment verification must not follow from identity verification")
		}
	})
}

// --- helpers -----------------------------------------------------------------

func mustRegister(ctx context.Context, t *testing.T, db *postgres.DB, email, name, orgName, orgSlug string) *domain.Hirer {
	t.Helper()
	var h *domain.Hirer
	err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		var err error
		h, err = db.Hirers().Register(ctx, tx,
			&domain.Hirer{Email: email, DisplayName: name, AuthProvider: domain.ProviderEmail},
			&domain.Organization{Name: orgName, Slug: orgSlug},
			[]byte("argon2id-hash"))
		return err
	})
	if err != nil {
		t.Fatalf("registering %s: %v", email, err)
	}
	return h
}

func mustInvite(ctx context.Context, t *testing.T, db *postgres.DB, owner *domain.Hirer, email string, hash []byte) domain.RequestID {
	t.Helper()
	var id domain.RequestID
	err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		var err error
		id, err = db.Organizations().CreateInvitation(ctx, tx, owner.OrganizationID,
			email, domain.RoleMember, owner.ID, hash, time.Now().Add(postgres.InvitationWindow))
		return err
	})
	if err != nil {
		t.Fatalf("inviting %s: %v", email, err)
	}
	return id
}

func mustVerify(ctx context.Context, t *testing.T, db *postgres.DB, org domain.OrganizationID, admin domain.AdminID) {
	t.Helper()
	if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		return db.Organizations().VerifyOrganization(ctx, tx, org, admin, "Companies House record matches.")
	}); err != nil {
		t.Fatalf("verifying: %v", err)
	}
}

func giveHirerGitHubIdentity(ctx context.Context, t *testing.T, db *postgres.DB, id domain.HirerID, githubID int64, login string) {
	t.Helper()
	if _, err := db.Pool().Exec(ctx,
		`INSERT INTO user_github_identities (id, hirer_account_id, github_user_id, github_login)
		 VALUES ($1, $2, $3, $4)`, uuid.NewString(), string(id), githubID, login); err != nil {
		t.Fatalf("giving the hirer a github identity: %v", err)
	}
}

func scanRow(ctx context.Context, t *testing.T, db *postgres.DB, dest []any, query string, args ...any) {
	t.Helper()
	if err := db.Pool().QueryRow(ctx, query, args...).Scan(dest...); err != nil {
		t.Fatalf("querying: %v", err)
	}
}
