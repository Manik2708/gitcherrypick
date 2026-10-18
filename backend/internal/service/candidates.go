package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// Staging and contacting, on the role (ADR-0023).
//
// Staging is private; contacting is irreversible (ADR-0008 §3a). The rule it
// serves is that a contacted contributor can never be un-contacted, and a rule
// that strict is only tolerable if there is a moment before it binds.
//
// This was a separate ShortlistService over a separate table. There was never
// more than one round per role, so it was a second name for the same job,
// carrying a second title and a second status that could disagree with the
// role's.

// Stage puts somebody on a role and discloses nothing.
//
// AssertNotSelf fires HERE rather than at contact time: discovering that a
// candidate is the recruiter's own account at send time is too late.
func (s *RoleService) Stage(ctx context.Context, p domain.Principal, org domain.OrganizationID, id domain.RoleID, target domain.UserID, note string) (*domain.RoleCandidate, error) {
	hirer, err := s.hiring(ctx, p, org)
	if err != nil {
		return nil, err
	}
	role, err := s.owned(ctx, org, id)
	if err != nil {
		return nil, err
	}
	if role.Status == domain.RoleClosed {
		return nil, Coded(ErrConflict, CodeRoleClosed,
			"a closed role takes nobody new")
	}
	if err := s.access.AssertNotSelf(ctx, hirer.ID, target); err != nil {
		return nil, err
	}

	candidate, err := s.roles.Stage(ctx, &domain.RoleCandidate{
		UserID: target, Note: note, AddedBy: domain.RefTo(hirer),
	}, id)
	if err != nil {
		switch {
		case errors.Is(err, port.ErrConflict):
			return nil, Coded(ErrConflict, CodeAlreadyShortlisted,
				"this contributor is already on the role")
		case errors.Is(err, port.ErrNotFound):
			return nil, Coded(ErrConflict, CodeRoleClosed,
				"a closed role takes nobody new")
		}
		return nil, fmt.Errorf("staging the candidate: %w", err)
	}
	return candidate, nil
}

// Unstage takes a staged candidate off a role.
//
// Permitted only while they have been told nothing. Afterwards the contributor
// was contacted, and deleting the row would destroy the record of a disclosure
// that already happened.
func (s *RoleService) Unstage(ctx context.Context, p domain.Principal, org domain.OrganizationID, id domain.RoleID, target domain.UserID) error {
	if _, err := s.hiring(ctx, p, org); err != nil {
		return err
	}
	if _, err := s.owned(ctx, org, id); err != nil {
		return err
	}

	if err := s.roles.Unstage(ctx, id, target); err != nil {
		switch {
		case errors.Is(err, port.ErrEntryNotified):
			refusal := Coded(ErrConflict, CodeEntryAlreadyNotified,
				"this contributor has been told; they stay on the role")
			var notified *port.NotifiedEntryError
			if errors.As(err, &notified) {
				return refusal.WithDetail(map[string]any{"notified_at": notified.NotifiedAt})
			}
			return refusal
		case errors.Is(err, port.ErrConflict):
			return fmt.Errorf("this contributor has been told; they stay: %w", ErrConflict)
		case errors.Is(err, port.ErrNotFound):
			return ErrNotFound
		}
		return fmt.Errorf("unstaging the candidate: %w", err)
	}
	return nil
}

