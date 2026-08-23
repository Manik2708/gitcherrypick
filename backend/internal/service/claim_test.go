package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
	"github.com/Manik2708/gitcherrypick/backend/internal/service"
	mocks "github.com/Manik2708/gitcherrypick/backend/mocks/port"
)

func TestClaimServiceGet(t *testing.T) {
	t.Run("someone else's claim is NOT FOUND, never forbidden", func(t *testing.T) {
		// A 403 confirms the id exists, and claim ids are otherwise
		// unguessable — the pair (403 on real, 404 on fake) turns the endpoint
		// into an existence oracle over other people's evidence (ADR-0008 §6).
		f := newClaimFixture(t)
		f.claims.EXPECT().ByID(mock.Anything, claimID).
			Return(&domain.Claim{ID: claimID, UserID: "somebody-else"}, nil)

		_, err := f.svc.Get(ctx(t), userID, claimID)
		if !errors.Is(err, service.ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
		if errors.Is(err, service.ErrForbidden) {
			t.Error("a foreign claim must not be distinguishable from a missing one")
		}
	})

	t.Run("a missing claim is the same answer", func(t *testing.T) {
		f := newClaimFixture(t)
		f.claims.EXPECT().ByID(mock.Anything, claimID).Return(nil, port.ErrNotFound)

		_, err := f.svc.Get(ctx(t), userID, claimID)
		if !errors.Is(err, service.ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
	})
}

func TestClaimServiceSubmitPipelineOrder(t *testing.T) {
	t.Run("a structural failure costs no GitHub call", func(t *testing.T) {
		// The whole reason the pipeline is ordered (ADR-0003): a claim that is
		// six PRs long should not cost six round trips to discover it is too
		// long. The GitHub mock has NO expectations, so any call fails the test.
		f := newClaimFixture(t)
		claim := validClaim()
		claim.PREvidence = append(claim.PREvidence, pr(6, "acme", "lib", 106))

		f.expectGet(claim)
		f.users.EXPECT().ByID(mock.Anything, userID).Return(contributor(), nil)
		f.expectStatus(domain.ClaimInvalid)

		_, failures, err := f.svc.Submit(ctx(t), userID, claimID)
		if err != nil {
			t.Fatalf("submitting: %v", err)
		}
		if len(failures) == 0 {
			t.Fatal("expected a structural failure")
		}
	})

	t.Run("a duplicate inside the claim is caught locally", func(t *testing.T) {
		f := newClaimFixture(t)
		claim := validClaim()
		claim.PREvidence = []domain.PREvidence{
			pr(1, "acme", "lib", 101),
			pr(2, "acme", "lib", 101), // the same PR twice
		}

		f.expectGet(claim)
		f.users.EXPECT().ByID(mock.Anything, userID).Return(contributor(), nil)
		f.expectStatus(domain.ClaimInvalid)

		_, failures, err := f.svc.Submit(ctx(t), userID, claimID)
		if err != nil {
			t.Fatalf("submitting: %v", err)
		}
		if len(failures) != 1 {
			t.Fatalf("expected 1 failure, got %d: %+v", len(failures), failures)
		}
		if failures[0].Reason != domain.DuplicateInClaim {
			t.Errorf("expected duplicate_in_claim, got %q", failures[0].Reason)
		}
		if failures[0].Position != 2 {
			t.Errorf("expected the SECOND occurrence named, got position %d", failures[0].Position)
		}
	})

	t.Run("every failure is reported, not just the first", func(t *testing.T) {
		// The contributor is doing curation work; a rejection that names one
		// problem at a time wastes it.
		f := newClaimFixture(t)
		claim := validClaim()
		claim.PREvidence = []domain.PREvidence{
			pr(1, "acme", "lib", 101),
			pr(2, "acme", "lib", 102),
			pr(3, "acme", "lib", 103),
		}
		f.expectGet(claim)
		f.users.EXPECT().ByID(mock.Anything, userID).Return(contributor(), nil)
		f.expectStatus(domain.ClaimInvalid)

		// 101 not merged, 102 authored by someone else, 103 fine.
		f.github.EXPECT().PullRequest(mock.Anything, "acme", "lib", 101).
			Return(&domain.PRFacts{Merged: false, Repository: domain.RepoFacts{Public: true}}, nil)
		f.github.EXPECT().PullRequest(mock.Anything, "acme", "lib", 102).
			Return(&domain.PRFacts{Merged: true, AuthorUserID: 999999,
				Repository: domain.RepoFacts{Public: true}}, nil)
		f.github.EXPECT().PullRequest(mock.Anything, "acme", "lib", 103).
			Return(&domain.PRFacts{Merged: true, AuthorUserID: githubUserID,
				Repository: domain.RepoFacts{Public: true}}, nil)

		_, failures, err := f.svc.Submit(ctx(t), userID, claimID)
		if err != nil {
			t.Fatalf("submitting: %v", err)
		}
		if len(failures) != 2 {
			t.Fatalf("expected both failures, got %d: %+v", len(failures), failures)
		}

		byPosition := map[int]domain.EvidenceInvalidReason{}
		for _, fl := range failures {
			byPosition[fl.Position] = fl.Reason
		}
		if byPosition[1] != domain.NotMerged {
			t.Errorf("position 1: expected not_merged, got %q", byPosition[1])
		}
		if byPosition[2] != domain.NotAuthoredByClaimant {
			t.Errorf("position 2: expected not_authored_by_claimant, got %q", byPosition[2])
		}
		if _, named := byPosition[3]; named {
			t.Error("the valid PR was named as a failure")
		}
	})

	t.Run("a failed validation writes no links and enqueues nothing", func(t *testing.T) {
		// A failed validation must never spend a model call. The broker and the
		// skill repository have no expectations, so any write fails the test.
		f := newClaimFixture(t)
		claim := validClaim()
		claim.Skills = nil // no declared skill

		f.expectGet(claim)
		f.users.EXPECT().ByID(mock.Anything, userID).Return(contributor(), nil)
		f.expectStatus(domain.ClaimInvalid)

		_, failures, err := f.svc.Submit(ctx(t), userID, claimID)
		if err != nil {
			t.Fatalf("submitting: %v", err)
		}
		if len(failures) == 0 {
			t.Fatal("expected a failure")
		}
	})
}

func TestClaimServiceSubmitOutbox(t *testing.T) {
	t.Run("links, status and the job are one transaction", func(t *testing.T) {
		// The outbox property (ADR-0004). A claim submitted with no job would
		// never be judged; a job with no links would spend a model call on
		// evidence whose uniqueness was never checked.
		f := newClaimFixture(t)
		claim := validClaim()

		f.claims.EXPECT().ByID(mock.Anything, claimID).Return(claim, nil).Twice()
		f.users.EXPECT().ByID(mock.Anything, userID).Return(contributor(), nil)
		f.expectMergedByClaimant(claim)

		var order []string
		f.skills.EXPECT().LinkPairs(mock.Anything, mock.Anything, mock.Anything).
			RunAndReturn(func(context.Context, port.Tx, []port.PRLink) error {
				order = append(order, "links")
				return nil
			})
		f.claims.EXPECT().SetStatus(mock.Anything, mock.Anything, claimID, domain.ClaimQueued).
			RunAndReturn(func(context.Context, port.Tx, domain.ClaimID, domain.ClaimStatus) error {
				order = append(order, "status")
				return nil
			})
		f.broker.EXPECT().Publish(mock.Anything, mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, _ port.Tx, m port.Message) error {
				order = append(order, "publish")
				if m.ClaimID != claimID {
					t.Errorf("published the wrong claim: %s", m.ClaimID)
				}
				if m.Version != claim.Version {
					t.Errorf("published version %d, want %d", m.Version, claim.Version)
				}
				return nil
			})

		if _, failures, err := f.svc.Submit(ctx(t), userID, claimID); err != nil || len(failures) > 0 {
			t.Fatalf("expected a clean submit, got %v / %+v", err, failures)
		}

		want := []string{"links", "status", "publish"}
		for i := range want {
			if i >= len(order) || order[i] != want[i] {
				t.Fatalf("expected %v, got %v", want, order)
			}
		}
		if !f.txCommitted {
			t.Error("the submit transaction did not commit")
		}
	})

	t.Run("a pair conflict is reported as a failure, not a crash", func(t *testing.T) {
		// Another claim took the pair between the local check and the write.
		// The contributor gets a named failure rather than a 500.
		f := newClaimFixture(t)
		claim := validClaim()

		f.expectGet(claim)
		f.users.EXPECT().ByID(mock.Anything, userID).Return(contributor(), nil)
		f.expectMergedByClaimant(claim)
		f.skills.EXPECT().LinkPairs(mock.Anything, mock.Anything, mock.Anything).
			Return(port.ErrConflict)

		_, failures, err := f.svc.Submit(ctx(t), userID, claimID)
		if err != nil {
			t.Fatalf("expected a failure list, not an error: %v", err)
		}
		if len(failures) != 1 || failures[0].Reason != domain.DuplicateInClaim {
			t.Errorf("expected a duplicate failure, got %+v", failures)
		}
	})

	t.Run("a locked claim cannot be resubmitted", func(t *testing.T) {
		// ADR-0003's anti-reroll rule.
		f := newClaimFixture(t)
		locked := validClaim()
		until := f.now.Add(3 * 24 * time.Hour)
		locked.LockedUntil = &until

		f.claims.EXPECT().ByID(mock.Anything, claimID).Return(locked, nil)

		_, _, err := f.svc.Submit(ctx(t), userID, claimID)
		if !errors.Is(err, service.ErrConflict) {
			t.Fatalf("expected ErrConflict, got %v", err)
		}
	})
}

func TestClaimServiceReviewRole(t *testing.T) {
	t.Run("a reviewer claim needs a review and NOT authorship", func(t *testing.T) {
		// The role check inverts for pr-review (ADR-0003). Reviewing your own
		// PR is not review work.
		f := newClaimFixture(t)
		claim := validClaim()
		claim.PREvidence = []domain.PREvidence{{
			Position: 1, RepoOwner: "acme", RepoName: "lib", PRNumber: 101,
			Role: domain.RoleReviewer}}

		f.expectGet(claim)
		f.users.EXPECT().ByID(mock.Anything, userID).Return(contributor(), nil)
		f.expectStatus(domain.ClaimInvalid)

		// Merged, and written BY the claimant.
		f.github.EXPECT().PullRequest(mock.Anything, "acme", "lib", 101).
			Return(&domain.PRFacts{Merged: true, AuthorUserID: githubUserID,
				Repository: domain.RepoFacts{Public: true}}, nil)

		_, failures, err := f.svc.Submit(ctx(t), userID, claimID)
		if err != nil {
			t.Fatalf("submitting: %v", err)
		}
		if len(failures) != 1 || failures[0].Reason != domain.AuthoredByClaimant {
			t.Fatalf("expected authored_by_claimant, got %+v", failures)
		}
	})

	t.Run("a reviewer claim with no review by the claimant is refused", func(t *testing.T) {
		f := newClaimFixture(t)
		claim := validClaim()
		claim.PREvidence = []domain.PREvidence{{
			Position: 1, RepoOwner: "acme", RepoName: "lib", PRNumber: 101,
			Role: domain.RoleReviewer}}

		f.expectGet(claim)
		f.users.EXPECT().ByID(mock.Anything, userID).Return(contributor(), nil)
		f.expectStatus(domain.ClaimInvalid)

		f.github.EXPECT().PullRequest(mock.Anything, "acme", "lib", 101).
			Return(&domain.PRFacts{Merged: true, AuthorUserID: 999999,
				Repository: domain.RepoFacts{Public: true}}, nil)
		f.github.EXPECT().Reviews(mock.Anything, "acme", "lib", 101).
			Return([]port.Review{{AuthorUserID: 888888}}, nil)

		_, failures, err := f.svc.Submit(ctx(t), userID, claimID)
		if err != nil {
			t.Fatalf("submitting: %v", err)
		}
		if len(failures) != 1 || failures[0].Reason != domain.NotReviewedByClaimant {
			t.Fatalf("expected not_reviewed_by_claimant, got %+v", failures)
		}
	})
}

func TestClaimServiceWithdraw(t *testing.T) {
	t.Run("warns before demoting, and refuses without confirmation", func(t *testing.T) {
		// The warning is worth nothing if it can be skipped by not asking.
		f := newClaimFixture(t)
		claim := validClaim()

		f.claims.EXPECT().ByID(mock.Anything, claimID).Return(claim, nil)
		f.skills.EXPECT().UserSkills(mock.Anything, userID).Return([]domain.UserSkill{{
			UserID: userID, SkillID: goSkillID, Slug: "go",
			Standing: domain.Primary, DistinctPRCount: 5, Score: 64.2,
		}}, nil)

		_, err := f.svc.Withdraw(ctx(t), userID, claimID, false)
		if !errors.Is(err, service.ErrConflict) {
			t.Fatalf("expected a refusal without confirmation, got %v", err)
		}
	})

	t.Run("a withdrawal that demotes nothing needs no confirmation", func(t *testing.T) {
		// Five PRs, of which this claim carries two: still primary afterwards.
		f := newClaimFixture(t)
		claim := validClaim()
		claim.PREvidence = claim.PREvidence[:2]

		f.claims.EXPECT().ByID(mock.Anything, claimID).Return(claim, nil).Twice()
		f.skills.EXPECT().UserSkills(mock.Anything, userID).Return([]domain.UserSkill{{
			UserID: userID, SkillID: goSkillID, Slug: "go",
			Standing: domain.Primary, DistinctPRCount: 9,
		}}, nil)
		f.claims.EXPECT().SetStatus(mock.Anything, mock.Anything, claimID, domain.ClaimWithdrawn).
			Return(nil)

		if _, err := f.svc.Withdraw(ctx(t), userID, claimID, false); err != nil {
			t.Fatalf("expected no confirmation needed, got %v", err)
		}
	})

	t.Run("the preview names the skill that would demote", func(t *testing.T) {
		f := newClaimFixture(t)
		claim := validClaim()

		f.claims.EXPECT().ByID(mock.Anything, claimID).Return(claim, nil)
		f.skills.EXPECT().UserSkills(mock.Anything, userID).Return([]domain.UserSkill{
			{UserID: userID, SkillID: goSkillID, Slug: "go",
				Standing: domain.Primary, DistinctPRCount: 5},
			{UserID: userID, SkillID: "other-skill", Slug: "kubernetes",
				Standing: domain.Primary, DistinctPRCount: 12},
		}, nil)

		demoting, err := f.svc.WithdrawPreview(ctx(t), userID, claimID)
		if err != nil {
			t.Fatalf("previewing: %v", err)
		}
		if len(demoting) != 1 {
			t.Fatalf("expected exactly 1 demotion, got %d", len(demoting))
		}
		if demoting[0].Slug != "go" {
			t.Errorf("expected 'go', got %q", demoting[0].Slug)
		}
	})
}

// --- fixture -----------------------------------------------------------------

const (
	claimID      = domain.ClaimID("01920000-0000-7000-8000-0000000f0001")
	goSkillID    = domain.SkillID("01920000-0000-7000-8000-00000000e001")
	githubUserID = int64(100001)
)

type claimFixture struct {
	svc    *service.ClaimService
	claims *mocks.ClaimRepository
	skills *mocks.SkillRepository
	users  *mocks.UserRepository
	github *mocks.GitHubClient
	broker *mocks.Broker
	tx     *mocks.TxManager
	clock  *mocks.Clock

	now         time.Time
	txCommitted bool
}

func newClaimFixture(t *testing.T) *claimFixture {
	t.Helper()
	f := &claimFixture{
		claims: mocks.NewClaimRepository(t),
		skills: mocks.NewSkillRepository(t),
		users:  mocks.NewUserRepository(t),
		github: mocks.NewGitHubClient(t),
		broker: mocks.NewBroker(t),
		tx:     mocks.NewTxManager(t),
		clock:  mocks.NewClock(t),
		now:    time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC),
	}

	f.clock.EXPECT().Now().Return(f.now).Maybe()

	// A real InTx: it runs the closure and records whether it would commit, so
	// a test can assert on atomicity rather than on the mock being called.
	f.tx.EXPECT().InTx(mock.Anything, mock.Anything).
		RunAndReturn(func(c context.Context, fn func(context.Context, port.Tx) error) error {
			err := fn(c, stubTx{})
			f.txCommitted = err == nil
			return err
		}).Maybe()

	f.svc = service.NewClaimService(f.claims, f.skills, f.users, f.github, f.broker, f.tx, f.clock)
	return f
}

