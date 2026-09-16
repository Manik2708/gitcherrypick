package postgres_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
	"github.com/Manik2708/gitcherrypick/backend/internal/repository/postgres"
)

func TestHirerRepositoryRegister(t *testing.T) {
	t.Run("creates an INDEPENDENT hirer and a request naming them", func(t *testing.T) {
		// Since ADR-0017 §1 registration creates no organisation at all. It is
		// the independent hirer's route — someone hiring on their own account —
		// and the verification request names the person, because there is no
		// company for it to name.
		db, ctx := newDB(t), testContext(t)

		h := mustRegisterIndependent(ctx, t, db, "pat@unknown.example", "pat", "Pat Ferreira")

		if h.OrganizationID != "" {
			t.Errorf("registration created an organisation: %q", h.OrganizationID)
		}
		if n := count(t, db, `SELECT count(*) FROM organizations`); n != 0 {
			t.Errorf("expected no organisations, found %d", n)
		}
		if n := count(t, db,
			`SELECT count(*) FROM organization_members WHERE hirer_account_id = $1`,
			string(h.ID)); n != 0 {
			t.Error("an independent hirer is a member of nothing")
		}
		if n := count(t, db,
			`SELECT count(*) FROM verification_requests
			  WHERE hirer_account_id = $1 AND status = 'pending'`, string(h.ID)); n != 1 {
			t.Errorf("expected 1 pending request naming the hirer, found %d", n)
		}
	})

	t.Run("the request names the SEAT, not an organization", func(t *testing.T) {
		// The inverse of what this asserted before ADR-0017.
		// ck_verification_single_subject wants exactly one subject, and
		// onboarding a company needs no verification_requests row at all — the
		// submission is the record, and approving it is the verification.
		db, ctx := newDB(t), testContext(t)
		h := mustRegisterIndependent(ctx, t, db, "pat@unknown.example", "pat", "Pat")

		var hirerID, orgID *string
		scanRow(ctx, t, db, []any{&hirerID, &orgID},
			`SELECT hirer_account_id, organization_id FROM verification_requests`)
		if hirerID == nil || *hirerID != string(h.ID) {
			t.Error("the request should name the hirer")
		}
		if orgID != nil {
			t.Error("there is no organisation to name")
		}
	})

	t.Run("a duplicate username conflicts and rolls back the org", func(t *testing.T) {
		// The USERNAME is the unique key since ADR-0016. The org is created
		// before the seat, so a clash has to unwind a row that already went in.
		db, ctx := newDB(t), testContext(t)
		mustRegister(ctx, t, db, "pat@unknown.example", "Pat", "First Ltd", "first-ltd")

		err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			_, err := db.Hirers().Register(ctx, tx, port.NewHirerAccount{
				Hirer: &domain.Hirer{
					Email: "impostor@elsewhere.example", Username: "pat",
					DisplayName: "Impostor", AuthProvider: domain.ProviderEmail,
				},
				Organization: &domain.Organization{Name: "Second Ltd", Slug: "second-ltd"},
				PasswordHash: []byte("hash"),
			})
			return err
		})
		if !errors.Is(err, port.ErrConflict) {
			t.Fatalf("expected port.ErrConflict, got %v", err)
		}
		if n := count(t, db, `SELECT count(*) FROM organizations WHERE slug = 'second-ltd'`); n != 0 {
			t.Errorf("the rolled-back organization survived: %d row(s)", n)
		}
	})

	t.Run("two seats may share a contact address", func(t *testing.T) {
		// hirer_accounts carries no unique constraint on email at all
		// (ADR-0016): two people in one company may both be reachable at
		// hiring@acme.com, and the address identifies neither of them.
		db, ctx := newDB(t), testContext(t)
		owner := mustRegister(ctx, t, db, "hiring@acme.com", "Hank", "Acme", "acme")

		id := mustRoster(ctx, t, db, owner, "hiring@acme.com", "maya")
		second := mustRedeem(ctx, t, db, id, "Maya Okafor")

		if second.Email != owner.Email {
			t.Fatalf("email = %q, want the shared address", second.Email)
		}
		if second.Username == owner.Username {
			t.Error("the two seats must still be told apart by username")
		}
	})

	t.Run("a google seat is stored without a password", func(t *testing.T) {
		// ck_hirer_password_only_for_email. An OAuth account with a hash would
		// mean two ways in, one of which nobody set.
		db, ctx := newDB(t), testContext(t)

		var h *domain.Hirer
		err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			registered, err := db.Hirers().Register(ctx, tx, port.NewHirerAccount{
				Hirer:        &domain.Hirer{Email: "hank@acme.com", DisplayName: "Hank", AuthProvider: domain.ProviderGoogle},
				Organization: &domain.Organization{Name: "Acme Corp", Slug: "acme"},
				PasswordHash: []byte("this must be ignored"),
			})
			if err != nil {
				return err
			}
			h = registered.Hirer
			return nil
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
			registered, err := db.Hirers().Register(ctx, tx, port.NewHirerAccount{
				Hirer:        &domain.Hirer{Email: "hank@acme.com", DisplayName: "Hank", AuthProvider: domain.ProviderGoogle},
				Organization: &domain.Organization{Name: "Acme", Slug: "acme"},
			})
			if err != nil {
				return err
			}
			h = registered.Hirer
			return nil
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

func TestOrganizationRepositoryRoster(t *testing.T) {
	t.Run("a redeemed seat inherits the organization's verification", func(t *testing.T) {
		// The whole point of verifying an org rather than a person
		// (ADR-0008 §3a).
		db, ctx := newDB(t), testContext(t)
		owner := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")
		admin := mustCreateAdmin(ctx, t, db)
		mustUnverify(ctx, t, db, owner.OrganizationID)
		mustVerify(ctx, t, db, owner.OrganizationID, admin)

		id := mustRoster(ctx, t, db, owner, "rita@acme.com", "rita")
		seat := mustRedeem(ctx, t, db, id, "Rita Sandoval")

		org, err := db.Hirers().Organization(ctx, seat.OrganizationID)
		if err != nil {
			t.Fatalf("reading the org: %v", err)
		}
		if !org.IsVerified() {
			t.Error("the redeemed seat's organization should be verified")
		}
		if n := count(t, db,
			`SELECT count(*) FROM verification_requests WHERE hirer_account_id = $1`, string(seat.ID)); n != 0 {
			t.Error("a redeemed seat must not create its own verification request")
		}
	})

	t.Run("the username and role come from the entry, not the redeemer", func(t *testing.T) {
		// A redeemer who could name themselves could take a colleague's name,
		// or claim owner and roster further people from it (ADR-0016 §3).
		db, ctx := newDB(t), testContext(t)
		owner := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")

		id := mustRoster(ctx, t, db, owner, "rita@acme.com", "rita")
		seat := mustRedeem(ctx, t, db, id, "Rita Sandoval")

		if seat.Username != "rita" {
			t.Errorf("username = %q, want the one the organization pinned", seat.Username)
		}
		if seat.Email != "rita@acme.com" {
			t.Errorf("email = %q, want the rostered address", seat.Email)
		}
		if n := count(t, db,
			`SELECT count(*) FROM organization_members
			  WHERE hirer_account_id = $1 AND role = 'member'`, string(seat.ID)); n != 1 {
			t.Error("the membership should carry the role the entry pinned")
		}
	})

	t.Run("an entry is single-use", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		owner := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")
		id := mustRoster(ctx, t, db, owner, "rita@acme.com", "rita")

		redeem := func(name string) error {
			return db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
				_, err := db.Organizations().RedeemRosterEntry(ctx, tx, id,
					&domain.Hirer{DisplayName: name, AuthProvider: domain.ProviderEmail},
					[]byte("hash"), time.Now())
				return err
			})
		}
		if err := redeem("Rita Sandoval"); err != nil {
			t.Fatalf("first redemption: %v", err)
		}
		// Redeemed, not absent. The entry is still there — it is the record of
		// why the seat exists — so the second attempt is a conflict.
		if err := redeem("Someone Else"); !errors.Is(err, port.ErrRosterEntryRedeemed) {
			t.Fatalf("expected a replayed redemption to be refused, got %v", err)
		}
		if n := count(t, db, `SELECT count(*) FROM hirer_accounts WHERE display_name = 'Someone Else'`); n != 0 {
			t.Error("a replayed redemption created a seat")
		}
	})

	t.Run("a username is claimed platform-wide the moment it is rostered", func(t *testing.T) {
		// Two organizations cannot both stage "maya" and discover the clash
		// only at redemption — by which point both have made an offer.
		db, ctx := newDB(t), testContext(t)
		acme := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")
		other := mustRegister(ctx, t, db, "pat@other.example", "Pat", "Other Ltd", "other-ltd")

		mustRoster(ctx, t, db, acme, "maya@acme.com", "maya")

		err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			return db.Organizations().ClaimUsername(ctx, tx, "maya")
		})
		if !errors.Is(err, port.ErrConflict) {
			t.Fatalf("expected a second organization's claim on the name to be refused, got %v", err)
		}
		_ = other
	})

	t.Run("a name a LIVE SEAT holds cannot be rostered", func(t *testing.T) {
		// The bug this pins: uq_roster_username only covers the roster, so
		// rostering an existing hirer's name succeeded and produced an entry
		// that could never be redeemed — the clash surfaced when the redeemer
		// clicked their link, which is the wrong person and the wrong moment.
		db, ctx := newDB(t), testContext(t)
		acme := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")
		studio := mustRegister(ctx, t, db, "sam@studio.example", "Sam", "Tiny Studio", "tiny-studio")

		err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			return db.Organizations().ClaimUsername(ctx, tx, studio.Username)
		})
		if !errors.Is(err, port.ErrConflict) {
			t.Fatalf("expected a live seat's name to be refused, got %v", err)
		}
		_ = acme
	})

	t.Run("a revoked seat's name is never released", func(t *testing.T) {
		// "Never reused, including after revocation" (ADR-0016 §1). The
		// foreign key from hirer_accounts is what enforces it, so no caller
		// has to remember.
		db, ctx := newDB(t), testContext(t)
		owner := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")
		id := mustRoster(ctx, t, db, owner, "rita@acme.com", "rita")
		seat := mustRedeem(ctx, t, db, id, "Rita Sandoval")

		if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			return db.Hirers().Disable(ctx, tx, seat.ID, owner.ID, time.Now())
		}); err != nil {
			t.Fatalf("disabling: %v", err)
		}

		err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			return db.Organizations().ReleaseUsername(ctx, tx, "rita")
		})
		if err == nil {
			t.Fatal("a name a seat has held must not be releasable")
		}
		if n := count(t, db, `SELECT count(*) FROM hirer_usernames WHERE username = 'rita'`); n != 1 {
			t.Error("the claim should survive")
		}
	})

	t.Run("a withdrawn offer gives its name back", func(t *testing.T) {
		// Nobody ever signed in as it, so nothing is erased by letting it be
		// used again — and burning a name over a typo would make the roster a
		// trap an owner falls into once and never forgets.
		db, ctx := newDB(t), testContext(t)
		owner := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")
		id := mustRoster(ctx, t, db, owner, "rita@acme.com", "rtia")

		if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			if err := db.Organizations().RemoveRosterEntry(ctx, tx, id); err != nil {
				return err
			}
			return db.Organizations().ReleaseUsername(ctx, tx, "rtia")
		}); err != nil {
			t.Fatalf("withdrawing: %v", err)
		}

		// And the name is free again.
		mustRoster(ctx, t, db, owner, "rita@acme.com", "rtia")
	})

	t.Run("a redeemed entry is not found by the redemption lookup", func(t *testing.T) {
		// RosterEntryByEmail is the "may this address still become a seat?"
		// question. A redeemed entry can never answer yes.
		db, ctx := newDB(t), testContext(t)
		owner := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")
		id := mustRoster(ctx, t, db, owner, "rita@acme.com", "rita")

		err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			_, err := db.Organizations().RosterEntryByEmail(ctx, tx, owner.OrganizationID, "rita@acme.com")
			return err
		})
		if err != nil {
			t.Fatalf("an unredeemed entry should be found: %v", err)
		}

		mustRedeem(ctx, t, db, id, "Rita Sandoval")

		err = db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			_, err := db.Organizations().RosterEntryByEmail(ctx, tx, owner.OrganizationID, "rita@acme.com")
			return err
		})
		if !errors.Is(err, port.ErrNotFound) {
			t.Fatalf("expected a redeemed entry to be invisible to the lookup, got %v", err)
		}
	})

	t.Run("the lookup is case-insensitive", func(t *testing.T) {
		// citext, like every other address in the schema: a roster that
		// distinguished Rita@ from rita@ would refuse the right person.
		db, ctx := newDB(t), testContext(t)
		owner := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")
		mustRoster(ctx, t, db, owner, "rita@acme.com", "rita")

		err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			_, err := db.Organizations().RosterEntryByEmail(ctx, tx, owner.OrganizationID, "Rita@Acme.com")
			return err
		})
		if err != nil {
			t.Fatalf("a differently-cased address should match: %v", err)
		}
	})

	t.Run("removing an entry never deletes the seat", func(t *testing.T) {
		// The seat authored shortlists and the permanent record of who was
		// told. Disabling is the repository's separate job (ADR-0016 §5).
		db, ctx := newDB(t), testContext(t)
		owner := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")
		id := mustRoster(ctx, t, db, owner, "rita@acme.com", "rita")
		seat := mustRedeem(ctx, t, db, id, "Rita Sandoval")

		if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			return db.Organizations().RemoveRosterEntry(ctx, tx, id)
		}); err != nil {
			t.Fatalf("removing: %v", err)
		}

		if n := count(t, db, `SELECT count(*) FROM hirer_accounts WHERE id = $1`, string(seat.ID)); n != 1 {
			t.Fatal("removing a roster entry destroyed the seat")
		}
		if n := count(t, db, `SELECT count(*) FROM organization_hirer_roster WHERE id = $1`, string(id)); n != 0 {
			t.Error("the entry should be gone")
		}
	})

	t.Run("a disabled seat cannot sign in and keeps its name", func(t *testing.T) {
		// ByUsername filters disabled_at, so a revoked seat is indistinguishable
		// from an unknown one. The row survives, and so does the name: that is
		// what keeps "rita" unambiguous on a round from two years ago.
		db, ctx := newDB(t), testContext(t)
		owner := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")
		id := mustRoster(ctx, t, db, owner, "rita@acme.com", "rita")
		seat := mustRedeem(ctx, t, db, id, "Rita Sandoval")

		if _, err := db.Hirers().ByUsername(ctx, "rita"); err != nil {
			t.Fatalf("a live seat should sign in: %v", err)
		}

		if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			return db.Hirers().Disable(ctx, tx, seat.ID, owner.ID, time.Now())
		}); err != nil {
			t.Fatalf("disabling: %v", err)
		}

		if _, err := db.Hirers().ByUsername(ctx, "rita"); !errors.Is(err, port.ErrNotFound) {
			t.Fatalf("expected a disabled seat to be unreachable by sign-in, got %v", err)
		}

		seats, err := db.Hirers().ListSeats(ctx, owner.OrganizationID)
		if err != nil {
			t.Fatalf("listing seats: %v", err)
		}
		var found bool
		for _, h := range seats {
			if h.Username == "rita" {
				found = true
				if h.DisabledAt == nil {
					t.Error("the revoked seat should be marked disabled")
				}
			}
		}
		if !found {
			t.Error("a revoked seat must stay listable, so old rounds can name their author")
		}
	})
}

