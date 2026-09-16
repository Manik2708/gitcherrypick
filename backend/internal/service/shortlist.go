package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// ShortlistService owns hiring rounds and the two-phase disclosure.
//
// Staging is private; confirm is irreversible (ADR-0008 §3a). The rule it
// serves is that a shortlisted contributor can never be un-shortlisted, and a
// rule that strict is only tolerable if there is a moment before it binds.
type ShortlistService struct {
	// roles resolves the job a round is for. A round with no role could only
	// tell a contributor that somebody is interested in them (ADR-0019 §3).
	roles port.RoleRepository

	shortlists port.ShortlistRepository
	contacts   port.ContactRepository
	users      port.UserRepository
	hirers     port.HirerRepository
	notifier   port.Notifier
	access     port.AccessService
	tx         port.TxManager
	clock      port.Clock
}

// NewShortlistService wires hiring rounds.
func NewShortlistService(
	shortlists port.ShortlistRepository,
	roles port.RoleRepository,
	contacts port.ContactRepository,
	users port.UserRepository,
	hirers port.HirerRepository,
	notifier port.Notifier,
	access port.AccessService,
	tx port.TxManager,
	clock port.Clock,
) *ShortlistService {
	return &ShortlistService{shortlists: shortlists, roles: roles,
		contacts: contacts, users: users, hirers: hirers, notifier: notifier,
		access: access, tx: tx, clock: clock}
}

var _ port.ShortlistService = (*ShortlistService)(nil)

// Create opens a draft round.
//
// tentative_result_date is required and must be in the future: it is what the
// contributor is told, and the overdue ratio is computed against it. A date
// already past would be born overdue (ADR-0005).
func (s *ShortlistService) Create(ctx context.Context, p domain.Principal, role domain.RoleID, name, description string, date time.Time) (*domain.Shortlist, error) {
	hirer, err := s.capable(ctx, p)
	if err != nil {
		return nil, err
	}

	// The round is FOR a job (ADR-0019 §3), and the job has to be one of this
	// organisation's and actually open. An unopened role would put a salary
	// nobody approved in front of everybody contacted; another company's role
	// would put their salary there.
	opening, err := s.roles.ByID(ctx, role)
	if err != nil {
		if errors.Is(err, port.ErrNotFound) {
			return nil, Coded(ErrInvalid, CodeRoleNotFound,
				"that role does not exist")
		}
		return nil, fmt.Errorf("reading the role: %w", err)
	}
	if opening.OrgID != hirer.OrganizationID {
		// Not found rather than forbidden: whether another organisation has a
		// role with this id is not this caller's business.
		return nil, Coded(ErrInvalid, CodeRoleNotFound, "that role does not exist")
	}
	if !opening.IsOpen() {
		return nil, Coded(ErrInvalid, CodeInvalidShortlist,
			"that role is not open — a round can only approach people for a job "+
				"the organisation has actually committed to")
	}

	if name == "" {
		return nil, fmt.Errorf("a round needs a name: %w", ErrInvalid)
	}
	if date.IsZero() {
		return nil, fmt.Errorf("a round needs a tentative result date: %w", ErrInvalid)
	}
	if !date.After(s.clock.Now()) {
		return nil, fmt.Errorf("the tentative result date must be in the future: %w", ErrInvalid)
	}

	created, err := s.shortlists.Create(ctx, &domain.Shortlist{
		OrganizationID: hirer.OrganizationID, RoleID: role,
		Name: name, Description: description,
		TentativeResultDate: date, CreatedBy: domain.RefTo(hirer),
	})
	if err != nil {
		return nil, fmt.Errorf("creating the round: %w", err)
	}
	return created, nil
}

// List reads the organization's rounds.
func (s *ShortlistService) List(ctx context.Context, p domain.Principal, status *domain.ShortlistStatus) ([]domain.Shortlist, error) {
	hirer, err := s.capable(ctx, p)
	if err != nil {
		return nil, err
	}
	out, err := s.shortlists.ListByOrganization(ctx, hirer.OrganizationID, status)
	if err != nil {
		return nil, fmt.Errorf("listing rounds: %w", err)
	}
	return out, nil
}

// Get reads one the caller's organization owns.
func (s *ShortlistService) Get(ctx context.Context, p domain.Principal, id domain.ShortlistID) (*domain.Shortlist, error) {
	hirer, err := s.capable(ctx, p)
	if err != nil {
		return nil, err
	}
	return s.owned(ctx, hirer, id)
}

// Update edits a round's presentation and its promised date.
//
// Editing the date does NOT rewrite contact requests already sent: those carry
// their own copy, so the record of what a contributor was told survives.
func (s *ShortlistService) Update(ctx context.Context, p domain.Principal, id domain.ShortlistID, name, description *string, date *time.Time) (*domain.Shortlist, error) {
	hirer, err := s.capable(ctx, p)
	if err != nil {
		return nil, err
	}
	if _, err := s.owned(ctx, hirer, id); err != nil {
		return nil, err
	}
	if date != nil && !date.After(s.clock.Now()) {
		return nil, fmt.Errorf("the tentative result date must be in the future: %w", ErrInvalid)
	}

	updated, err := s.shortlists.Update(ctx, id, name, description, date)
	if err != nil {
		if errors.Is(err, port.ErrNotFound) {
			return nil, Coded(ErrConflict, CodeShortlistClosed, "a closed round cannot be edited")
		}
		return nil, fmt.Errorf("updating the round: %w", err)
	}
	return updated, nil
}

