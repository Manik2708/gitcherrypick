package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
	"github.com/Manik2708/gitcherrypick/backend/internal/repository/postgres"
)

// Openings (ADR-0019). The properties worth holding at this layer are the ones
// the service cannot enforce alone: that an open role is never mutated, that a
// revision and the close it implies are one act, and that a closed role always
// says why.

func TestRoleIsCreatedAsADraft(t *testing.T) {
	// A role is never born open. Opening is a separate act, which is what makes
	// the organisation's authority setting enforceable at all — a create that
	// opened would hand the commitment to whoever could stage it.
	db, ctx := newDB(t), testContext(t)
	hirer := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")

	role := mustCreateRole(ctx, t, db, hirer, func(r *domain.Role) {
		r.EligibleCountries = []string{"GB", "DE"}
		r.Questions = []domain.RoleQuestion{{Question: "Do you need a visa?"}}
	})

	if role.Status != domain.RoleDraft {
		t.Errorf("status = %q, want draft", role.Status)
	}
	if role.OpenedBy != nil || role.OpenedAt != nil {
		t.Error("a draft carries no signature")
	}
	if len(role.EligibleCountries) != 2 {
		t.Errorf("eligible countries = %v", role.EligibleCountries)
	}
	if len(role.Questions) != 1 || role.Questions[0].Position != 0 {
		t.Errorf("questions = %+v", role.Questions)
	}
}

func TestOpenRoleCannotBeUpdated(t *testing.T) {
	// THE rule of ADR-0019 §13. A contributor contacted last week must keep
	// reading the salary they agreed to talk about, and the guarantee lives in
	// the statement rather than in anybody remembering to check.
	db, ctx := newDB(t), testContext(t)
	hirer := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")
	role := mustCreateRole(ctx, t, db, hirer, nil)

	// A draft is editable.
	role.Title = "Senior backend engineer"
	updated := mustUpdateDraft(ctx, t, db, role)
	if updated.Title != "Senior backend engineer" {
		t.Fatalf("a draft should be editable, title = %q", updated.Title)
	}

	opened := mustOpen(ctx, t, db, role.ID, hirer)
	if opened.OpenedBy == nil || *opened.OpenedBy != hirer.ID {
		t.Error("opening must name who signed it")
	}

	opened.Title = "Changed after everybody was told"
	err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		_, err := db.Roles().UpdateDraft(ctx, tx, opened)
		return err
	})
	if !errors.Is(err, port.ErrConflict) {
		t.Fatalf("editing an open role should conflict, got %v", err)
	}

	after, err := db.Roles().ByID(ctx, role.ID)
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if after.Title != "Senior backend engineer" {
		t.Errorf("the open role was edited anyway: %q", after.Title)
	}
}

func TestReviseSupersedesAndClosesInOneAct(t *testing.T) {
	db, ctx := newDB(t), testContext(t)
	hirer := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")
	original := mustCreateRole(ctx, t, db, hirer, nil)
	mustOpen(ctx, t, db, original.ID, hirer)

	ctc := int64(95_000_00)
	next := domain.Role{
		OrgID: hirer.OrganizationID, Title: "Backend engineer",
		Engagement: domain.EngagementFullTime, Location: domain.LocationRemote,
		Currency: "GBP", YearlyCTC: &ctc, CreatedBy: hirer.ID,
	}

	var revised *domain.Role
	if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		out, err := db.Roles().Revise(ctx, tx, original.ID, &next, hirer.ID, time.Now())
		revised = out
		return err
	}); err != nil {
		t.Fatalf("revising: %v", err)
	}

	if revised.Supersedes == nil || *revised.Supersedes != original.ID {
		t.Error("the successor must point at what it replaced")
	}
	if revised.Status != domain.RoleDraft {
		// Opening it is the commitment, governed by the update setting. A
		// revision that opened itself would route around the owner.
		t.Errorf("a successor is a draft, got %q", revised.Status)
	}

	old, err := db.Roles().ByID(ctx, original.ID)
	if err != nil {
		t.Fatalf("reading the original: %v", err)
	}
	if old.Status != domain.RoleClosed {
		t.Errorf("the original should be closed, got %q", old.Status)
	}
	if old.CloseReason == nil || *old.CloseReason != domain.CloseSuperseded {
		t.Errorf("close reason = %v, want superseded", old.CloseReason)
	}

	t.Run("a role is replaced at most once", func(t *testing.T) {
		// Two successors would leave two rows each claiming to be current, and
		// nothing able to say which a contributor should be shown.
		second := next
		err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			_, err := db.Roles().Revise(ctx, tx, original.ID, &second, hirer.ID, time.Now())
			return err
		})
		if !errors.Is(err, port.ErrConflict) {
			t.Fatalf("a second revision should conflict, got %v", err)
		}
	})
}

