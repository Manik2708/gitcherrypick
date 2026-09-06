package postgres_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
	"github.com/Manik2708/gitcherrypick/backend/internal/repository/postgres"
)

func TestClaimRepositoryReplace(t *testing.T) {
	t.Run("removes what it does not mention", func(t *testing.T) {
		// A replace is not a merge. ADR-0008 §6 makes PUT the atomic form of
		// the three POST sub-resources, so anything absent is deleted.
		db, ctx := newDB(t), testContext(t)
		seedCatalogue(ctx, t, db)
		user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		claim := mustCreateDraft(ctx, t, db, user.ID)

		first := &domain.Claim{
			PREvidence: []domain.PREvidence{
				{Position: 1, RepoOwner: "acme", RepoName: "lib", PRNumber: 55},
				{Position: 2, RepoOwner: "acme", RepoName: "lib", PRNumber: 56},
			},
			Skills: []domain.ClaimSkill{{Slug: "kubernetes", IsNominatedPrimary: true}},
		}
		got := mustReplace(ctx, t, db, claim.ID, 1, first)
		if got.Version != 2 {
			t.Errorf("expected version 2, got %d", got.Version)
		}

		second := &domain.Claim{
			PREvidence: []domain.PREvidence{{Position: 1, RepoOwner: "acme", RepoName: "app", PRNumber: 101}},
			ProjectEvidence: []domain.ProjectEvidence{
				{RepoOwner: "acme", RepoName: "platform", ContributionSummary: "Wrote the controller."},
			},
			Skills: []domain.ClaimSkill{{Slug: "go", IsNominatedPrimary: true}},
		}
		got = mustReplace(ctx, t, db, claim.ID, 2, second)

		if len(got.PREvidence) != 1 || got.PREvidence[0].PRNumber != 101 {
			t.Errorf("expected only PR 101, got %+v", got.PREvidence)
		}
		if len(got.Skills) != 1 || got.Skills[0].Slug != "go" {
			t.Errorf("expected only 'go', got %+v", got.Skills)
		}
		if len(got.ProjectEvidence) != 1 {
			t.Errorf("expected the project, got %+v", got.ProjectEvidence)
		}
	})

	t.Run("a stale version loses and changes nothing", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		seedCatalogue(ctx, t, db)
		user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		claim := mustCreateDraft(ctx, t, db, user.ID)

		mustReplace(ctx, t, db, claim.ID, 1, &domain.Claim{
			Skills: []domain.ClaimSkill{{Slug: "go", IsNominatedPrimary: true}},
		})

		err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			_, err := db.Claims().Replace(ctx, tx, claim.ID, 1, &domain.Claim{
				Skills: []domain.ClaimSkill{{Slug: "postgres", IsNominatedPrimary: true}},
			})
			return err
		})
		if !errors.Is(err, port.ErrVersionStale) {
			t.Fatalf("expected port.ErrVersionStale, got %v", err)
		}

		got, err := db.Claims().ByID(ctx, claim.ID)
		if err != nil {
			t.Fatalf("reading back: %v", err)
		}
		if len(got.Skills) != 1 || got.Skills[0].Slug != "go" {
			t.Errorf("the losing write took effect: %+v", got.Skills)
		}
		if got.Version != 2 {
			t.Errorf("expected version untouched at 2, got %d", got.Version)
		}
	})

	t.Run("a locked claim refuses the edit", func(t *testing.T) {
		// ADR-0003's anti-reroll rule, stated at the edit rather than at the
		// submit. Reported as locked, not as a version conflict.
		db, ctx := newDB(t), testContext(t)
		seedCatalogue(ctx, t, db)
		user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		claim := mustCreateDraft(ctx, t, db, user.ID)

		mustEvaluate(ctx, t, db, claim.ID, time.Now(), time.Now().Add(domain.LockWindow))

		err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			_, err := db.Claims().Replace(ctx, tx, claim.ID, 1, &domain.Claim{
				Skills: []domain.ClaimSkill{{Slug: "go", IsNominatedPrimary: true}},
			})
			return err
		})
		if !errors.Is(err, port.ErrConflict) {
			t.Fatalf("expected port.ErrConflict, got %v", err)
		}
		if err != nil && !strings.Contains(err.Error(), "locked") {
			t.Errorf("expected the error to say it is locked, got %q", err)
		}
	})

	t.Run("an expired lock reopens the claim", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		seedCatalogue(ctx, t, db)
		user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		claim := mustCreateDraft(ctx, t, db, user.ID)

		mustEvaluate(ctx, t, db, claim.ID, time.Now().Add(-8*24*time.Hour), time.Now().Add(-time.Hour))

		got := mustReplace(ctx, t, db, claim.ID, 1, &domain.Claim{
			Skills: []domain.ClaimSkill{{Slug: "go", IsNominatedPrimary: true}},
		})
		// Editing an evaluated claim returns it to draft; the old scores stand
		// until a new submission replaces them (ADR-0003).
		if got.Status != domain.ClaimDraft {
			t.Errorf("expected the edit to return the claim to draft, got %s", got.Status)
		}
	})

	t.Run("the lock is per claim, not per account", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		seedCatalogue(ctx, t, db)
		user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")

		locked := mustCreateDraft(ctx, t, db, user.ID)
		mustEvaluate(ctx, t, db, locked.ID, time.Now(), time.Now().Add(domain.LockWindow))

		other := mustCreateDraft(ctx, t, db, user.ID)
		if _, err := db.Claims().ByID(ctx, other.ID); err != nil {
			t.Fatalf("reading the other claim: %v", err)
		}
		got := mustReplace(ctx, t, db, other.ID, 1, &domain.Claim{
			Skills: []domain.ClaimSkill{{Slug: "go", IsNominatedPrimary: true}},
		})
		if got.Version != 2 {
			t.Error("a contributor mid-way through another skill must not be frozen")
		}
	})
}