// Contact tells every staged candidate. IRREVERSIBLE.
//
// The requests are written in one transaction; the emails go out AFTER commit.
// An email cannot be rolled back, so sending inside the transaction would tell
// a contributor about interest that a failure then erased.
//
// resultBy must be in the future: it is what each contributor is told, and the
// overdue ratio is computed against it. A date already past would be promised
// broken (ADR-0005).
func (s *RoleService) Contact(ctx context.Context, p domain.Principal, org domain.OrganizationID, id domain.RoleID, resultBy time.Time) (*port.ContactResult, error) {
	hirer, err := s.hiring(ctx, p, org)
	if err != nil {
		return nil, err
	}
	role, err := s.owned(ctx, org, id)
	if err != nil {
		return nil, err
	}
	switch role.Status {
	case domain.RoleClosed:
		return nil, Coded(ErrConflict, CodeRoleClosed,
			"a closed role contacts nobody")
	case domain.RoleDraft:
		// A draft is not a commitment (ADR-0019 §4). Telling somebody about a
		// salary nobody signed off is what opening a role exists to prevent.
		return nil, Coded(ErrConflict, CodeRoleNotOpen,
			"open the role first — a draft is not a commitment anybody may be told about")
	}

	if resultBy.IsZero() {
		return nil, Coded(ErrInvalid, CodeInvalidRole,
			"say when you expect to have an answer — everybody you contact is told this date").
			WithDetail(map[string]any{"field": "tentative_result_date"})
	}
	if !resultBy.After(s.clock.Now()) {
		return nil, Coded(ErrInvalid, CodeInvalidRole,
			"that date has already passed, so it would be overdue the moment it was sent").
			WithDetail(map[string]any{"field": "tentative_result_date"})
	}

	before, err := s.roles.Candidates(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("reading the candidates: %w", err)
	}
	alreadyNotified := 0
	for i := range before {
		if !domain.Removable(&before[i]) {
			alreadyNotified++
		}
	}

	var requests []domain.ContactRequest
	err = s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		var err error
		requests, err = s.roles.Contact(ctx, tx, id, hirer.ID, resultBy)
		return err
	})
	if err != nil {
		if errors.Is(err, port.ErrConflict) {
			return nil, Coded(ErrConflict, CodeRoleClosed, "this role is closed")
		}
		return nil, fmt.Errorf("contacting: %w", err)
	}

	// After commit, and failures do not undo the disclosure — the contributor
	// sees the request on their dashboard whether or not the email lands.
	s.notifyContacts(ctx, requests)

	after, err := s.roles.ByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("reading the role back: %w", err)
	}
	return &port.ContactResult{
		Role: after, Notified: len(requests),
		AlreadyNotified: alreadyNotified, Requests: requests,
	}, nil
}

// ContactRequests is what each contacted person answered.
func (s *RoleService) ContactRequests(ctx context.Context, p domain.Principal, org domain.OrganizationID, id domain.RoleID) ([]domain.ContactRequest, error) {
	if _, err := s.hiring(ctx, p, org); err != nil {
		return nil, err
	}
	if _, err := s.owned(ctx, org, id); err != nil {
		return nil, err
	}

	out, err := s.roles.ContactRequestsForRole(ctx, id)
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
func (s *RoleService) notifyContacts(ctx context.Context, requests []domain.ContactRequest) {
	// One role per contact step, read once. A company contacting forty people
	// produces forty requests pointing at one job.
	roles := map[domain.RoleID]*domain.Role{}

	// Country names for the location phrase, read once for the whole step.
	names := s.countryNames(ctx)

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
			// unsent. The request is real either way, and withholding
			// somebody's invitation because a row would not load helps nobody.
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
			data["role_location"] = domain.LocationPhrase(role.Locations, names)

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

// hiring is seat() plus the ADR-0002 capability gate.
//
// Reading a role needs a seat; putting a person on one needs a VERIFIED
// account, because it ends in somebody being contacted on the organisation's
// behalf.
func (s *RoleService) hiring(ctx context.Context, p domain.Principal, org domain.OrganizationID) (*domain.Hirer, error) {
	hirer, _, err := s.seat(ctx, p, org)
	if err != nil {
		return nil, err
	}
	if err := s.access.RequireHiringCapability(ctx, p); err != nil {
		return nil, err
	}
	return hirer, nil
}

// countryNames turns a country code into its name, from the place service.
//
// FAILS OPEN: a provider that is down, or a code it does not know, reads as
// the code itself. A contact request is not held back for a country's name
// (ADR-0018 §9).
func (s *RoleService) countryNames(ctx context.Context) func(string) string {
	return countryNamer(ctx, s.places)
}

// countryNamer is the shared lookup, for every service that names a country
// in a message.
func countryNamer(ctx context.Context, places port.PlaceService) func(string) string {
	known := map[string]string{}
	if places != nil {
		if countries, err := places.Countries(ctx); err == nil {
			for _, c := range countries {
				known[c.Code] = c.Name
			}
		}
	}
	return func(code string) string {
		if name := known[code]; name != "" {
			return name
		}
		return code
	}
}