func TestOrganizationRepositoryVerify(t *testing.T) {
	t.Run("verifying lifts every seat, including one rostered beforehand", func(t *testing.T) {
		// A roster may be built while the organization waits for review; only
		// REDEEMING one needs a verified org (ADR-0016 §2).
		db, ctx := newDB(t), testContext(t)
		owner := mustRegister(ctx, t, db, "pat@unknown.example", "Pat", "Unknown Ltd", "unknown-ltd")
		admin := mustCreateAdmin(ctx, t, db)
		mustUnverify(ctx, t, db, owner.OrganizationID)

		id := mustRoster(ctx, t, db, owner, "colleague@unknown.example", "colleague")
		mustRedeem(ctx, t, db, id, "Colleague Chen")

		org, _ := db.Hirers().Organization(ctx, owner.OrganizationID)
		if org.IsVerified() {
			t.Fatal("the org should start unverified")
		}

		mustVerify(ctx, t, db, owner.OrganizationID, admin)

		org, err := db.Hirers().Organization(ctx, owner.OrganizationID)
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
		// No verification_requests assertion. Onboarding creates none — the
		// submission is the record and approving it is the verification
		// (ADR-0017 §2) — so there is no request here to close. What this
		// test is actually about is that lifting the organisation lifts every
		// seat under it, which the membership check above covers.
	})

	t.Run("verifying twice conflicts", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		owner := mustRegister(ctx, t, db, "pat@unknown.example", "Pat", "Unknown Ltd", "unknown-ltd")
		admin := mustCreateAdmin(ctx, t, db)
		mustUnverify(ctx, t, db, owner.OrganizationID)
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
		mustUnverify(ctx, t, db, owner.OrganizationID)
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

// mustRegister creates an organisation and its owner, through the real path.
//
// Since ADR-0017 that path is ONBOARDING, not registration: a form is
// submitted, the address is proven, and an administrator approves — which is
// the only thing that creates a company. Registration now makes an independent
// hirer with no organisation at all, so a helper that still called it would be
// handing every test an account that belongs to nobody.
//
// Kept under the old name and signature so the forty call sites below read the
// same. What changed is which door they go through, and that is the point.
func mustRegister(ctx context.Context, t *testing.T, db *postgres.DB, email, name, orgName, orgSlug string) *domain.Hirer {
	t.Helper()

	username := usernameFrom(email)
	var submission *port.OnboardingSubmission
	if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		var err error
		submission, err = db.Onboarding().Submit(ctx, tx, &port.OnboardingSubmission{
			Name: orgName, Email: email,
		})
		return err
	}); err != nil {
		t.Fatalf("submitting %q: %v", orgName, err)
	}

	if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		if err := db.Organizations().ClaimUsername(ctx, tx, username); err != nil {
			return err
		}
		return db.Onboarding().Prove(ctx, tx, submission.ID, port.OnboardingOwner{
			Username: username, DisplayName: name, PasswordHash: []byte("argon2id-hash"),
		}, time.Now())
	}); err != nil {
		t.Fatalf("proving %q: %v", orgName, err)
	}

	admin := mustCreateAdmin(ctx, t, db)
	var org *domain.Organization
	if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		var err error
		org, err = db.Onboarding().Promote(ctx, tx, submission.ID, orgSlug, admin, time.Now())
		return err
	}); err != nil {
		t.Fatalf("approving %q: %v", orgName, err)
	}

	owner, err := db.Hirers().ByUsername(ctx, username)
	if err != nil {
		t.Fatalf("reading back the owner of %q: %v", orgName, err)
	}
	owner.Organization = org
	return owner
}