func TestClaimRepositoryFingerprint(t *testing.T) {
	t.Run("is stable under reordering", func(t *testing.T) {
		// The evidence is a set; position is presentation. Reordering the same
		// five PRs must not read as new evidence.
		db, ctx := newDB(t), testContext(t)
		seedCatalogue(ctx, t, db)
		user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")

		a := mustCreateDraft(ctx, t, db, user.ID)
		mustReplace(ctx, t, db, a.ID, 1, &domain.Claim{
			PREvidence: []domain.PREvidence{
				{Position: 1, RepoOwner: "acme", RepoName: "lib", PRNumber: 55},
				{Position: 2, RepoOwner: "acme", RepoName: "lib", PRNumber: 56},
			},
			Skills: []domain.ClaimSkill{{Slug: "go", IsNominatedPrimary: true}},
		})

		b := mustCreateDraft(ctx, t, db, user.ID)
		mustReplace(ctx, t, db, b.ID, 1, &domain.Claim{
			PREvidence: []domain.PREvidence{
				{Position: 1, RepoOwner: "acme", RepoName: "lib", PRNumber: 56},
				{Position: 2, RepoOwner: "acme", RepoName: "lib", PRNumber: 55},
			},
			Skills: []domain.ClaimSkill{{Slug: "go", IsNominatedPrimary: true}},
		})

		fa := mustFingerprint(ctx, t, db, a.ID)
		fb := mustFingerprint(ctx, t, db, b.ID)
		if fa != fb {
			t.Errorf("reordering changed the fingerprint:\n  %s\n  %s", fa, fb)
		}
	})

	t.Run("changes when the evidence changes", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		seedCatalogue(ctx, t, db)
		user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		claim := mustCreateDraft(ctx, t, db, user.ID)

		mustReplace(ctx, t, db, claim.ID, 1, &domain.Claim{
			PREvidence: []domain.PREvidence{{Position: 1, RepoOwner: "acme", RepoName: "lib", PRNumber: 55}},
			Skills:     []domain.ClaimSkill{{Slug: "go", IsNominatedPrimary: true}},
		})
		before := mustFingerprint(ctx, t, db, claim.ID)

		mustReplace(ctx, t, db, claim.ID, 2, &domain.Claim{
			PREvidence: []domain.PREvidence{{Position: 1, RepoOwner: "acme", RepoName: "lib", PRNumber: 99}},
			Skills:     []domain.ClaimSkill{{Slug: "go", IsNominatedPrimary: true}},
		})
		if after := mustFingerprint(ctx, t, db, claim.ID); after == before {
			t.Error("changed evidence produced the same fingerprint")
		}
	})

	t.Run("a different skill is different evidence", func(t *testing.T) {
		// The same PRs claimed for a different skill is a different judgement,
		// so it must not be refused as an unchanged resubmission.
		db, ctx := newDB(t), testContext(t)
		seedCatalogue(ctx, t, db)
		user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")

		prs := []domain.PREvidence{{Position: 1, RepoOwner: "acme", RepoName: "lib", PRNumber: 55}}

		a := mustCreateDraft(ctx, t, db, user.ID)
		mustReplace(ctx, t, db, a.ID, 1, &domain.Claim{PREvidence: prs,
			Skills: []domain.ClaimSkill{{Slug: "go", IsNominatedPrimary: true}}})

		b := mustCreateDraft(ctx, t, db, user.ID)
		mustReplace(ctx, t, db, b.ID, 1, &domain.Claim{PREvidence: prs,
			Skills: []domain.ClaimSkill{{Slug: "kubernetes", IsNominatedPrimary: true}}})

		if mustFingerprint(ctx, t, db, a.ID) == mustFingerprint(ctx, t, db, b.ID) {
			t.Error("the same PRs for a different skill must fingerprint differently")
		}
	})
}