func (f *claimFixture) expectGet(c *domain.Claim) {
	f.claims.EXPECT().ByID(mock.Anything, claimID).Return(c, nil)
}

func (f *claimFixture) expectStatus(status domain.ClaimStatus) {
	f.claims.EXPECT().SetStatus(mock.Anything, mock.Anything, claimID, status).Return(nil)
}

// expectMergedByClaimant makes every PR in the claim pass the GitHub checks.
func (f *claimFixture) expectMergedByClaimant(c *domain.Claim) {
	for _, e := range c.PREvidence {
		f.github.EXPECT().PullRequest(mock.Anything, e.RepoOwner, e.RepoName, e.PRNumber).
			Return(&domain.PRFacts{
				Merged: true, AuthorUserID: githubUserID,
				Repository: domain.RepoFacts{Public: true},
			}, nil)
	}
}

// stubTx satisfies port.Tx. The transaction's behaviour is the repository
// layer's business; here it only has to be passable.
type stubTx struct{}

func (stubTx) TxHandle() {}

func validClaim() *domain.Claim {
	return &domain.Claim{
		ID: claimID, UserID: userID, Status: domain.ClaimDraft, Version: 1,
		PREvidence: []domain.PREvidence{
			pr(1, "acme", "lib", 101), pr(2, "acme", "lib", 102),
			pr(3, "acme", "platform", 55), pr(4, "acme", "platform", 56),
			pr(5, "indie", "smalllib", 12),
		},
		Skills: []domain.ClaimSkill{{
			SkillID: goSkillID, Slug: "go",
			Origin: domain.UserDeclared, IsNominatedPrimary: true,
		}},
	}
}

func pr(position int, owner, repo string, number int) domain.PREvidence {
	return domain.PREvidence{
		Position: position, RepoOwner: owner, RepoName: repo,
		PRNumber: number, Role: domain.RoleAuthor,
	}
}

func contributor() *domain.Contributor {
	return &domain.Contributor{
		ID: userID, DisplayName: "Alice Okafor",
		GitHubUserID: githubUserID, GitHubLogin: "aliceok",
	}
}