// Close ends a round.
func (s *ShortlistService) Close(ctx context.Context, p domain.Principal, id domain.ShortlistID) (*domain.Shortlist, error) {
	hirer, err := s.capable(ctx, p)
	if err != nil {
		return nil, err
	}
	if _, err := s.owned(ctx, hirer, id); err != nil {
		return nil, err
	}

	closed, err := s.shortlists.Close(ctx, id)
	if err != nil {
		if errors.Is(err, port.ErrNotFound) {
			return nil, Coded(ErrConflict, CodeShortlistClosed, "this round is already closed")
		}
		return nil, fmt.Errorf("closing the round: %w", err)
	}
	return closed, nil
}

// AddEntry stages a candidate. It discloses NOTHING.
//
// AssertNotSelf fires HERE rather than at confirm. Discovering at send time
// that one of your candidates was never eligible is too late (ADR-0008 §3a).
func (s *ShortlistService) AddEntry(ctx context.Context, p domain.Principal, id domain.ShortlistID, target domain.UserID, note string) (*domain.ShortlistEntry, error) {
	hirer, err := s.capable(ctx, p)
	if err != nil {
		return nil, err
	}
	if _, err := s.owned(ctx, hirer, id); err != nil {
		return nil, err
	}
	if err := s.access.AssertNotSelf(ctx, hirer.ID, target); err != nil {
		return nil, err
	}

	entry, err := s.shortlists.AddEntry(ctx, &domain.ShortlistEntry{
		ShortlistID: id, UserID: target, Note: note, AddedBy: domain.RefTo(hirer),
	})
	if err != nil {
		switch {
		case errors.Is(err, port.ErrConflict):
			return nil, Coded(ErrConflict, CodeAlreadyShortlisted,
				"this contributor is already on the round")
		case errors.Is(err, port.ErrNotFound):
			return nil, Coded(ErrConflict, CodeShortlistClosed,
				"a closed round takes no new entries")
		}
		return nil, fmt.Errorf("staging the entry: %w", err)
	}
	return entry, nil
}

// RemoveEntry deletes a staged candidate.
//
// Permitted only while unnotified. Afterwards the contributor was told, and
// deleting the entry would destroy the record of a disclosure that happened.
func (s *ShortlistService) RemoveEntry(ctx context.Context, p domain.Principal, id domain.ShortlistID, target domain.UserID) error {
	hirer, err := s.capable(ctx, p)
	if err != nil {
		return err
	}
	if _, err := s.owned(ctx, hirer, id); err != nil {
		return err
	}

	if err := s.shortlists.RemoveEntry(ctx, id, target); err != nil {
		switch {
		case errors.Is(err, port.ErrEntryNotified):
			refusal := Coded(ErrConflict, CodeEntryAlreadyNotified,
				"this contributor has been told; the entry stays")
			var notified *port.NotifiedEntryError
			if errors.As(err, &notified) {
				return refusal.WithDetail(map[string]any{"notified_at": notified.NotifiedAt})
			}
			return refusal
		case errors.Is(err, port.ErrConflict):
			return fmt.Errorf("this contributor has been told; the entry stays: %w", ErrConflict)
		case errors.Is(err, port.ErrNotFound):
			return ErrNotFound
		}
		return fmt.Errorf("removing the entry: %w", err)
	}
	return nil
}

// Confirm notifies every unnotified entry. IRREVERSIBLE.
//
// The requests are written in one transaction; the emails go out AFTER commit.
// An email cannot be rolled back, so sending inside the transaction would tell
// a contributor about interest that a failure then erased.
func (s *ShortlistService) Confirm(ctx context.Context, p domain.Principal, id domain.ShortlistID) (*port.ConfirmResult, error) {
	hirer, err := s.capable(ctx, p)
	if err != nil {
		return nil, err
	}
	before, err := s.owned(ctx, hirer, id)
	if err != nil {
		return nil, err
	}

	alreadyNotified := 0
	for _, e := range before.Entries {
		if !e.Removable() {
			alreadyNotified++
		}
	}

	var requests []domain.ContactRequest
	err = s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		var err error
		requests, err = s.shortlists.Confirm(ctx, tx, id)
		return err
	})
	if err != nil {
		if errors.Is(err, port.ErrConflict) {
			return nil, Coded(ErrConflict, CodeShortlistClosed, "this round is closed")
		}
		return nil, fmt.Errorf("confirming: %w", err)
	}

	// After commit, and failures do not undo the disclosure — the contributor
	// sees the request on their dashboard whether or not the email lands.
	s.notifyContacts(ctx, requests)

	after, err := s.shortlists.ByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("reading the confirmed round: %w", err)
	}
	return &port.ConfirmResult{
		Shortlist: after, Notified: len(requests),
		AlreadyNotified: alreadyNotified, Requests: requests,
	}, nil
}

