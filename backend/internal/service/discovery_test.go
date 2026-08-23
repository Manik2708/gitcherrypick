package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
	"github.com/Manik2708/gitcherrypick/backend/internal/service"
	mocks "github.com/Manik2708/gitcherrypick/backend/mocks/port"
)

func TestDiscoveryServiceGating(t *testing.T) {
	t.Run("a contributor cannot search", func(t *testing.T) {
		f := newDiscoveryFixture(t)

		_, err := f.svc.Search(ctx(t), contributorPrincipal(), domain.SearchQuery{})
		if !errors.Is(err, service.ErrForbidden) {
			t.Fatalf("expected ErrForbidden, got %v", err)
		}
	})

	t.Run("an incapable hirer cannot search", func(t *testing.T) {
		// The two refusals read differently: "wrong account" and "not verified
		// yet". The search repository has no expectations, so reaching it fails.
		f := newDiscoveryFixture(t)
		f.access.EXPECT().RequireHiringCapability(mock.Anything, mock.Anything).
			Return(service.ErrNotCapable)

		_, err := f.svc.Search(ctx(t), hirerPrincipal(), domain.SearchQuery{})
		if !errors.Is(err, service.ErrNotCapable) {
			t.Fatalf("expected ErrNotCapable, got %v", err)
		}
	})

	t.Run("a contributor cannot read a leaderboard", func(t *testing.T) {
		// They see only their own rank (ADR-0005).
		f := newDiscoveryFixture(t)

		_, err := f.svc.Leaderboard(ctx(t), contributorPrincipal(), domain.BoardOverall, "")
		if !errors.Is(err, service.ErrForbidden) {
			t.Fatalf("expected ErrForbidden, got %v", err)
		}
	})
}

func TestDiscoveryServiceFilterValidation(t *testing.T) {
	t.Run("a skill outside the catalogue is refused, not silently empty", func(t *testing.T) {
		// Returning zero results would read as "nobody has this skill" rather
		// than "you typed a skill that does not exist".
		f := newDiscoveryFixture(t)
		f.access.EXPECT().RequireHiringCapability(mock.Anything, mock.Anything).Return(nil)
		f.skills.EXPECT().BySlug(mock.Anything, "cobol").Return(nil, port.ErrNotFound)

		_, err := f.svc.Search(ctx(t), hirerPrincipal(), domain.SearchQuery{Skills: []string{"cobol"}})
		if !errors.Is(err, service.ErrInvalid) {
			t.Fatalf("expected ErrInvalid, got %v", err)
		}
	})

	t.Run("a skill score above 100 is refused", func(t *testing.T) {
		// Skill scores are ceiled at 100 (ADR-0007), so 140 is not a narrow
		// filter — it is an empty one.
		f := newDiscoveryFixture(t)
		f.access.EXPECT().RequireHiringCapability(mock.Anything, mock.Anything).Return(nil)

		_, err := f.svc.Search(ctx(t), hirerPrincipal(),
			domain.SearchQuery{MinSkillScore: fl(140)})
		if !errors.Is(err, service.ErrInvalid) {
			t.Fatalf("expected ErrInvalid, got %v", err)
		}
	})

	t.Run("a generalist score above 100 is allowed", func(t *testing.T) {
		// The breadth score is deliberately unbounded (ADR-0007 §5).
		f := newDiscoveryFixture(t)
		f.access.EXPECT().RequireHiringCapability(mock.Anything, mock.Anything).Return(nil)
		f.search.EXPECT().Search(mock.Anything, hirerID, mock.Anything).
			Return(&domain.SearchResults{RankedBy: domain.RankedByGeneralist}, nil)

		if _, err := f.svc.Search(ctx(t), hirerPrincipal(),
			domain.SearchQuery{MinGeneralistScore: fl(240)}); err != nil {
			t.Fatalf("the generalist score has no ceiling, got %v", err)
		}
	})
}

