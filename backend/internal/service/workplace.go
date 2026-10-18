package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// WorkplaceService owns an organisation's kind and its addresses (ADR-0024).
//
// The kind changes in both directions and deliberately unequally: losing an
// office is an owner's act, gaining one is an administrator's decision. Only an
// organisation's FIRST address is ever reviewed by a person; every later one is
// an owner's to add.
type WorkplaceService struct {
	orgs     port.OrganizationRepository
	roles    port.RoleRepository
	requests port.OfficeRequestRepository
	hirers   port.HirerRepository
	notifier port.Notifier

	// places names the country in the approval email, failing open to the
	// code (ADR-0024 amendment 1 §1.10).
	places port.PlaceService

	tx    port.TxManager
	clock port.Clock
}

// NewWorkplaceService wires addresses and office requests.
func NewWorkplaceService(
	orgs port.OrganizationRepository,
	roles port.RoleRepository,
	requests port.OfficeRequestRepository,
	hirers port.HirerRepository,
	notifier port.Notifier,
	places port.PlaceService,
	tx port.TxManager,
	clock port.Clock,
) *WorkplaceService {
	return &WorkplaceService{orgs: orgs, roles: roles, requests: requests,
		hirers: hirers, notifier: notifier, places: places, tx: tx, clock: clock}
}

var _ port.WorkplaceService = (*WorkplaceService)(nil)

// seatIn resolves the caller as a hirer of this organisation, and as an owner
// when ownerOnly. Both refusals are ErrForbidden, which the controller answers
// as the roster does (ADR-0024 decision 8).
func seatIn(p domain.Principal, org domain.OrganizationID, ownerOnly bool) (*domain.Hirer, error) {
	hirer, err := requireHirer(p)
	if err != nil {
		return nil, err
	}
	if hirer.OrganizationID != org {
		return nil, fmt.Errorf("you are not a member of that organization: %w", ErrForbidden)
	}
	if ownerOnly && hirer.OrgRole != domain.RoleOwner {
		return nil, fmt.Errorf("only an owner may change the organisation's addresses: %w", ErrForbidden)
	}
	return hirer, nil
}

// Addresses is the envelope every seat reads.
//
// It is where a client learns the organisation's kind: the role form already
// reads it for the onsite picker. The latest office request rides along while
// the organisation is fully remote and the request is not approved, so every
// seat sees one is waiting — or why it was refused (amendment 1 §1.3).
func (s *WorkplaceService) Addresses(ctx context.Context, p domain.Principal, org domain.OrganizationID) (*port.WorkplaceView, error) {
	if _, err := seatIn(p, org, false); err != nil {
		return nil, err
	}
	return s.view(ctx, org)
}

// view reads the envelope, committed state.
func (s *WorkplaceService) view(ctx context.Context, org domain.OrganizationID) (*port.WorkplaceView, error) {
	workplace, err := s.orgs.Workplace(ctx, nil, org)
	if err != nil {
		return nil, fmt.Errorf("reading the kind of organization %s: %w", org, err)
	}
	addresses, err := s.orgs.Addresses(ctx, org)
	if err != nil {
		return nil, fmt.Errorf("reading the addresses of organization %s: %w", org, err)
	}
	out := &port.WorkplaceView{Workplace: workplace, Addresses: addresses}

	if workplace != domain.WorkplaceFullyRemote {
		return out, nil
	}
	latest, err := s.requests.Latest(ctx, org)
	switch {
	case errors.Is(err, port.ErrNotFound):
		return out, nil
	case err != nil:
		return nil, fmt.Errorf("reading the office request of organization %s: %w", org, err)
	}
	if latest.Status() != domain.OfficeApproved {
		out.OfficeRequest = latest
	}
	return out, nil
}

// normaliseAddress checks an address body, naming the top-level field it
// refuses: the address IS the body here (amendment 1 §1.8).
func normaliseAddress(a *domain.Address) error {
	if field := addressProblem(&a.Country, a.City, a.Street1); field != "" {
		return invalidAddress(field, "an address needs a country, a city and a street")
	}
	a.City = strings.TrimSpace(a.City)
	a.Street1 = strings.TrimSpace(a.Street1)
	a.Street2 = strings.TrimSpace(a.Street2)
	a.PostalCode = strings.TrimSpace(a.PostalCode)
	return nil
}

