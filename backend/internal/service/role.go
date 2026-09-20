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
	roles port.RoleRepository

	// openings are roles made PUBLIC, with a bar on them (ADR-0020). A
	// separate repository because an opening is a separate row: an open role
	// is immutable, and a bar living on the role could not be raised without
	// superseding the job underneath.
	openings port.OpeningRepository

	// skills resolves a bar's SLUGS to ids. A hirer names "go", not a uuid —
	// the catalogue is slug-keyed everywhere a human touches it (search,
	// claims, the leaderboard), and an advert should not be the one place
	// that is different.
	skills port.SkillRepository

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
	openings port.OpeningRepository,
	skills port.SkillRepository,
	settings port.OrgSettingsRepository,
	contacts port.ContactRepository,
	profiles port.ProfileRepository,
	users port.UserRepository,
	orgs port.OrganizationRepository,
	tx port.TxManager,
	clock port.Clock,
) *RoleService {
	return &RoleService{roles: roles, openings: openings, skills: skills, settings: settings,
		contacts: contacts, profiles: profiles, users: users, orgs: orgs,
		tx: tx, clock: clock}
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
	existing, err := s.owned(ctx, org, id)
	if err != nil {
		return nil, err
	}
	if !settings.RoleCreateAuthority.Commits(hirer.OrgRole == domain.RoleOwner) {
		return nil, Coded(ErrForbidden, CodeOwnerApprovalRequired,
			"an owner has to approve this role before it goes out — it states a "+
				"salary and a process on the organisation's behalf")
	}

	// A CLOSED ROLE IS FINAL. Only a draft opens.
	//
	// Refused here rather than merely left out of the client, because a rule a
	// hirer is warned about before they close something has to be true
	// afterwards — and the repository's UPDATE would happily bring one back.
	//
	// The sharpest case is a superseded role: something replaced it, so
	// reopening would leave two open roles for one job, which is the state the
	// revise-and-close transaction exists to prevent (ADR-0019 §13). The rest
	// are refused for a plainer reason — everybody contacted was told the role
	// closed, and a job that un-closes makes that a lie.
	if existing.Status == domain.RoleClosed {
		return nil, Coded(ErrConflict, CodeRoleImmutable,
			"a closed role cannot be reopened — write a new one, which is also "+
				"what everybody you contacted about the old one would expect")
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
	// recruiter staging a closure still learns that somebody was not eligible
	// rather than discovering it after an owner approves.
	hires, err := s.resolveHires(ctx, id, in)
	if err != nil {
		return nil, err
	}

	// A seat that may STAGE but not COMMIT gets its request recorded and the
	// role stays open. Returning a refusal would lose the fact that somebody
	// asked.
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

// resolveHires checks that everybody named was a candidate on THIS role who
// accepted.
//
// Ids off the role's own candidate list rather than typed addresses. That is a
// better form of the same rule: a company may record hiring somebody who
// agreed to talk to it and nobody else (ADR-0019 §15), and picking from a list
// it already holds means the question "does this address have an account
// here?" can no longer be asked at all.
//
// Accepted, specifically. A STAGED candidate has been told nothing, a notified
// one has not answered, and a declined one said no — recording any of them as
// a hire would be a company asserting something about a person who never
// agreed to talk to it.
func (s *RoleService) resolveHires(ctx context.Context, role domain.RoleID, in port.CloseRequest) ([]domain.UserID, error) {
	if in.Reason != domain.CloseHiredViaPlatform {
		if len(in.Hired) > 0 {
			return nil, Coded(ErrInvalid, CodeInvalidRole,
				"only a role closed as hired through the platform names anybody")
		}
		return nil, nil
	}

	if len(in.Hired) == 0 {
		return nil, Coded(ErrInvalid, CodeInvalidRole,
			"tell us who you hired — this is the one thing that closes the loop "+
				"between a ranked contributor and a job")
	}

	candidates, err := s.roles.Candidates(ctx, role)
	if err != nil {
		return nil, fmt.Errorf("reading the candidates: %w", err)
	}
	accepted := map[domain.UserID]bool{}
	for _, c := range candidates {
		if c.Accepted {
			accepted[c.UserID] = true
		}
	}

	seen := map[domain.UserID]bool{}
	out := make([]domain.UserID, 0, len(in.Hired))
	for _, user := range in.Hired {
		if user == "" || seen[user] {
			continue
		}
		if !accepted[user] {
			// One refusal for every way of failing — never shortlisted,
			// never answered, declined — because telling them apart would
			// report a contributor's answer to a question about somebody
			// else's hiring.
			return nil, Coded(ErrInvalid, CodeHireNotAccepted,
				"only somebody who accepted a contact request for this role can "+
					"be recorded as hired through the platform — if you found "+
					"them another way, close this as hired elsewhere")
		}
		seen[user] = true
		out = append(out, user)
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

// Candidates lists everybody already on a round for this role.
func (s *RoleService) Candidates(ctx context.Context, p domain.Principal, org domain.OrganizationID, id domain.RoleID) ([]domain.RoleCandidate, error) {
	if _, _, err := s.seat(ctx, p, org); err != nil {
		return nil, err
	}
	if _, err := s.owned(ctx, org, id); err != nil {
		return nil, err
	}

	out, err := s.roles.Candidates(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("listing candidates: %w", err)
	}
	return out, nil
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

/* --- public openings (ADR-0020) --------------------------------------------- */

// Opening reads the advert on a role.
func (s *RoleService) Opening(ctx context.Context, p domain.Principal, org domain.OrganizationID, id domain.RoleID) (*domain.Opening, error) {
	if _, _, err := s.seat(ctx, p, org); err != nil {
		return nil, err
	}
	if _, err := s.owned(ctx, org, id); err != nil {
		return nil, err
	}

	out, err := s.openings.ByRole(ctx, id)
	if err != nil {
		if errors.Is(err, port.ErrNotFound) {
			return nil, Coded(ErrNotFound, CodeOpeningNotFound,
				"this role has not been advertised")
		}
		return nil, fmt.Errorf("reading the opening: %w", err)
	}
	return out, nil
}

// SaveOpening writes the bar. It does NOT publish.
func (s *RoleService) SaveOpening(ctx context.Context, p domain.Principal, org domain.OrganizationID, id domain.RoleID, in domain.Opening) (*domain.Opening, error) {
	hirer, settings, err := s.seat(ctx, p, org)
	if err != nil {
		return nil, err
	}
	if _, err := s.owned(ctx, org, id); err != nil {
		return nil, err
	}
	if !settings.RoleCreateAuthority.Permits(hirer.OrgRole == domain.RoleOwner) {
		return nil, Coded(ErrForbidden, CodeForbidden,
			"only an owner may advertise a role in this organisation")
	}
	if err := validateOpening(&in); err != nil {
		return nil, err
	}
	if err := s.resolveSkills(ctx, &in); err != nil {
		return nil, err
	}

	in.RoleID = id
	in.CreatedBy = hirer.ID

	var out *domain.Opening
	if err := s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		saved, err := s.openings.Save(ctx, tx, &in)
		out = saved
		return err
	}); err != nil {
		return nil, fmt.Errorf("saving the opening: %w", err)
	}
	return out, nil
}

// PublishOpening makes the advert visible to contributors.
//
// The COMMITTING half, so it reads Commits: an opening states a public bar on
// the organisation's behalf, which is the kind of thing ADR-0019 §11 put
// behind an owner. It reuses role_create_authority rather than adding a fourth
// setting, because a dial for a thing published alongside the role it
// describes is a dial nobody sets.
func (s *RoleService) PublishOpening(ctx context.Context, p domain.Principal, org domain.OrganizationID, id domain.RoleID) (*domain.Opening, error) {
	hirer, settings, err := s.seat(ctx, p, org)
	if err != nil {
		return nil, err
	}
	role, err := s.owned(ctx, org, id)
	if err != nil {
		return nil, err
	}
	if !settings.RoleCreateAuthority.Commits(hirer.OrgRole == domain.RoleOwner) {
		return nil, Coded(ErrForbidden, CodeOwnerApprovalRequired,
			"an owner has to approve this before it goes out publicly — it "+
				"states a salary and a bar on the organisation's behalf")
	}

	// Only an OPEN role may be advertised. A draft is not a commitment and a
	// closed role is a withdrawn job, so neither is something anybody should
	// be reading about.
	if !role.IsOpen() {
		return nil, Coded(ErrConflict, CodeRoleImmutable,
			"only an open role can be advertised — open the role first")
	}

	var out *domain.Opening
	if err := s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		published, err := s.openings.Publish(ctx, tx, id, hirer.ID, s.clock.Now())
		out = published
		return err
	}); err != nil {
		if errors.Is(err, port.ErrNotFound) {
			return nil, Coded(ErrNotFound, CodeOpeningNotFound,
				"write what the role asks for before advertising it")
		}
		return nil, fmt.Errorf("publishing the opening: %w", err)
	}
	return out, nil
}

// WithdrawOpening takes it down. Under role_close_authority, because it only
// ever withdraws something.
func (s *RoleService) WithdrawOpening(ctx context.Context, p domain.Principal, org domain.OrganizationID, id domain.RoleID) (*domain.Opening, error) {
	hirer, settings, err := s.seat(ctx, p, org)
	if err != nil {
		return nil, err
	}
	if _, err := s.owned(ctx, org, id); err != nil {
		return nil, err
	}
	if !settings.RoleCloseAuthority.Permits(hirer.OrgRole == domain.RoleOwner) {
		return nil, Coded(ErrForbidden, CodeForbidden,
			"only an owner may withdraw an advert in this organisation")
	}

	var out *domain.Opening
	if err := s.tx.InTx(ctx, func(ctx context.Context, tx port.Tx) error {
		withdrawn, err := s.openings.Withdraw(ctx, tx, id, s.clock.Now())
		out = withdrawn
		return err
	}); err != nil {
		return nil, fmt.Errorf("withdrawing the opening: %w", err)
	}
	return out, nil
}

// Openings is the contributor's read.
//
// It sends NOTHING to anybody. No application, no interest, no view count —
// the consent model runs one way, and this only changes what somebody can see
// (ADR-0020 §8). There is deliberately no write path from here.
func (s *RoleService) Openings(ctx context.Context, id domain.UserID, limit, offset int) (*port.OpeningResults, error) {
	out, err := s.openings.Matching(ctx, port.OpeningMatch{
		UserID: id, Limit: limit, Offset: offset,
	})
	if err != nil {
		return nil, fmt.Errorf("reading openings: %w", err)
	}

	// The role travels with each advert, so a contributor sees what the job
	// IS and not only what it demands.
	for i := range out.Openings {
		role, err := s.roles.ByID(ctx, out.Openings[i].RoleID)
		if err != nil {
			// A role that will not load leaves the advert thinner rather than
			// failing the page. The rest are still worth showing.
			continue
		}
		out.Openings[i].Role = role
	}
	return out, nil
}

// resolveSkills turns the slugs a hirer named into ids.
//
// An unknown slug is REFUSED rather than dropped. A bar quietly missing the
// skill it was written around would advertise a role to people who do not have
// it, and nothing in the response would say so.
func (s *RoleService) resolveSkills(ctx context.Context, o *domain.Opening) error {
	for i := range o.Skills {
		slug := strings.ToLower(strings.TrimSpace(o.Skills[i].Slug))
		if slug == "" {
			continue // already an id, from a caller that held one
		}
		skill, err := s.skills.BySlug(ctx, slug)
		if err != nil {
			if errors.Is(err, port.ErrNotFound) {
				return Coded(ErrInvalid, CodeUnknownSkill,
					"%q is not a skill in the catalogue", slug)
			}
			return fmt.Errorf("resolving skill %q: %w", slug, err)
		}
		o.Skills[i].SkillID = skill.ID
		o.Skills[i].Name = skill.Name
	}
	return nil
}

// validateOpening checks the bar.
//
// Every refusal is CODED. A bare ErrInvalid falls back to invalid_claim at the
// controller, which would tell a hirer their EVIDENCE was rejected when what
// was wrong was a threshold.
func validateOpening(o *domain.Opening) error {
	if o.MinOverallScore != nil && (*o.MinOverallScore < 0 || *o.MinOverallScore > 100) {
		return Coded(ErrInvalid, CodeInvalidOpening,
			"an overall score is between 0 and 100")
	}
	if o.MinGeneralistScore != nil && *o.MinGeneralistScore < 0 {
		// No upper bound: breadth is unbounded by construction (ADR-0007).
		return Coded(ErrInvalid, CodeInvalidOpening,
			"a generalist score cannot be negative")
	}
	if o.MinOSSYOE != nil && (*o.MinOSSYOE < 0 || *o.MinOSSYOE > 80) {
		return Coded(ErrInvalid, CodeInvalidOpening,
			"years in open source must be between 0 and 80")
	}

	// Keyed on whichever the caller gave: a slug before resolution, an id
	// after. Both are the same skill named two ways, and listing one twice is
	// the same mistake either way.
	seen := map[string]bool{}
	for _, s := range o.Skills {
		key := strings.ToLower(strings.TrimSpace(s.Slug))
		if key == "" {
			key = string(s.SkillID)
		}
		if key == "" {
			return Coded(ErrInvalid, CodeInvalidOpening, "name the skill")
		}
		if seen[key] {
			return Coded(ErrInvalid, CodeInvalidOpening,
				"the same skill is listed twice")
		}
		seen[key] = true
		if s.MinScore < 0 || s.MinScore > 100 {
			return Coded(ErrInvalid, CodeInvalidOpening,
				"a skill score is between 0 and 100")
		}
	}
	return nil
}