func TestClaimRepositorySuggestions(t *testing.T) {
	t.Run("a decision is final in both directions", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		seedCatalogue(ctx, t, db)
		user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		claim := mustCreateDraft(ctx, t, db, user.ID)

		mustReplace(ctx, t, db, claim.ID, 1, &domain.Claim{
			Skills: []domain.ClaimSkill{
				{Slug: "go", IsNominatedPrimary: true},
				{Slug: "kubernetes", Origin: domain.AISuggested},
			},
		})
		k8s := skillID(ctx, t, db, "kubernetes")

		decide := func(accept bool) error {
			return db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
				_, err := db.Claims().DecideSuggestion(ctx, tx, claim.ID, k8s, accept)
				return err
			})
		}
		if err := decide(true); err != nil {
			t.Fatalf("accepting: %v", err)
		}
		if err := decide(false); !errors.Is(err, port.ErrConflict) {
			t.Fatalf("expected dismissing an accepted suggestion to conflict, got %v", err)
		}
		if err := decide(true); !errors.Is(err, port.ErrConflict) {
			t.Fatalf("expected re-accepting to conflict, got %v", err)
		}
	})

	t.Run("a declared skill is not a suggestion", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		seedCatalogue(ctx, t, db)
		user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		claim := mustCreateDraft(ctx, t, db, user.ID)

		mustReplace(ctx, t, db, claim.ID, 1, &domain.Claim{
			Skills: []domain.ClaimSkill{{Slug: "go", IsNominatedPrimary: true}},
		})
		goID := skillID(ctx, t, db, "go")

		err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			_, err := db.Claims().DecideSuggestion(ctx, tx, claim.ID, goID, true)
			return err
		})
		if !errors.Is(err, port.ErrConflict) {
			t.Fatalf("expected a declared skill to be unreachable through this path, got %v", err)
		}
	})

	t.Run("an undecided suggestion is inert", func(t *testing.T) {
		// ADR-0003 §10: invisible, unscored, excluded from every total until
		// the contributor accepts it.
		db, ctx := newDB(t), testContext(t)
		seedCatalogue(ctx, t, db)
		user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		claim := mustCreateDraft(ctx, t, db, user.ID)

		mustReplace(ctx, t, db, claim.ID, 1, &domain.Claim{
			Skills: []domain.ClaimSkill{
				{Slug: "go", IsNominatedPrimary: true},
				{Slug: "kubernetes", Origin: domain.AISuggested},
			},
		})

		got, err := db.Claims().ByID(ctx, claim.ID)
		if err != nil {
			t.Fatalf("reading: %v", err)
		}
		var inert int
		for _, s := range got.Skills {
			if s.IsInert() {
				inert++
			}
		}
		if inert != 1 {
			t.Errorf("expected exactly 1 inert suggestion, got %d", inert)
		}
	})
}

