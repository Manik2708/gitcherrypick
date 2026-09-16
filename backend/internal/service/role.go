package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// RoleService owns openings (ADR-0019).
//
// Three rules run through the file, and each exists because the alternative
// quietly harms somebody:
//
//   - AN OPEN ROLE IS IMMUTABLE. A change writes a successor and closes the
//     original, so a contributor contacted last week keeps reading the salary
//     they agreed to talk about.
//   - AUTHORITY IS THE ORGANISATION'S CHOICE, not this file's. Three settings,
//     one per action, and the staging half of a change is separated from the
//     committing half so an owner can hold the second without blocking the
//     first.
//   - A HIRE MAY ONLY NAME SOMEBODY WHO AGREED TO TALK. The email must already
//     have been released to this organisation, which is both the honest
//     definition of "hired through the platform" and what stops the field being
//     a way to test whether an address has an account here.
type RoleService struct {
	roles    port.RoleRepository
	settings port.OrgSettingsRepository
	contacts port.ContactRepository
	profiles port.ProfileRepository
	users    port.UserRepository
	orgs     port.OrganizationRepository
	tx       port.TxManager
	clock    port.Clock
}

// NewRoleService wires openings.
func NewRoleService(
	roles port.RoleRepository,
	settings port.OrgSettingsRepository,
	contacts port.ContactRepository,
	profiles port.ProfileRepository,
	users port.UserRepository,
	orgs port.OrganizationRepository,
	tx port.TxManager,
	clock port.Clock,
) *RoleService {
	return &RoleService{roles: roles, settings: settings, contacts: contacts,
		profiles: profiles, users: users, orgs: orgs, tx: tx, clock: clock}
}

var _ port.RoleService = (*RoleService)(nil)

// Create writes a draft.
//
// A role is never born open. Opening is a separate act, which is what makes
// role_create_authority enforceable at all: a create-and-open in one request
// would hand the commitment to whoever could stage it.
func (s *RoleService) Create(ctx context.Context, p domain.Principal, org domain.OrganizationID, in domain.Role) (*domain.Role, error) {
	hirer, settings, err := s.seat(ctx, p, org)
	if err != nil {
		return nil, err
	}
	if !settings.RoleCreateAuthority.Permits(hirer.OrgRole == domain.RoleOwner) {
		return nil, Coded(ErrForbidden, CodeForbidden,
			"only an owner may create a role in this organisation")
	}

	in.OrgID = org
	in.CreatedBy = hirer.ID
	if err := s.validate(ctx, org, &in); err != nil {
		return nil, err
	}

	var out *domain.Role
	if err := s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		created, err := s.roles.Create(ctx, tx, &in)
		if err != nil {
			return err
		}
		out = created
		return nil
	}); err != nil {
		return nil, fmt.Errorf("creating the role: %w", err)
	}
	return out, nil
}

// Update replaces a DRAFT's contents.
//
// An open role is refused here rather than silently revised: the two are
// different acts with different consequences — one changes something nobody has
// seen, the other withdraws a live opening and replaces it — and a client that
// meant one must not get the other.
func (s *RoleService) Update(ctx context.Context, p domain.Principal, org domain.OrganizationID, id domain.RoleID, in domain.Role) (*domain.Role, error) {
	hirer, settings, err := s.seat(ctx, p, org)
	if err != nil {
		return nil, err
	}
	existing, err := s.owned(ctx, org, id)
	if err != nil {
		return nil, err
	}
	if !settings.RoleUpdateAuthority.Permits(hirer.OrgRole == domain.RoleOwner) {
		return nil, Coded(ErrForbidden, CodeForbidden,
			"only an owner may change a role in this organisation")
	}
	if existing.Status != domain.RoleDraft {
		return nil, Coded(ErrConflict, CodeRoleImmutable,
			"this role has been opened and cannot be edited — revise it instead, "+
				"which publishes a new version and leaves everybody already "+
				"contacted reading the one they were shown")
	}

	in.ID = id
	in.OrgID = org
	if err := s.validate(ctx, org, &in); err != nil {
		return nil, err
	}

	var out *domain.Role
	if err := s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		updated, err := s.roles.UpdateDraft(ctx, tx, &in)
		if err != nil {
			return err
		}
		out = updated
		return nil
	}); err != nil {
		return nil, fmt.Errorf("updating the role: %w", err)
	}
	return out, nil
}