// ContactRequests reads a round's requests for the hirer side.
func (s *ShortlistService) ContactRequests(ctx context.Context, p domain.Principal, id domain.ShortlistID) ([]domain.ContactRequest, error) {
	hirer, err := s.capable(ctx, p)
	if err != nil {
		return nil, err
	}
	if _, err := s.owned(ctx, hirer, id); err != nil {
		return nil, err
	}

	out, err := s.contacts.ListForShortlist(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("listing contact requests: %w", err)
	}
	return out, nil
}

// notifyContacts tells each contributor an organization is interested, and
// WHAT IN (ADR-0019 §2).
//
// This message IS the contact request — the platform sends it, the address is
// disclosed to nobody, and acceptance is still what releases it. Carrying the
// role is what turns "somebody is interested in you" into something a person
// can answer honestly, which produces fewer yeses that evaporate on the first
// call and more informed declines.
func (s *ShortlistService) notifyContacts(ctx context.Context, requests []domain.ContactRequest) {
	// One role per round, read once. A company running a round against forty
	// people produces forty requests pointing at one job.
	roles := map[domain.RoleID]*domain.Role{}

	for _, cr := range requests {
		contributor, err := s.users.ByID(ctx, cr.UserID)
		if err != nil {
			continue
		}
		org, err := s.hirers.Organization(ctx, cr.OrganizationID)
		if err != nil {
			continue
		}
		role, ok := roles[cr.RoleID]
		if !ok {
			// A role we cannot read leaves the message thinner rather than
			// unsent. The request is real either way, and withholding somebody's
			// invitation because a row would not load helps nobody.
			role, _ = s.roles.ByID(ctx, cr.RoleID)
			roles[cr.RoleID] = role
		}

		// The payment disclosure (ADR-0002 §5). A contributor deciding whether
		// to release their email is told what the platform knows.
		data := map[string]any{
			"organization":          org.Name,
			"payment_verified":      org.PaymentVerified(),
			"tentative_result_date": cr.TentativeResultDate,
			"contact_request_id":    string(cr.ID),
		}
		if role != nil {
			data["role_title"] = role.Title
			// Words, not enum values. "full_time, remote" in somebody's inbox
			// is the database leaking into a sentence.
			data["role_engagement"] = role.Engagement.Label()
			data["role_location"] = role.Location.Label()

			// The process fields, disclosed HERE and nowhere else (ADR-0017).
			if role.MaxInterviewRounds != nil {
				data["max_interview_rounds"] = *role.MaxInterviewRounds
			}
			if role.AvgDaysToOffer != nil {
				data["avg_days_to_offer"] = *role.AvgDaysToOffer
			}
			if role.RequiresOnlineTest != nil {
				data["requires_online_test"] = *role.RequiresOnlineTest
			}
		}

		_ = s.notifier.Send(ctx, port.Notification{
			Kind:      port.NotifyContactRequest,
			Recipient: contributor.Email,
			Data:      data,
		})
	}
}

// capable extracts a hirer and checks the ADR-0002 gate.
func (s *ShortlistService) capable(ctx context.Context, p domain.Principal) (*domain.Hirer, error) {
	hirer, err := requireHirer(p)
	if err != nil {
		return nil, err
	}
	if err := s.access.RequireHiringCapability(ctx, p); err != nil {
		return nil, err
	}

	// A round belongs to an ORGANIZATION, not to the recruiter who made it —
	// one that vanished when a recruiter left would be worse than useless
	// (ADR-0008 §3a), and shortlists.organization_id is NOT NULL because of it.
	//
	// An INDEPENDENT hirer has no organisation (ADR-0017 §1), so there is
	// nothing for a round to belong to. Refused here, plainly, rather than
	// passed down to fail as an invalid uuid and reach the caller as a 500.
	//
	// PROVISIONAL. ADR-0017 says an independent hirer is verified individually
	// and hires, but the whole shortlist and contact model is organisation
	// scoped, and the ADR does not say how the two meet. Reported to the
	// Planner; a clear refusal is the honest placeholder until they decide,
	// and is reversible in a way that inventing a one-person organisation
	// here would not be.
	if hirer.OrganizationID == "" {
		return nil, Coded(ErrNotCapable, CodeOrganizationRequired,
			"hiring rounds belong to an organisation, and this account has none")
	}
	return hirer, nil
}

// owned reads a round the caller's organization owns.
//
// Another organization's is NOT FOUND. A competitor must not be able to
// confirm that a given id belongs to somebody.
func (s *ShortlistService) owned(ctx context.Context, hirer *domain.Hirer, id domain.ShortlistID) (*domain.Shortlist, error) {
	sl, err := s.shortlists.ByID(ctx, id)
	if err != nil {
		if errors.Is(err, port.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("reading the round: %w", err)
	}
	if sl.OrganizationID != hirer.OrganizationID {
		return nil, ErrNotFound
	}
	return sl, nil
}