// usernameFrom derives a sign-in name from an address, for the tests that do
// not care what it is.
//
// The local part only, so two addresses at different domains do not collide on
// the unconditional unique index. Tests that assert ON the username pass their
// own rather than relying on this.
func usernameFrom(email string) string {
	if at := strings.IndexByte(email, '@'); at > 0 {
		return strings.ReplaceAll(email[:at], ".", "")
	}
	return email
}

// mustRoster names an address for a future seat, returning the entry id.
func mustRoster(ctx context.Context, t *testing.T, db *postgres.DB, owner *domain.Hirer, email, username string) domain.RosterEntryID {
	t.Helper()
	var id domain.RosterEntryID
	err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		// The service claims the name out of the global namespace before
		// writing the entry; a test that skipped it would be exercising a
		// path production never takes.
		if err := db.Organizations().ClaimUsername(ctx, tx, username); err != nil {
			return err
		}
		entry, err := db.Organizations().AddRosterEntry(ctx, tx, &port.RosterEntry{
			OrganizationID: owner.OrganizationID, Email: email, Username: username,
			Role: domain.RoleMember, AddedBy: domain.RefTo(owner),
		})
		if err != nil {
			return err
		}
		id = entry.ID
		return nil
	})
	if err != nil {
		t.Fatalf("rostering %s: %v", email, err)
	}
	return id
}