// Revise publishes a successor to an open role and closes the original.
//
// The successor is a DRAFT: opening it is the commitment, and a revision that
// opened itself would route around the owner the setting exists to involve.
func (s *RoleService) Revise(ctx context.Context, p domain.Principal, org domain.OrganizationID, id domain.RoleID, in domain.Role) (*domain.Role, error) {
	hirer, settings, err := s.seat(ctx, p, org)
	if err != nil {
		return nil, err
	}
	existing, err := s.owned(ctx, org, id)
	if err != nil {
		return nil, err
	}
	if !settings.RoleUpdateAuthority.Permits(hirer.OrgRole == domain.RoleOwner) {
		return nil, Coded(ErrForbidden, CodeForbidden,
			"only an owner may change a role in this organisation")
	}
	if existing.Status != domain.RoleOpen {
		return nil, Coded(ErrConflict, CodeRoleImmutable,
			"only an open role is revised — edit a draft in place, and a closed "+
				"role by opening a new one")
	}

	in.OrgID = org
	in.CreatedBy = hirer.ID
	if err := s.validate(ctx, org, &in); err != nil {
		return nil, err
	}

	var out *domain.Role
	if err := s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		next, err := s.roles.Revise(ctx, tx, id, &in, hirer.ID, s.clock.Now())
		if err != nil {
			return err
		}
		out = next
		return nil
	}); err != nil {
		return nil, fmt.Errorf("revising the role: %w", err)
	}
	return out, nil
}

// Open signs the numbers.
//
// This is the COMMITTING half, so it reads Commits rather than Permits: under
// draft_and_approve anybody may have written the role and only an owner may
// send it out.
func (s *RoleService) Open(ctx context.Context, p domain.Principal, org domain.OrganizationID, id domain.RoleID) (*domain.Role, error) {
	hirer, settings, err := s.seat(ctx, p, org)
	if err != nil {
		return nil, err
	}
	if _, err := s.owned(ctx, org, id); err != nil {
		return nil, err
	}
	if !settings.RoleCreateAuthority.Commits(hirer.OrgRole == domain.RoleOwner) {
		return nil, Coded(ErrForbidden, CodeOwnerApprovalRequired,
			"an owner has to approve this role before it goes out — it states a "+
				"salary and a process on the organisation's behalf")
	}

	var out *domain.Role
	if err := s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		opened, err := s.roles.Open(ctx, tx, id, hirer.ID, s.clock.Now())
		if err != nil {
			return err
		}
		out = opened
		return nil
	}); err != nil {
		return nil, fmt.Errorf("opening the role: %w", err)
	}
	return out, nil
}

// Close ends a role and records why.
//
// A seat that may STAGE but not COMMIT gets its request recorded and the role
// stays open — a request is not an outcome, and returning a refusal would lose
// the fact that somebody asked.
func (s *RoleService) Close(ctx context.Context, p domain.Principal, org domain.OrganizationID, id domain.RoleID, in port.CloseRequest) (*domain.Role, error) {
	hirer, settings, err := s.seat(ctx, p, org)
	if err != nil {
		return nil, err
	}
	existing, err := s.owned(ctx, org, id)
	if err != nil {
		return nil, err
	}
	if existing.Status != domain.RoleOpen {
		return nil, Coded(ErrConflict, CodeRoleImmutable,
			"this role is not open")
	}

	owner := hirer.OrgRole == domain.RoleOwner
	if !settings.RoleCloseAuthority.Permits(owner) {
		return nil, Coded(ErrForbidden, CodeForbidden,
			"only an owner may close a role in this organisation")
	}

	if !in.Reason.Choosable() {
		// 'superseded' is written by a revision and offered to nobody: a hirer
		// claiming a role was replaced when nothing replaced it would leave a
		// closed role that lies about why.
		return nil, Coded(ErrInvalid, CodeInvalidRole,
			"%q is not a reason a role can be closed for", in.Reason)
	}
	if in.Reason == domain.CloseOther && strings.TrimSpace(in.Note) == "" {
		return nil, Coded(ErrInvalid, CodeInvalidRole,
			"tell us what happened — \"other\" with nothing after it is not a reason")
	}

	// Resolve the people BEFORE deciding whether this seat may commit, so a
	// recruiter staging a closure still learns that an address was wrong rather
	// than discovering it after an owner approves.
	hires, err := s.resolveHires(ctx, org, in)
	if err != nil {
		return nil, err
	}

	if !settings.RoleCloseAuthority.Commits(owner) {
		var out *domain.Role
		if err := s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
			requested, err := s.roles.RequestClose(ctx, tx, id, hirer.ID, s.clock.Now())
			if err != nil {
				return err
			}
			out = requested
			return nil
		}); err != nil {
			return nil, fmt.Errorf("requesting closure: %w", err)
		}
		return out, nil
	}

	var out *domain.Role
	if err := s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		closed, err := s.roles.Close(ctx, tx, id, port.RoleClosure{
			By: hirer.ID, At: s.clock.Now(), Reason: in.Reason,
			Note: strings.TrimSpace(in.Note), Hires: hires,
		})
		if err != nil {
			return err
		}
		out = closed
		return nil
	}); err != nil {
		return nil, fmt.Errorf("closing the role: %w", err)
	}
	return out, nil
}