func TestDiscoveryServiceScorecard(t *testing.T) {
	t.Run("self-exclusion surfaces as NOT FOUND", func(t *testing.T) {
		// A distinguishable response would confirm the identity link, which is
		// the one thing the exclusion exists to keep quiet.
		f := newDiscoveryFixture(t)
		f.access.EXPECT().RequireHiringCapability(mock.Anything, mock.Anything).Return(nil)
		f.access.EXPECT().AssertNotSelf(mock.Anything, hirerID, userID).Return(service.ErrSelf)

		_, err := f.svc.Scorecard(ctx(t), hirerPrincipal(), userID)
		if !errors.Is(err, service.ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
		if errors.Is(err, service.ErrSelf) {
			t.Error("the identity link must not be confirmed to the caller")
		}
	})
}

func TestDiscoveryServiceSavedSearches(t *testing.T) {
	t.Run("replaying evaluates the filters AS THE CALLER", func(t *testing.T) {
		// The gates are properties of the requester, not of the saved query
		// (ADR-0008 §5). Search is called with the REPLAYING hirer's id.
		f := newDiscoveryFixture(t)
		f.access.EXPECT().RequireHiringCapability(mock.Anything, mock.Anything).Return(nil).Twice()
		f.saved.EXPECT().ByID(mock.Anything, savedID).Return(&domain.SavedSearch{
			ID: savedID, OrganizationID: orgID, Name: "Go",
			Filters: domain.SearchQuery{Skills: []string{"go"}},
		}, nil)
		f.skills.EXPECT().BySlug(mock.Anything, "go").
			Return(&domain.Skill{ID: goSkillID, Slug: "go"}, nil)
		f.search.EXPECT().Search(mock.Anything, hirerID, mock.Anything).
			Return(&domain.SearchResults{}, nil)

		if _, err := f.svc.ReplaySavedSearch(ctx(t), hirerPrincipal(), savedID); err != nil {
			t.Fatalf("replaying: %v", err)
		}
	})

	t.Run("another organization's saved search is NOT FOUND", func(t *testing.T) {
		// A competitor must not be able to confirm that an id belongs to
		// somebody.
		f := newDiscoveryFixture(t)
		f.access.EXPECT().RequireHiringCapability(mock.Anything, mock.Anything).Return(nil)
		f.saved.EXPECT().ByID(mock.Anything, savedID).Return(&domain.SavedSearch{
			ID: savedID, OrganizationID: "another-org",
		}, nil)

		_, err := f.svc.ReplaySavedSearch(ctx(t), hirerPrincipal(), savedID)
		if !errors.Is(err, service.ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("saving validates, so a stored search cannot fail on replay", func(t *testing.T) {
		f := newDiscoveryFixture(t)
		f.access.EXPECT().RequireHiringCapability(mock.Anything, mock.Anything).Return(nil)
		f.skills.EXPECT().BySlug(mock.Anything, "cobol").Return(nil, port.ErrNotFound)

		_, err := f.svc.SaveSearch(ctx(t), hirerPrincipal(), "Bad",
			domain.SearchQuery{Skills: []string{"cobol"}})
		if !errors.Is(err, service.ErrInvalid) {
			t.Fatalf("expected ErrInvalid, got %v", err)
		}
	})
}

func TestAdminServiceDecisions(t *testing.T) {
	t.Run("only an admin may drain a queue", func(t *testing.T) {
		f := newAdminFixture(t)

		if _, err := f.svc.PendingVerifications(ctx(t), hirerPrincipal()); !errors.Is(err, service.ErrForbidden) {
			t.Errorf("a hirer drained the verification queue: %v", err)
		}
		if _, err := f.svc.PendingReevaluations(ctx(t), contributorPrincipal()); !errors.Is(err, service.ErrForbidden) {
			t.Errorf("a contributor drained the dispute queue: %v", err)
		}
	})

	t.Run("a rejection must carry a reason", func(t *testing.T) {
		// 'No' without a reason produces a resubmission of the same thing.
		f := newAdminFixture(t)

		err := f.svc.DecideVerification(ctx(t), adminPrincipal(), requestID, false, "")
		if !errors.Is(err, service.ErrInvalid) {
			t.Fatalf("expected ErrInvalid, got %v", err)
		}
	})

	t.Run("an accepted dispute re-queues the claim in the same transaction", func(t *testing.T) {
		// A dispute marked accepted with no job would leave the contributor
		// told they were right and nothing happening.
		f := newAdminFixture(t)
		f.reevals.EXPECT().Pending(mock.Anything).Return([]domain.ReevaluationRequest{{
			ID: requestID, ClaimID: claimID, UserID: userID, Status: domain.ReevalPending,
		}}, nil)
		f.reevals.EXPECT().Decide(mock.Anything, mock.Anything, requestID, adminID, true, mock.Anything).
			Return(nil)
		f.claims.EXPECT().ByID(mock.Anything, claimID).
			Return(&domain.Claim{ID: claimID, Version: 3}, nil)
		f.claims.EXPECT().SetStatus(mock.Anything, mock.Anything, claimID, domain.ClaimQueued).
			Return(nil)

		var published port.Message
		f.broker.EXPECT().Publish(mock.Anything, mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, _ port.Tx, m port.Message) error {
				published = m
				return nil
			})

		if err := f.svc.DecideReevaluation(ctx(t), adminPrincipal(), requestID, true, "Fair point."); err != nil {
			t.Fatalf("deciding: %v", err)
		}
		if published.ClaimID != claimID {
			t.Errorf("re-queued the wrong claim: %s", published.ClaimID)
		}
		if published.Version != 3 {
			t.Errorf("re-queued version %d, want the claim's current 3", published.Version)
		}
	})

	t.Run("a rejected dispute re-queues nothing", func(t *testing.T) {
		// The judgement stands; only the cooldown moves. The broker has no
		// expectations, so a publish fails the test.
		f := newAdminFixture(t)
		f.reevals.EXPECT().Pending(mock.Anything).Return([]domain.ReevaluationRequest{{
			ID: requestID, ClaimID: claimID, UserID: userID, Status: domain.ReevalPending,
		}}, nil)
		f.reevals.EXPECT().Decide(mock.Anything, mock.Anything, requestID, adminID, false, mock.Anything).
			Return(nil)

		if err := f.svc.DecideReevaluation(ctx(t), adminPrincipal(), requestID, false,
			"The renewal bound is a constant change."); err != nil {
			t.Fatalf("deciding: %v", err)
		}
	})

	t.Run("approving a skill creates it and closes the request together", func(t *testing.T) {
		f := newAdminFixture(t)
		created := &domain.Skill{ID: "new-skill", Slug: "webassembly", Name: "WebAssembly"}

		f.skills.EXPECT().Create(mock.Anything, mock.Anything, mock.Anything).Return(created, nil)
		f.skills.EXPECT().DecideRequest(mock.Anything, mock.Anything, requestID, adminID, true, "", created).
			Return(nil)

		got, err := f.svc.DecideSkillRequest(ctx(t), adminPrincipal(), requestID, true,
			&domain.Skill{Slug: "webassembly", Name: "WebAssembly"}, "")
		if err != nil {
			t.Fatalf("approving: %v", err)
		}
		if got.Slug != "webassembly" {
			t.Errorf("expected the created skill returned, got %+v", got)
		}
	})

	t.Run("a slug collision is a conflict the admin sees, not a crash", func(t *testing.T) {
		// A bad slug fragments a population forever, so the admin has to be
		// told rather than shown a 500.
		f := newAdminFixture(t)
		f.skills.EXPECT().Create(mock.Anything, mock.Anything, mock.Anything).
			Return(nil, port.ErrConflict)

		_, err := f.svc.DecideSkillRequest(ctx(t), adminPrincipal(), requestID, true,
			&domain.Skill{Slug: "go", Name: "Go Again"}, "")
		if !errors.Is(err, service.ErrConflict) {
			t.Fatalf("expected ErrConflict, got %v", err)
		}
	})
}

// --- fixtures ----------------------------------------------------------------

const (
	savedID   = domain.SavedSearchID("01920000-0000-7000-8000-000000005001")
	requestID = domain.RequestID("01920000-0000-7000-8000-000000006001")
)

type discoveryFixture struct {
	svc    *service.DiscoveryService
	search *mocks.SearchRepository
	skills *mocks.SkillRepository
	saved  *mocks.SavedSearchRepository
	access *mocks.AccessService
}

func newDiscoveryFixture(t *testing.T) *discoveryFixture {
	t.Helper()
	f := &discoveryFixture{
		search: mocks.NewSearchRepository(t),
		skills: mocks.NewSkillRepository(t),
		saved:  mocks.NewSavedSearchRepository(t),
		access: mocks.NewAccessService(t),
	}
	f.svc = service.NewDiscoveryService(f.search, f.skills, f.saved, f.access)
	return f
}

type adminFixture struct {
	svc     *service.AdminService
	admins  *mocks.AdminRepository
	skills  *mocks.SkillRepository
	orgs    *mocks.OrganizationRepository
	reevals *mocks.ReevaluationRepository
	claims  *mocks.ClaimRepository
	broker  *mocks.Broker
	tx      *mocks.TxManager
}

func newAdminFixture(t *testing.T) *adminFixture {
	t.Helper()
	f := &adminFixture{
		admins:  mocks.NewAdminRepository(t),
		skills:  mocks.NewSkillRepository(t),
		orgs:    mocks.NewOrganizationRepository(t),
		reevals: mocks.NewReevaluationRepository(t),
		claims:  mocks.NewClaimRepository(t),
		broker:  mocks.NewBroker(t),
		tx:      mocks.NewTxManager(t),
	}
	f.tx.EXPECT().InTx(mock.Anything, mock.Anything).
		RunAndReturn(func(c context.Context, fn func(context.Context, port.Tx) error) error {
			return fn(c, stubTx{})
		}).Maybe()

	f.svc = service.NewAdminService(f.admins, f.skills, f.orgs, f.reevals, f.claims, f.broker, f.tx)
	return f
}

func fl(v float64) *float64 { return &v }