// AddAddress adds a further address, live at once and unreviewed.
//
// Owners only — even where role_create_authority lets a member open roles.
// A fully remote organisation adds none: its first address is an office
// request, reviewed by a person (decisions 10, 12).
func (s *WorkplaceService) AddAddress(ctx context.Context, p domain.Principal, org domain.OrganizationID, a domain.Address) (*domain.Address, error) {
	owner, err := seatIn(p, org, true)
	if err != nil {
		return nil, err
	}
	if err := normaliseAddress(&a); err != nil {
		return nil, err
	}
	a.OrganizationID, a.IsMainOffice = org, false
	a.AddedBy = &owner.ID

	var out *domain.Address
	err = s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		workplace, err := s.orgs.LockWorkplace(ctx, tx, org)
		if err != nil {
			return err
		}
		if workplace == domain.WorkplaceFullyRemote {
			return Coded(ErrConflict, CodeOrganizationFullyRemote,
				"a fully remote organisation adds no address — ask for an office instead")
		}
		out, err = s.orgs.AddAddress(ctx, tx, &a)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("adding an address: %w", err)
	}
	return out, nil
}

// RemoveAddress stamps an address removed.
//
// Never the main office — it goes only with every other address, when the
// organisation goes fully remote — and never while a draft or open role uses
// it. A closed role at it is no obstacle: it still reads the removed row.
func (s *WorkplaceService) RemoveAddress(ctx context.Context, p domain.Principal, org domain.OrganizationID, id domain.AddressID) error {
	owner, err := seatIn(p, org, true)
	if err != nil {
		return err
	}

	err = s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		if _, err := s.orgs.LockWorkplace(ctx, tx, org); err != nil {
			return err
		}
		all, err := s.orgs.AllAddresses(ctx, tx, org)
		if err != nil {
			return err
		}
		var target *domain.Address
		for i := range all {
			if all[i].ID == id && !all[i].Removed() {
				target = &all[i]
			}
		}
		// Another organisation's address and one that does not exist answer
		// identically, so a guess confirms nothing.
		if target == nil {
			return Coded(ErrNotFound, CodeAddressNotFound, "no such address in this organisation")
		}
		if target.IsMainOffice {
			return Coded(ErrConflict, CodeMainOfficeNotRemovable,
				"the main office is removed only by going fully remote")
		}
		blocking, err := s.roles.ActiveOnsiteRoles(ctx, tx, org, &id)
		if err != nil {
			return err
		}
		if len(blocking) > 0 {
			return rolesIn(CodeAddressInUse,
				"a draft or open role uses this address — close or revise it first", blocking)
		}
		return s.orgs.RemoveAddress(ctx, tx, id, owner.ID, s.clock.Now())
	})
	if err != nil {
		return fmt.Errorf("removing address %s: %w", id, err)
	}
	return nil
}

// RequestOffice submits a fully remote organisation's first address.
//
// Nothing changes until an administrator approves: the organisation stays
// fully remote, adds no address and posts no onsite entry meanwhile. A pending
// request cannot be withdrawn or changed (decision 14).
func (s *WorkplaceService) RequestOffice(ctx context.Context, p domain.Principal, org domain.OrganizationID, a domain.Address) (*domain.OfficeRequest, error) {
	owner, err := seatIn(p, org, true)
	if err != nil {
		return nil, err
	}
	if err := normaliseAddress(&a); err != nil {
		return nil, err
	}

	var out *domain.OfficeRequest
	err = s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		workplace, err := s.orgs.LockWorkplace(ctx, tx, org)
		if err != nil {
			return err
		}
		if workplace == domain.WorkplaceOffice {
			return Coded(ErrConflict, CodeOrganizationHasOffice,
				"an organisation with an office adds further addresses itself")
		}
		out, err = s.requests.Submit(ctx, tx, &domain.OfficeRequest{
			OrganizationID: org,
			Country:        a.Country, City: a.City, PostalCode: a.PostalCode,
			Street1: a.Street1, Street2: a.Street2,
			RequestedBy: domain.RefTo(owner),
		})
		if errors.Is(err, port.ErrConflict) {
			return Coded(ErrConflict, CodeOfficeRequestPending,
				"a request is already waiting for an administrator")
		}
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("requesting an office: %w", err)
	}
	return out, nil
}