// resolveHires turns the addresses a hirer typed into contributors.
//
// EVERY address must already have been released to this organisation by an
// accepted contact request. The refusal names WHICH one failed, because a hirer
// entering several must not be left guessing — and it offers the honest
// alternative rather than silently downgrading the reason, which would leave a
// company believing it had recorded something it had not.
func (s *RoleService) resolveHires(ctx context.Context, org domain.OrganizationID, in port.CloseRequest) ([]domain.UserID, error) {
	if in.Reason != domain.CloseHiredViaPlatform {
		if len(in.HiredEmails) > 0 {
			return nil, Coded(ErrInvalid, CodeInvalidRole,
				"only a role closed as hired through the platform names anybody")
		}
		return nil, nil
	}

	if len(in.HiredEmails) == 0 {
		return nil, Coded(ErrInvalid, CodeInvalidRole,
			"tell us who you hired — this is the one thing that closes the loop "+
				"between a ranked contributor and a job")
	}

	seen := map[domain.UserID]bool{}
	out := make([]domain.UserID, 0, len(in.HiredEmails))
	for _, email := range in.HiredEmails {
		email = strings.TrimSpace(email)
		if email == "" {
			continue
		}
		id, err := s.contacts.ReleasedTo(ctx, org, email)
		if err != nil {
			if errors.Is(err, port.ErrNotFound) {
				return nil, Coded(ErrInvalid, CodeHireNotReleased,
					"%s never accepted a contact request from you, so we cannot "+
						"record them as hired through the platform — if you found "+
						"them another way, close this as hired elsewhere", email)
			}
			return nil, fmt.Errorf("resolving %s: %w", email, err)
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil, Coded(ErrInvalid, CodeInvalidRole, "tell us who you hired")
	}
	return out, nil
}

// List returns the organisation's roles.
func (s *RoleService) List(ctx context.Context, p domain.Principal, org domain.OrganizationID, status *domain.RoleStatus) ([]domain.Role, error) {
	if _, _, err := s.seat(ctx, p, org); err != nil {
		return nil, err
	}
	out, err := s.roles.ListByOrganization(ctx, org, status)
	if err != nil {
		return nil, fmt.Errorf("listing roles: %w", err)
	}
	return out, nil
}

// Role returns one.
func (s *RoleService) Role(ctx context.Context, p domain.Principal, org domain.OrganizationID, id domain.RoleID) (*domain.Role, error) {
	if _, _, err := s.seat(ctx, p, org); err != nil {
		return nil, err
	}
	return s.owned(ctx, org, id)
}

// Settings returns the organisation's policy.
//
// Readable by ANY seat, not owners only: a member who cannot see the rule they
// are working under discovers it by being refused, which is the worst way to
// learn it.
func (s *RoleService) Settings(ctx context.Context, p domain.Principal, org domain.OrganizationID) (*domain.OrgSettings, error) {
	if _, _, err := s.seat(ctx, p, org); err != nil {
		return nil, err
	}
	out, err := s.settings.Settings(ctx, org)
	if err != nil {
		return nil, fmt.Errorf("reading settings: %w", err)
	}
	return out, nil
}

// SaveSettings replaces it. OWNERS ONLY, under every combination — otherwise a
// member sets any_hirer and grants themselves the authority the setting exists
// to withhold.
func (s *RoleService) SaveSettings(ctx context.Context, p domain.Principal, org domain.OrganizationID, in domain.OrgSettings) (*domain.OrgSettings, error) {
	hirer, _, err := s.seat(ctx, p, org)
	if err != nil {
		return nil, err
	}
	if hirer.OrgRole != domain.RoleOwner {
		return nil, Coded(ErrForbidden, CodeForbidden,
			"only an owner may change who can create and close roles")
	}

	for _, a := range []domain.RoleAuthority{
		in.RoleCreateAuthority, in.RoleUpdateAuthority, in.RoleCloseAuthority,
	} {
		if !a.Valid() {
			return nil, Coded(ErrInvalid, CodeInvalidRole,
				"%q is not one of any_hirer, draft_and_approve or owners_only", a)
		}
	}

	in.OrgID = org
	if err := s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		return s.settings.Save(ctx, tx, &in)
	}); err != nil {
		return nil, fmt.Errorf("saving settings: %w", err)
	}
	return s.settings.Settings(ctx, org)
}

