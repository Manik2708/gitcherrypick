// Package service holds the business logic.
//
// A service talks to repository INTERFACES and does not know which database is
// behind them. It receives a domain.Principal and DECIDES ACCESS — controllers
// never do, because a rule enforced in a handler is a rule enforced in as many
// places as there are handlers (ADR-0002).
//
// Nothing here mentions http.Request, a status code, or JSON. The same service
// must be callable from a worker or a CLI without pretending to be an HTTP
// handler, which is also what lets these tests mock the layer below instead of
// standing up a server.
package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// Sentinel errors the layer above maps onto status codes.
//
// A controller distinguishes cases with errors.Is. It never inspects a
// repository error, which would let storage decide an HTTP response.
var (
	// ErrForbidden is a principal that is authenticated but not permitted.
	ErrForbidden = errors.New("forbidden")

	// ErrNotCapable is the ADR-0002 hiring gate specifically: verified
	// registration is not verified capability, and the two failures read
	// differently to whoever hits them.
	ErrNotCapable = errors.New("hiring capability required")

	// ErrSelf is AssertNotSelf. Surfaced separately from ErrNotFound so a
	// service can decide which of the two a caller is told — search reports
	// nothing at all, a scorecard reports not-found.
	ErrSelf = errors.New("cannot act on your own contributor account")

	// ErrNotFound is the caller asking about something they may not see, or
	// that does not exist. Deliberately the same error for both.
	ErrNotFound = errors.New("not found")

	// ErrConflict is a request that contradicts current state.
	ErrConflict = errors.New("conflict")

	// ErrInvalid is a request that could never be valid.
	ErrInvalid = errors.New("invalid")
)

// AccessService holds the two gates ADR-0002 requires as named, individually
// tested functions.
//
// They are methods rather than inline checks precisely so they can be tested
// once and reused at each of the call sites the ADR names — search, scorecard
// read, and shortlist add.
type AccessService struct {
	hirers port.HirerRepository
}

// NewAccessService wires the gates.
func NewAccessService(hirers port.HirerRepository) *AccessService {
	return &AccessService{hirers: hirers}
}

var _ port.AccessService = (*AccessService)(nil)

// RequireHiringCapability fails unless the hirer is verified AND their
// organization is verified.
//
// The ORGANIZATION is what carries capability (ADR-0002, ADR-0008 §3a).
// Verifying one lifts every seat, and an invited member inherits whatever the
// org already has — so this reads through the organization rather than
// trusting a seat's own column, which could be stale for a seat invited before
// the org was approved.
//
// Payment verification is NOT checked. Missing payment evidence is disclosed
// to the contributor at contact time rather than blocking the org (ADR-0002
// §5); treating it as a gate here would quietly turn a disclosure into a ban.
func (s *AccessService) RequireHiringCapability(ctx context.Context, p domain.Principal) error {
	if p.Kind != domain.KindHirer || p.Hirer == nil {
		return fmt.Errorf("only a hirer may search or shortlist: %w", ErrForbidden)
	}

	org, err := s.hirers.Organization(ctx, p.Hirer.OrganizationID)
	if err != nil {
		if errors.Is(err, port.ErrNotFound) {
			return fmt.Errorf("the seat's organization is missing: %w", ErrNotCapable)
		}
		return fmt.Errorf("reading the organization: %w", err)
	}
	if !org.IsVerified() {
		return fmt.Errorf("organization %s is not verified: %w", org.Slug, ErrNotCapable)
	}
	return nil
}

// AssertNotSelf fails if the target contributor shares a GitHub identity with
// this hirer.
//
// The guard against self-hire. It is a convenience check rather than the real
// defence — verification is what stops someone becoming a hirer at all — but
// it costs one query and removes the most obvious case.
//
// Search applies the same rule as a SQL exclusion rather than calling this,
// so a self-match never reaches a result count. This method is for the reads
// that fetch one contributor by id.
func (s *AccessService) AssertNotSelf(ctx context.Context, hirer domain.HirerID, target domain.UserID) error {
	shares, err := s.hirers.SharesGitHubIdentity(ctx, hirer, target)
	if err != nil {
		return fmt.Errorf("checking for a shared github identity: %w", err)
	}
	if shares {
		return ErrSelf
	}
	return nil
}

// requireAdmin extracts an administrator, or fails.
//
// There is no requireContributor: the contributor-facing services take a
// domain.UserID directly, because extracting it from a principal is the
// controller's job and passing the id makes those services callable from a
// worker with no principal at all.
func requireAdmin(p domain.Principal) (domain.AdminID, error) {
	if p.Kind != domain.KindAdmin || p.Admin == nil {
		return "", fmt.Errorf("only an administrator may do this: %w", ErrForbidden)
	}
	return p.Admin.ID, nil
}

// requireHirer extracts a hirer WITHOUT checking capability.
//
// Separate from RequireHiringCapability because the two failures mean
// different things: "you are not a hirer" and "you are a hirer who cannot yet
// hire". Collapsing them would tell an unverified recruiter they are in the
// wrong account.
func requireHirer(p domain.Principal) (*domain.Hirer, error) {
	if p.Kind != domain.KindHirer || p.Hirer == nil {
		return nil, fmt.Errorf("only a hirer may do this: %w", ErrForbidden)
	}
	return p.Hirer, nil
}
