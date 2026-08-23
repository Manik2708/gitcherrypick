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

// Service tests mock the layer BELOW (CLAUDE.md). No database: these assert
// what the service decides given what a repository returns, which is the
// business logic. What the repository itself returns is settled by the
// integration suite.

func TestRequireHiringCapability(t *testing.T) {
	t.Run("a verified organization grants capability", func(t *testing.T) {
		hirers := mocks.NewHirerRepository(t)
		hirers.EXPECT().Organization(mock.Anything, orgID).Return(verifiedOrg(), nil)

		err := service.NewAccessService(hirers).RequireHiringCapability(ctx(t), hirerPrincipal())
		if err != nil {
			t.Fatalf("expected capability, got %v", err)
		}
	})

	t.Run("an unverified organization does not, however verified the seat", func(t *testing.T) {
		// Capability is a property of the ORGANIZATION (ADR-0002). A seat's own
		// verified_at is not consulted, because it can be stale for a seat
		// invited before the org was approved.
		hirers := mocks.NewHirerRepository(t)
		hirers.EXPECT().Organization(mock.Anything, orgID).Return(unverifiedOrg(), nil)

		p := hirerPrincipal()
		now := time.Now()
		p.Hirer.VerifiedAt = &now // the seat says verified; the org does not

		err := service.NewAccessService(hirers).RequireHiringCapability(ctx(t), p)
		if !errors.Is(err, service.ErrNotCapable) {
			t.Fatalf("expected ErrNotCapable, got %v", err)
		}
	})

	t.Run("payment verification is not a gate", func(t *testing.T) {
		// ADR-0002 §5: missing payment evidence is DISCLOSED to the contributor
		// at contact time. Gating on it here would turn a disclosure into a ban.
		hirers := mocks.NewHirerRepository(t)
		org := verifiedOrg()
		org.PaymentVerifiedAt = nil
		hirers.EXPECT().Organization(mock.Anything, orgID).Return(org, nil)

		if err := service.NewAccessService(hirers).RequireHiringCapability(ctx(t), hirerPrincipal()); err != nil {
			t.Fatalf("payment must not gate capability, got %v", err)
		}
	})

	t.Run("a contributor is forbidden, not merely incapable", func(t *testing.T) {
		// The two failures read differently to whoever hits them: "you are in
		// the wrong account" and "your organization is not verified yet".
		hirers := mocks.NewHirerRepository(t)

		err := service.NewAccessService(hirers).RequireHiringCapability(ctx(t), contributorPrincipal())
		if !errors.Is(err, service.ErrForbidden) {
			t.Fatalf("expected ErrForbidden, got %v", err)
		}
		if errors.Is(err, service.ErrNotCapable) {
			t.Error("a contributor must not be told their organization is unverified")
		}
	})

	t.Run("an admin is forbidden too", func(t *testing.T) {
		hirers := mocks.NewHirerRepository(t)

		err := service.NewAccessService(hirers).RequireHiringCapability(ctx(t), adminPrincipal())
		if !errors.Is(err, service.ErrForbidden) {
			t.Fatalf("expected ErrForbidden, got %v", err)
		}
	})

	t.Run("a missing organization is reported as incapable, not as a crash", func(t *testing.T) {
		hirers := mocks.NewHirerRepository(t)
		hirers.EXPECT().Organization(mock.Anything, orgID).Return(nil, port.ErrNotFound)

		err := service.NewAccessService(hirers).RequireHiringCapability(ctx(t), hirerPrincipal())
		if !errors.Is(err, service.ErrNotCapable) {
			t.Fatalf("expected ErrNotCapable, got %v", err)
		}
	})

	t.Run("a repository failure is propagated, not swallowed as a denial", func(t *testing.T) {
		// A database outage must not read as "this recruiter is unverified" —
		// that is a wrong answer given confidently.
		hirers := mocks.NewHirerRepository(t)
		boom := errors.New("connection refused")
		hirers.EXPECT().Organization(mock.Anything, orgID).Return(nil, boom)

		err := service.NewAccessService(hirers).RequireHiringCapability(ctx(t), hirerPrincipal())
		if !errors.Is(err, boom) {
			t.Fatalf("expected the underlying failure, got %v", err)
		}
		if errors.Is(err, service.ErrNotCapable) {
			t.Error("an outage was reported as a verification failure")
		}
	})
}

func TestAssertNotSelf(t *testing.T) {
	t.Run("a shared identity is refused", func(t *testing.T) {
		hirers := mocks.NewHirerRepository(t)
		hirers.EXPECT().SharesGitHubIdentity(mock.Anything, hirerID, userID).Return(true, nil)

		err := service.NewAccessService(hirers).AssertNotSelf(ctx(t), hirerID, userID)
		if !errors.Is(err, service.ErrSelf) {
			t.Fatalf("expected ErrSelf, got %v", err)
		}
	})

	t.Run("everyone else is unaffected", func(t *testing.T) {
		// The exclusion is identity-scoped, not a general hide.
		hirers := mocks.NewHirerRepository(t)
		hirers.EXPECT().SharesGitHubIdentity(mock.Anything, hirerID, userID).Return(false, nil)

		if err := service.NewAccessService(hirers).AssertNotSelf(ctx(t), hirerID, userID); err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	})

	t.Run("a repository failure is not treated as 'not self'", func(t *testing.T) {
		// Failing open here would let the one case the guard exists for through.
		hirers := mocks.NewHirerRepository(t)
		boom := errors.New("connection refused")
		hirers.EXPECT().SharesGitHubIdentity(mock.Anything, hirerID, userID).Return(false, boom)

		err := service.NewAccessService(hirers).AssertNotSelf(ctx(t), hirerID, userID)
		if err == nil {
			t.Fatal("an outage must not read as 'not self'")
		}
		if !errors.Is(err, boom) {
			t.Errorf("expected the underlying failure, got %v", err)
		}
	})
}

// --- fixtures ----------------------------------------------------------------

const (
	orgID   = domain.OrganizationID("01920000-0000-7000-8000-00000000b001")
	hirerID = domain.HirerID("01920000-0000-7000-8000-00000000c001")
	userID  = domain.UserID("01920000-0000-7000-8000-00000000a001")
	adminID = domain.AdminID("01920000-0000-7000-8000-00000000d001")
)

func ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return c
}

func verifiedOrg() *domain.Organization {
	now := time.Now()
	return &domain.Organization{
		ID: orgID, Name: "Acme Corp", Slug: "acme",
		VerifiedAt: &now, PaymentVerifiedAt: &now,
	}
}

func unverifiedOrg() *domain.Organization {
	return &domain.Organization{ID: orgID, Name: "Unknown Ltd", Slug: "unknown-ltd"}
}

func hirerPrincipal() domain.Principal {
	return domain.Principal{
		Kind:  domain.KindHirer,
		Hirer: &domain.Hirer{ID: hirerID, OrganizationID: orgID, DisplayName: "Hank Rivera"},
	}
}

func contributorPrincipal() domain.Principal {
	return domain.Principal{
		Kind:        domain.KindContributor,
		Contributor: &domain.Contributor{ID: userID, DisplayName: "Alice Okafor"},
	}
}

func adminPrincipal() domain.Principal {
	return domain.Principal{
		Kind:  domain.KindAdmin,
		Admin: &domain.Admin{ID: adminID, DisplayName: "Root Admin"},
	}
}