// GoFullyRemote removes every address and makes the organisation fully remote.
//
// Owners only, not reviewed, and emails nobody: it takes capability away
// rather than granting any. Refused while any draft or open role has an onsite
// entry, naming them (decision 15).
func (s *WorkplaceService) GoFullyRemote(ctx context.Context, p domain.Principal, org domain.OrganizationID) (*port.WorkplaceView, error) {
	owner, err := seatIn(p, org, true)
	if err != nil {
		return nil, err
	}

	err = s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		workplace, err := s.orgs.LockWorkplace(ctx, tx, org)
		if err != nil {
			return err
		}
		if workplace == domain.WorkplaceFullyRemote {
			return Coded(ErrConflict, CodeOrganizationFullyRemote,
				"the organisation is already fully remote")
		}
		blocking, err := s.roles.ActiveOnsiteRoles(ctx, tx, org, nil)
		if err != nil {
			return err
		}
		if len(blocking) > 0 {
			return rolesIn(CodeOnsiteRolesExist,
				"draft or open roles are onsite — close or revise them first", blocking)
		}
		return s.orgs.GoFullyRemote(ctx, tx, org, owner.ID, s.clock.Now())
	})
	if err != nil {
		return nil, fmt.Errorf("going fully remote: %w", err)
	}
	return s.view(ctx, org)
}

// OfficeRequests is the administrator's queue.
func (s *WorkplaceService) OfficeRequests(ctx context.Context, p domain.Principal) ([]domain.OfficeRequest, error) {
	if _, err := requireAdmin(p); err != nil {
		return nil, err
	}
	out, err := s.requests.Queue(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading the office request queue: %w", err)
	}
	return out, nil
}

// DecideOfficeRequest approves or rejects one.
//
// Approval, in one transaction under the organisation's lock: the address
// becomes the main office, the organisation becomes an office one, the
// request is stamped. A rejection stamps the request and changes nothing
// else; any owner may ask again at once. The requesting owner is emailed
// either way (decisions 13–14).
func (s *WorkplaceService) DecideOfficeRequest(ctx context.Context, p domain.Principal, id domain.OfficeRequestID, approve bool, reason string) (*port.OfficeDecision, error) {
	adminID, err := requireAdmin(p)
	if err != nil {
		return nil, err
	}
	reason = strings.TrimSpace(reason)
	if !approve && reason == "" {
		return nil, Coded(ErrInvalid, CodeReasonRequired, "a rejection must carry a reason")
	}

	now := s.clock.Now()
	var request *domain.OfficeRequest
	err = s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		var err error
		request, err = s.requests.ByID(ctx, tx, id)
		if err != nil {
			if errors.Is(err, port.ErrNotFound) {
				return Coded(ErrNotFound, CodeNotFound, "no such office request")
			}
			return err
		}
		if request.Status() != domain.OfficePending {
			return Coded(ErrConflict, CodeRequestAlreadyDecided,
				"this request has already been decided").
				WithDetail(map[string]any{"status": string(request.Status())})
		}
		if !approve {
			return s.requests.Reject(ctx, tx, id, adminID, reason, now)
		}

		workplace, err := s.orgs.LockWorkplace(ctx, tx, request.OrganizationID)
		if err != nil {
			return err
		}
		if workplace == domain.WorkplaceOffice {
			// Unreachable while the invariants hold: a pending request exists
			// only for a fully remote organisation. Refused rather than
			// writing a second live main office.
			return Coded(ErrConflict, CodeOrganizationHasOffice,
				"the organisation already has an office")
		}
		_, err = s.requests.Approve(ctx, tx, id, adminID, now)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("deciding office request %s: %w", id, err)
	}

	s.notifyDecision(ctx, request, approve, reason)

	status := domain.OfficeRejected
	if approve {
		status = domain.OfficeApproved
	}
	return &port.OfficeDecision{ID: id, Status: status, ReviewedAt: now, ReviewedBy: adminID}, nil
}

// notifyDecision emails the owner who asked.
//
// After commit, and a failure is swallowed: the decision has landed, and an
// undelivered notice about it is not a reason to undo an administrator's work.
func (s *WorkplaceService) notifyDecision(ctx context.Context, r *domain.OfficeRequest, approved bool, reason string) {
	requester, err := s.hirers.ByID(ctx, r.RequestedBy.ID)
	if err != nil || requester == nil || requester.Email == "" {
		return
	}
	data := map[string]any{"organization": r.OrganizationName, "city": r.City}
	kind := port.NotifyOfficeRequestApproved
	if approved {
		// The country's NAME, falling back to the code only if the place
		// service fails — the contact email's rule.
		data["country"] = countryNamer(ctx, s.places)(r.Country)
	} else {
		kind = port.NotifyOfficeRequestRejected
		data["reason"] = reason
	}
	_ = s.notifier.Send(ctx, port.Notification{Kind: kind, Recipient: requester.Email, Data: data})
}