// Matching returns the open roles a contributor could be approached for.
//
// A contributor's own view, and the ONE place their compensation expectation is
// used: it keeps roles paying less than they asked for out of their way, and
// travels no further (ADR-0018 §5).
func (s *RoleService) Matching(ctx context.Context, id domain.UserID, limit, offset int) ([]domain.Role, error) {
	prefs, err := s.profiles.WorkPreferences(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("reading work preferences: %w", err)
	}

	pay, err := s.profiles.Compensation(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("reading compensation: %w", err)
	}

	match := port.RoleMatch{
		Country:   prefs.CurrentCountry,
		Shapes:    shapesOf(prefs),
		OfficeYOE: prefs.OfficeYOE,
		OSSYears:  prefs.OSSYears(s.clock.Now()),
		Currency:  pay.Currency,
		MinYearly: pay.YearlyAmount,
		MinHourly: pay.HourlyRate,
		Limit:     limit, Offset: offset,
	}

	out, err := s.roles.Matching(ctx, match)
	if err != nil {
		return nil, fmt.Errorf("matching roles: %w", err)
	}
	return out, nil
}

// shapesOf turns a contributor's flags into the engagements they admit.
//
// The inverse of Engagement.Shape, kept next to it so the two cannot disagree
// about what a tick means.
func shapesOf(w *domain.WorkPreferences) []domain.Engagement {
	out := []domain.Engagement{}
	for _, e := range []domain.Engagement{
		domain.EngagementFullTime, domain.EngagementContract,
		domain.EngagementInternship, domain.EngagementFreelance,
	} {
		if e == domain.EngagementFreelance {
			// Freelance is answered by availability_status rather than a flag
			// (ADR-0018 §2), and this query does not read it — so a contributor
			// who ticked nothing sees no freelance roles either. Matching on it
			// here would show freelance work to somebody who said they wanted
			// none of these, which is the failure the empty case exists to
			// prevent.
			continue
		}
		if e.Shape(w) {
			out = append(out, e)
		}
	}
	return out
}

// --- shared checks -----------------------------------------------------------

// seat resolves the caller as a hirer in this organisation, with the policy
// that governs them.
func (s *RoleService) seat(ctx context.Context, p domain.Principal, org domain.OrganizationID) (*domain.Hirer, *domain.OrgSettings, error) {
	hirer, err := requireHirer(p)
	if err != nil {
		return nil, nil, err
	}
	if hirer.OrganizationID != org {
		return nil, nil, Coded(ErrForbidden, CodeNotAnOrgMember,
			"you are not a member of that organisation")
	}
	settings, err := s.settings.Settings(ctx, org)
	if err != nil {
		return nil, nil, fmt.Errorf("reading settings: %w", err)
	}
	return hirer, settings, nil
}

// owned reads a role and refuses one belonging to somebody else.
//
// NOT FOUND rather than forbidden: whether another organisation has a role with
// a given id is not this caller's business, and a distinct refusal would
// confirm it exists.
func (s *RoleService) owned(ctx context.Context, org domain.OrganizationID, id domain.RoleID) (*domain.Role, error) {
	role, err := s.roles.ByID(ctx, id)
	if err != nil {
		if errors.Is(err, port.ErrNotFound) {
			return nil, Coded(ErrNotFound, CodeRoleNotFound, "no such role")
		}
		return nil, fmt.Errorf("reading role %s: %w", id, err)
	}
	if role.OrgID != org {
		return nil, Coded(ErrNotFound, CodeRoleNotFound, "no such role")
	}
	return role, nil
}