func TestCloseRecordsWhyAndWho(t *testing.T) {
	db, ctx := newDB(t), testContext(t)
	hirer := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")

	t.Run("a request to close leaves the role open", func(t *testing.T) {
		// A request is not an outcome. The role still matches, can still be
		// shortlisted against, and the promise it makes still stands.
		role := mustCreateRole(ctx, t, db, hirer, nil)
		mustOpen(ctx, t, db, role.ID, hirer)

		var out *domain.Role
		if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			requested, err := db.Roles().RequestClose(ctx, tx, role.ID, hirer.ID, time.Now())
			out = requested
			return err
		}); err != nil {
			t.Fatalf("requesting closure: %v", err)
		}
		if out.Status != domain.RoleOpen {
			t.Errorf("status = %q, want open", out.Status)
		}
		if out.CloseRequestedBy == nil {
			t.Error("the request must name who asked")
		}
	})

	t.Run("hired through the platform names everybody", func(t *testing.T) {
		// A role may fill several seats: "two backend engineers" is one posting
		// and two people, and a single column would have lost the second.
		role := mustCreateRole(ctx, t, db, hirer, nil)
		mustOpen(ctx, t, db, role.ID, hirer)

		alice := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		bob := mustCreateContributor(ctx, t, db, "Bob Nakamura", 100002, "bobn")

		now := time.Now()
		var closed *domain.Role
		if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			out, err := db.Roles().Close(ctx, tx, role.ID, port.RoleClosure{
				By: hirer.ID, At: now, Reason: domain.CloseHiredViaPlatform,
				Hires: []domain.UserID{alice.ID, bob.ID},
			})
			closed = out
			return err
		}); err != nil {
			t.Fatalf("closing: %v", err)
		}

		if closed.Status != domain.RoleClosed {
			t.Errorf("status = %q", closed.Status)
		}
		if closed.CloseReason == nil || *closed.CloseReason != domain.CloseHiredViaPlatform {
			t.Errorf("reason = %v", closed.CloseReason)
		}
		if len(closed.Hires) != 2 {
			t.Fatalf("expected two hires, got %d", len(closed.Hires))
		}
		if closed.ClosedBy == nil || *closed.ClosedBy != hirer.ID {
			t.Error("a close must name who did it")
		}
	})

	t.Run("a closed role cannot be closed again", func(t *testing.T) {
		role := mustCreateRole(ctx, t, db, hirer, nil)
		mustOpen(ctx, t, db, role.ID, hirer)
		mustClose(ctx, t, db, role.ID, hirer, domain.CloseNotNeeded)

		err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			_, err := db.Roles().Close(ctx, tx, role.ID, port.RoleClosure{
				By: hirer.ID, At: time.Now(), Reason: domain.CloseNotNeeded,
			})
			return err
		})
		if !errors.Is(err, port.ErrConflict) {
			t.Fatalf("expected a conflict, got %v", err)
		}
	})
}

// Matching is where a contributor's own profile decides what they are shown
// (ADR-0019 §Endpoints). The two asymmetries are the point: an unstated office
// figure clears a minimum because it is self-reported either way, and unknown
// OSS years do NOT, because that figure is verified.
func TestMatchingFailsOpenExceptWhereItMustNot(t *testing.T) {
	db, ctx := newDB(t), testContext(t)
	hirer := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")

	three := 3
	anywhere := mustCreateRole(ctx, t, db, hirer, nil)
	mustOpen(ctx, t, db, anywhere.ID, hirer)

	ukOnly := mustCreateRole(ctx, t, db, hirer, func(r *domain.Role) {
		r.EligibleCountries = []string{"GB"}
	})
	mustOpen(ctx, t, db, ukOnly.ID, hirer)

	experienced := mustCreateRole(ctx, t, db, hirer, func(r *domain.Role) {
		r.MinOSSYOE = &three
	})
	mustOpen(ctx, t, db, experienced.ID, hirer)

	full := []domain.Engagement{domain.EngagementFullTime}
	has := func(roles []domain.Role, id domain.RoleID) bool {
		for _, r := range roles {
			if r.ID == id {
				return true
			}
		}
		return false
	}

	t.Run("no stated country sees roles that hire anywhere", func(t *testing.T) {
		out, err := db.Roles().Matching(ctx, port.RoleMatch{Shapes: full})
		if err != nil {
			t.Fatalf("matching: %v", err)
		}
		if !has(out, anywhere.ID) {
			t.Error("a role with no restriction must reach somebody who has not said where they are")
		}
		if has(out, ukOnly.ID) {
			t.Error("a UK-only role must not reach somebody whose country is unknown")
		}
	})

	t.Run("unknown open-source years do not clear a minimum", func(t *testing.T) {
		// Fail-CLOSED, deliberately and against the grain of this codebase:
		// failing open would make any minimum clearable with a link nobody
		// could check.
		out, err := db.Roles().Matching(ctx, port.RoleMatch{Shapes: full, Country: "GB"})
		if err != nil {
			t.Fatalf("matching: %v", err)
		}
		if has(out, experienced.ID) {
			t.Error("a verified minimum must not be cleared by an unknown figure")
		}
		if !has(out, ukOnly.ID) {
			t.Error("a UK contributor should see a UK role")
		}
	})

	t.Run("enough verified years clears it", func(t *testing.T) {
		five := 5
		out, err := db.Roles().Matching(ctx, port.RoleMatch{
			Shapes: full, Country: "GB", OSSYears: &five,
		})
		if err != nil {
			t.Fatalf("matching: %v", err)
		}
		if !has(out, experienced.ID) {
			t.Error("five years should clear a minimum of three")
		}
	})

	t.Run("nothing ticked matches nothing", func(t *testing.T) {
		// A person who said what they want and wants none of these should not
		// be shown roles they did not ask for.
		out, err := db.Roles().Matching(ctx, port.RoleMatch{})
		if err != nil {
			t.Fatalf("matching: %v", err)
		}
		if len(out) != 0 {
			t.Errorf("expected nothing, got %d roles", len(out))
		}
	})

	t.Run("a draft matches nobody", func(t *testing.T) {
		draft := mustCreateRole(ctx, t, db, hirer, nil)
		out, err := db.Roles().Matching(ctx, port.RoleMatch{Shapes: full})
		if err != nil {
			t.Fatalf("matching: %v", err)
		}
		if has(out, draft.ID) {
			t.Error("a draft is not a commitment and must not be shown")
		}
	})
}

