package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// ContactService is the contributor's side of consent.
//
// Accepting releases an email; declining does not. The asymmetry with the
// hirer's view is the design (ADR-0005): the contributor sees the organization,
// the promised date and the payment disclosure, while the hirer sees a status
// and — only after acceptance — an address.
type ContactService struct {
	contacts port.ContactRepository

	// roles hydrates the job each request is about (ADR-0019 §2). The details
	// travel WITH the request rather than through a separate lookup a client
	// might skip, because an invitation somebody cannot evaluate is one they
	// have to decline on suspicion.
	roles port.RoleRepository

	users    port.UserRepository
	hirers   port.HirerRepository
	notifier port.Notifier
	tx       port.TxManager
	clock    port.Clock
}

// NewContactService wires the consent flow.
func NewContactService(
	contacts port.ContactRepository,
	roles port.RoleRepository,
	users port.UserRepository,
	hirers port.HirerRepository,
	notifier port.Notifier,
	tx port.TxManager,
	clock port.Clock,
) *ContactService {
	return &ContactService{contacts: contacts, roles: roles, users: users,
		hirers: hirers, notifier: notifier, tx: tx, clock: clock}
}

var _ port.ContactService = (*ContactService)(nil)

// List reads the contributor's own requests.
func (s *ContactService) List(ctx context.Context, id domain.UserID, status *domain.ContactRequestStatus) ([]domain.ContactRequest, error) {
	out, err := s.contacts.ListForUser(ctx, id, status)
	if err != nil {
		return nil, fmt.Errorf("listing contact requests: %w", err)
	}
	if err := s.withRoles(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

// withRoles attaches the job each request is about.
//
// Cached by id across the list, because a company running one round against
// forty people produces forty requests pointing at one role — and reading it
// forty times would be forty round trips for one answer.
//
// A role that cannot be read is left NIL rather than failing the list. The
// request is still real and still answerable, and refusing to show somebody
// their invitations because one company's role row is unreadable would be
// punishing the wrong person.
func (s *ContactService) withRoles(ctx context.Context, requests []domain.ContactRequest) error {
	seen := map[domain.RoleID]*domain.Role{}
	for i := range requests {
		id := requests[i].RoleID
		if id == "" {
			continue
		}
		if role, ok := seen[id]; ok {
			requests[i].Role = role
			continue
		}
		role, err := s.roles.ByID(ctx, id)
		if err != nil {
			if errors.Is(err, port.ErrNotFound) {
				seen[id] = nil
				continue
			}
			return fmt.Errorf("reading role %s: %w", id, err)
		}
		seen[id] = role
		requests[i].Role = role
	}
	return nil
}

// Respond records the contributor's answer.
//
// On acceptance it ALSO refreshes their availability window. Answering yes to
// a hiring approach is a clearer statement of availability than the button they
// forgot to click (ADR-0008 §4), and a contributor who accepts while lapsed
// would otherwise stay hidden from the very search that found them.
//
// The email is sent AFTER commit. An email cannot be rolled back, so sending
// one inside a transaction that then fails tells a hirer something that did not
// happen.
func (s *ContactService) Respond(ctx context.Context, id domain.UserID, contact domain.ContactID, accept bool) (*domain.ContactRequest, error) {
	existing, err := s.contacts.ByID(ctx, contact)
	if err != nil {
		if errors.Is(err, port.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("reading contact request: %w", err)
	}
	// Somebody else's request is not found, never forbidden.
	if existing.UserID != id {
		return nil, ErrNotFound
	}

	var out *domain.ContactRequest
	err = s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		var err error
		out, err = s.contacts.Respond(ctx, tx, contact, accept, s.clock.Now())
		return err
	})
	if err != nil {
		if errors.Is(err, port.ErrNotFound) {
			// The UPDATE matched no row, which means it was already answered.
			return nil, fmt.Errorf("this request has already been answered: %w", ErrConflict)
		}
		return nil, fmt.Errorf("responding: %w", err)
	}

	if accept {
		if _, err := s.users.SetAvailability(ctx, id, domain.Looking); err != nil {
			// Not fatal: the answer is recorded and the email is released. A
			// stale availability window is a smaller problem than telling a
			// contributor their acceptance failed when it did not.
			_ = err
		}
		s.notify(ctx, out)
	}
	return out, nil
}

// notify tells the hirer their approach was accepted.
//
// Failures are swallowed deliberately. The consent is committed; a notifier
// outage must not undo it, and the hirer sees the acceptance on their
// shortlist regardless.
func (s *ContactService) notify(ctx context.Context, cr *domain.ContactRequest) {
	if cr == nil {
		return
	}
	hirer, err := s.hirers.ByID(ctx, cr.RequestedBy.ID)
	if err != nil {
		return
	}
	_ = s.notifier.Send(ctx, port.Notification{
		Kind:      port.NotifyContactAccepted,
		Recipient: hirer.Email,
		Data: map[string]any{
			"contact_request_id": string(cr.ID),
			"shortlist_id":       string(cr.ShortlistID),
		},
	})
}