// validate checks the fields that can be wrong.
//
// Every refusal is CODED. A bare ErrInvalid falls back to invalid_claim at the
// controller, which would tell a hirer their EVIDENCE was rejected when what
// was wrong was a currency — "claim" is a specific noun on this platform.
func (s *RoleService) validate(ctx context.Context, org domain.OrganizationID, r *domain.Role) error {
	r.Title = strings.TrimSpace(r.Title)
	if r.Title == "" {
		return Coded(ErrInvalid, CodeInvalidRole, "a role needs a title")
	}
	if !r.Engagement.Valid() {
		return Coded(ErrInvalid, CodeInvalidRole,
			"%q is not full_time, contract, internship or freelance", r.Engagement)
	}
	if !r.Location.Valid() {
		return Coded(ErrInvalid, CodeInvalidRole,
			"%q is not remote or address", r.Location)
	}

	switch r.Location {
	case domain.LocationAddress:
		if r.AddressID == nil {
			return Coded(ErrInvalid, CodeInvalidRole,
				"a role at an office needs an address — add one to the "+
					"organisation first, and every role there can point at it")
		}
		if err := s.addressBelongs(ctx, org, *r.AddressID); err != nil {
			return err
		}
	case domain.LocationRemote:
		// A remote role must not carry a pointer to the office it used to be
		// at. Cleared rather than refused: somebody switching a role to remote
		// is not making a mistake.
		r.AddressID = nil
	}

	// Freelance pays by the hour; everything else by the year. A role
	// advertising both cannot be compared against anybody's expectation.
	if r.Engagement == domain.EngagementFreelance {
		r.YearlyCTC, r.YearlyBase = nil, nil
	} else {
		r.HourlyRate, r.ExpectedHours = nil, nil
	}

	r.Currency = strings.ToUpper(strings.TrimSpace(r.Currency))
	stated := r.YearlyCTC != nil || r.YearlyBase != nil || r.HourlyRate != nil
	if stated && !validCurrencyCode(r.Currency) {
		return Coded(ErrInvalid, CodeInvalidRole,
			"an amount needs a currency — three letters, such as GBP")
	}
	if !stated {
		r.Currency = ""
	}
	for _, amount := range []*int64{r.YearlyCTC, r.YearlyBase, r.HourlyRate} {
		if amount != nil && *amount <= 0 {
			return Coded(ErrInvalid, CodeInvalidRole, "an amount must be positive")
		}
	}
	if r.YearlyCTC != nil && r.YearlyBase != nil && *r.YearlyBase > *r.YearlyCTC {
		return Coded(ErrInvalid, CodeInvalidRole,
			"the base cannot exceed the total package it is part of")
	}
	if r.ExpectedHours != nil && (*r.ExpectedHours <= 0 || *r.ExpectedHours > 168) {
		return Coded(ErrInvalid, CodeInvalidRole,
			"expected hours must be between 1 and 168 — there are 168 in a week")
	}

	for _, yoe := range []*int{r.MinOfficeYOE, r.MinOSSYOE} {
		if yoe != nil && (*yoe < 0 || *yoe > 80) {
			return Coded(ErrInvalid, CodeInvalidRole,
				"a minimum must be between 0 and 80 years")
		}
	}
	if r.MaxInterviewRounds != nil && *r.MaxInterviewRounds <= 0 {
		return Coded(ErrInvalid, CodeInvalidRole,
			"a process with no rounds is not a process — leave it blank instead")
	}
	if r.AvgDaysToOffer != nil && *r.AvgDaysToOffer < 0 {
		return Coded(ErrInvalid, CodeInvalidRole,
			"days to an offer cannot be negative")
	}

	countries := make([]string, 0, len(r.EligibleCountries))
	seen := map[string]bool{}
	for _, c := range r.EligibleCountries {
		c = strings.ToUpper(strings.TrimSpace(c))
		if c == "" || seen[c] {
			continue
		}
		if !validCountryCode(c) {
			return Coded(ErrInvalid, CodeInvalidRole,
				"%q is not a two-letter country code", c)
		}
		seen[c] = true
		countries = append(countries, c)
	}
	// Empty stays empty, and means ANYWHERE. An organisation that never
	// considered work authorisation should not exclude the world by leaving a
	// field blank (ADR-0019 §6).
	r.EligibleCountries = countries

	for i := range r.Questions {
		r.Questions[i].Question = strings.TrimSpace(r.Questions[i].Question)
		if r.Questions[i].Question == "" {
			return Coded(ErrInvalid, CodeInvalidRole,
				"a screening question needs to say something")
		}
	}
	return nil
}

// addressBelongs refuses a role pointed at somebody else's office.
func (s *RoleService) addressBelongs(ctx context.Context, org domain.OrganizationID, id domain.AddressID) error {
	addresses, err := s.orgs.Addresses(ctx, org)
	if err != nil {
		return fmt.Errorf("reading addresses: %w", err)
	}
	for _, a := range addresses {
		if a.ID == id {
			return nil
		}
	}
	return Coded(ErrInvalid, CodeInvalidRole,
		"that address does not belong to this organisation")
}