// Settings default rather than requiring a row, so nothing needs backfilling
// and an organisation that never opens the screen behaves like one that opened
// it and changed nothing.
func TestOrgSettingsDefaultWithoutARow(t *testing.T) {
	db, ctx := newDB(t), testContext(t)
	hirer := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")

	got, err := db.OrgSettings().Settings(ctx, hirer.OrganizationID)
	if err != nil {
		t.Fatalf("reading settings: %v", err)
	}
	if got.RoleCreateAuthority != domain.AuthorityDraftAndApprove {
		t.Errorf("create authority = %q, want draft_and_approve", got.RoleCreateAuthority)
	}
	if got.RoleCloseAuthority != domain.AuthorityAnyHirer {
		// Closing only ever WITHDRAWS a commitment, and a rule that makes the
		// safe direction harder than the unsafe one gets worked around.
		t.Errorf("close authority = %q, want any_hirer", got.RoleCloseAuthority)
	}

	got.RoleCreateAuthority = domain.AuthorityOwnersOnly
	if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		return db.OrgSettings().Save(ctx, tx, got)
	}); err != nil {
		t.Fatalf("saving settings: %v", err)
	}

	after, err := db.OrgSettings().Settings(ctx, hirer.OrganizationID)
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if after.RoleCreateAuthority != domain.AuthorityOwnersOnly {
		t.Errorf("create authority = %q", after.RoleCreateAuthority)
	}
}

// ReleasedTo is the only way an organisation may name a person (ADR-0019 §15).
// An address never released is indistinguishable from one with no account here,
// which is what stops the field being an oracle.
func TestReleasedToRefusesAnAddressNeverGiven(t *testing.T) {
	db, ctx := newDB(t), testContext(t)
	hirer := mustRegister(ctx, t, db, "hank@acme.com", "Hank", "Acme", "acme")
	alice := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")

	_, err := db.Contacts().ReleasedTo(ctx, hirer.OrganizationID, "alice@example.com")
	if !errors.Is(err, port.ErrNotFound) {
		t.Fatalf("an address never released must not resolve, got %v", err)
	}
	_ = alice
}

// --- helpers -----------------------------------------------------------------

func mustCreateRole(ctx context.Context, t *testing.T, db *postgres.DB, hirer *domain.Hirer, shape func(*domain.Role)) *domain.Role {
	t.Helper()

	in := domain.Role{
		OrgID: hirer.OrganizationID, Title: "Backend engineer",
		Engagement: domain.EngagementFullTime, Location: domain.LocationRemote,
		CreatedBy: hirer.ID,
	}
	if shape != nil {
		shape(&in)
	}

	var out *domain.Role
	if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		created, err := db.Roles().Create(ctx, tx, &in)
		out = created
		return err
	}); err != nil {
		t.Fatalf("creating a role: %v", err)
	}
	return out
}

func mustUpdateDraft(ctx context.Context, t *testing.T, db *postgres.DB, role *domain.Role) *domain.Role {
	t.Helper()
	var out *domain.Role
	if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		updated, err := db.Roles().UpdateDraft(ctx, tx, role)
		out = updated
		return err
	}); err != nil {
		t.Fatalf("updating a draft: %v", err)
	}
	return out
}

func mustOpen(ctx context.Context, t *testing.T, db *postgres.DB, id domain.RoleID, by *domain.Hirer) *domain.Role {
	t.Helper()
	var out *domain.Role
	if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		opened, err := db.Roles().Open(ctx, tx, id, by.ID, time.Now())
		out = opened
		return err
	}); err != nil {
		t.Fatalf("opening a role: %v", err)
	}
	return out
}

func mustClose(ctx context.Context, t *testing.T, db *postgres.DB, id domain.RoleID, by *domain.Hirer, reason domain.CloseReason) *domain.Role {
	t.Helper()
	var out *domain.Role
	if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		closed, err := db.Roles().Close(ctx, tx, id, port.RoleClosure{
			By: by.ID, At: time.Now(), Reason: reason,
		})
		out = closed
		return err
	}); err != nil {
		t.Fatalf("closing a role: %v", err)
	}
	return out
}