func TestClaimRepositoryLifecycle(t *testing.T) {
	t.Run("lists newest first", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")

		first := mustCreateDraft(ctx, t, db, user.ID)
		time.Sleep(2 * time.Millisecond)
		second := mustCreateDraft(ctx, t, db, user.ID)

		got, err := db.Claims().ListByUser(ctx, user.ID)
		if err != nil {
			t.Fatalf("listing: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("expected 2 claims, got %d", len(got))
		}
		if got[0].ID != second.ID || got[1].ID != first.ID {
			t.Error("expected newest first")
		}
	})

	t.Run("does not span users", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		alice := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		bob := mustCreateContributor(ctx, t, db, "Bob Nakamura", 100002, "bobn")
		mustCreateDraft(ctx, t, db, alice.ID)

		got, err := db.Claims().ListByUser(ctx, bob.ID)
		if err != nil {
			t.Fatalf("listing: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("expected Bob to have no claims, got %d", len(got))
		}
	})

	t.Run("submitting stamps submitted_at", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		claim := mustCreateDraft(ctx, t, db, user.ID)

		if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			return db.Claims().SetStatus(ctx, tx, claim.ID, domain.ClaimQueued)
		}); err != nil {
			t.Fatalf("submitting: %v", err)
		}

		got, err := db.Claims().ByID(ctx, claim.ID)
		if err != nil {
			t.Fatalf("reading: %v", err)
		}
		if got.SubmittedAt == nil {
			t.Error("expected submitted_at stamped")
		}
		if got.IsLocked(time.Now()) {
			t.Error("a queued claim is not yet locked")
		}
	})

	t.Run("evaluating starts the seven-day lock", func(t *testing.T) {
		db, ctx := newDB(t), testContext(t)
		user := mustCreateContributor(ctx, t, db, "Alice Okafor", 100001, "aliceok")
		claim := mustCreateDraft(ctx, t, db, user.ID)

		now := time.Now()
		mustEvaluate(ctx, t, db, claim.ID, now, now.Add(domain.LockWindow))

		got, err := db.Claims().ByID(ctx, claim.ID)
		if err != nil {
			t.Fatalf("reading: %v", err)
		}
		if !got.IsLocked(now) {
			t.Error("expected the claim locked immediately after evaluation")
		}
		if got.IsLocked(now.Add(8 * 24 * time.Hour)) {
			t.Error("expected the lock to have expired after eight days")
		}
	})
}

// --- helpers -----------------------------------------------------------------

func mustCreateDraft(ctx context.Context, t *testing.T, db *postgres.DB, user domain.UserID) *domain.Claim {
	t.Helper()
	c, err := db.Claims().Create(ctx, user)
	if err != nil {
		t.Fatalf("creating a draft: %v", err)
	}
	return c
}

func mustReplace(ctx context.Context, t *testing.T, db *postgres.DB, id domain.ClaimID, version int, c *domain.Claim) *domain.Claim {
	t.Helper()
	var got *domain.Claim
	if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		var err error
		got, err = db.Claims().Replace(ctx, tx, id, version, c)
		return err
	}); err != nil {
		t.Fatalf("replacing: %v", err)
	}
	return got
}

func mustEvaluate(ctx context.Context, t *testing.T, db *postgres.DB, id domain.ClaimID, at, lockedUntil time.Time) {
	t.Helper()
	if err := db.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		return db.Claims().SetEvaluated(ctx, tx, id, at, lockedUntil)
	}); err != nil {
		t.Fatalf("marking evaluated: %v", err)
	}
}

func mustFingerprint(ctx context.Context, t *testing.T, db *postgres.DB, id domain.ClaimID) string {
	t.Helper()
	f, err := db.Claims().Fingerprint(ctx, id)
	if err != nil {
		t.Fatalf("fingerprinting: %v", err)
	}
	return f
}