// mustRedeem turns an entry into a seat.
func mustRedeem(ctx context.Context, t *testing.T, db *postgres.DB, id domain.RosterEntryID, name string) *domain.Hirer {
	t.Helper()
	var seat *domain.Hirer
	err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		var err error
		seat, err = db.Organizations().RedeemRosterEntry(ctx, tx, id,
			&domain.Hirer{DisplayName: name, AuthProvider: domain.ProviderEmail},
			[]byte("argon2id-hash"), time.Now())
		return err
	})
	if err != nil {
		t.Fatalf("redeeming %s: %v", id, err)
	}
	return seat
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

// mustRegisterIndependent signs up a hirer with NO organisation.
//
// What POST /auth/hirer/register does since ADR-0017 §1: someone hiring on
// their own account rather than a company's, reviewed individually. Distinct
// from mustRegister above, which onboards a whole company.
func mustRegisterIndependent(ctx context.Context, t *testing.T, db *postgres.DB, email, username, name string) *domain.Hirer {
	t.Helper()
	var h *domain.Hirer
	if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		registered, err := db.Hirers().Register(ctx, tx, port.NewHirerAccount{
			Hirer: &domain.Hirer{
				Email: email, Username: username, DisplayName: name,
				AuthProvider: domain.ProviderEmail,
			},
			PasswordHash: []byte("argon2id-hash"),
		})
		if err != nil {
			return err
		}
		h = registered.Hirer
		return nil
	}); err != nil {
		t.Fatalf("registering %s: %v", email, err)
	}
	return h
}

// mustUnverify clears an organisation's verification.
//
// It exists because ADR-0017 removed the only path that produced an unverified
// organisation: onboarding creates the company AND verifies it in one step, so
// there is no product flow that leaves `verified_at` null any more.
//
// The tests below still exercise VerifyOrganization, which is now reachable
// only by constructing the state it acts on. That is worth saying out loud
// rather than hiding in a helper: the mechanism is intact, its trigger is gone,
// and whether it should be kept is a question for the Planner rather than
// something to settle by deleting tests.
func mustUnverify(ctx context.Context, t *testing.T, db *postgres.DB, org domain.OrganizationID) {
	t.Helper()
	if _, err := db.Pool().Exec(ctx,
		`UPDATE organizations SET verified_at = NULL, verified_by = NULL WHERE id = $1`,
		string(org)); err != nil {
		t.Fatalf("un-verifying %s: %v", org, err)
	}
}
