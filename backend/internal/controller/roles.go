package controller

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
	"github.com/Manik2708/gitcherrypick/backend/internal/service"
)

// RoleController serves /orgs/{orgID}/roles and /orgs/{orgID}/settings
// (ADR-0019).
//
// A separate controller from OrganizationController despite sharing a prefix:
// chi mounts one handler per prefix, so the two are composed at the router
// rather than merged here. Seats and openings are different subjects with
// different audiences, and keeping them apart is what stops a roster read
// growing a role field by being one struct away.
type RoleController struct {
	roles port.RoleService
}

// NewRoleController wires openings.
func NewRoleController(roles port.RoleService) *RoleController {
	return &RoleController{roles: roles}
}

var _ port.Controller = (*RoleController)(nil)

// Routes mounts the role surface under /org-roles.
//
// The prefix is not /orgs: chi allows one handler per mount point and
// OrganizationController already holds that one. The paths beneath carry the
// same {orgID} shape, so a client sees one coherent surface.
func (c *RoleController) Routes() (string, http.Handler) {
	r := chi.NewRouter()

	r.Get("/{orgID}/roles", c.list)
	r.Post("/{orgID}/roles", c.create)
	r.Get("/{orgID}/roles/{roleID}", c.get)
	r.Patch("/{orgID}/roles/{roleID}", c.update)
	r.Post("/{orgID}/roles/{roleID}/revise", c.revise)
	r.Post("/{orgID}/roles/{roleID}/open", c.open)
	r.Post("/{orgID}/roles/{roleID}/close", c.close)

	// The advert on a role (ADR-0020). Writing the bar and publishing it are
	// separate, because publishing is the commitment.
	// Who is already on a round for this role — read before searching again,
	// and picked from when closing.
	r.Get("/{orgID}/roles/{roleID}/candidates", c.candidates)

	r.Get("/{orgID}/roles/{roleID}/opening", c.opening)
	r.Put("/{orgID}/roles/{roleID}/opening", c.saveOpening)
	r.Post("/{orgID}/roles/{roleID}/opening/publish", c.publishOpening)
	r.Post("/{orgID}/roles/{roleID}/opening/withdraw", c.withdrawOpening)

	r.Get("/{orgID}/settings", c.settings)
	r.Put("/{orgID}/settings", c.saveSettings)

	return "/org-roles", r
}

// --- shapes ------------------------------------------------------------------

// roleQuestionBody is one yes/no question.
//
// Yes/no only. A free-text question is an interview, and an interview conducted
// before either side has agreed to talk is the asymmetry the consent model
// exists to prevent (ADR-0019 §9).
type roleQuestionBody struct {
	ID       string `json:"id,omitempty"`
	Question string `json:"question"`
	Expected *bool  `json:"expected"`
}

// roleRequest is what a hirer sends. Every optional field is a pointer, so
// "not stated" and "zero" stay distinct: a role open to somebody with no
// commercial experience and a role with no minimum are different invitations.
type roleRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`

	Engagement string `json:"engagement"`
	Location   string `json:"location"`
	AddressID  string `json:"address_id"`

	Currency      string `json:"currency"`
	YearlyCTC     *int64 `json:"yearly_ctc"`
	YearlyBase    *int64 `json:"yearly_base"`
	HourlyRate    *int64 `json:"hourly_rate"`
	ExpectedHours *int   `json:"expected_hours"`

	EligibleCountries []string `json:"eligible_countries"`

	MinOfficeYOE *int `json:"min_office_yoe"`
	MinOSSYOE    *int `json:"min_oss_yoe"`

	RequiresOnlineTest *bool `json:"requires_online_test"`
	MaxInterviewRounds *int  `json:"max_interview_rounds"`
	AvgDaysToOffer     *int  `json:"avg_days_to_offer"`

	Questions []roleQuestionBody `json:"questions"`
}

// role turns a request into the domain type. Validation belongs to the
// service; this only translates.
func (b roleRequest) role() domain.Role {
	out := domain.Role{
		Title:              b.Title,
		Description:        b.Description,
		Engagement:         domain.Engagement(b.Engagement),
		Location:           domain.RoleLocation(b.Location),
		Currency:           b.Currency,
		YearlyCTC:          b.YearlyCTC,
		YearlyBase:         b.YearlyBase,
		HourlyRate:         b.HourlyRate,
		ExpectedHours:      b.ExpectedHours,
		EligibleCountries:  b.EligibleCountries,
		MinOfficeYOE:       b.MinOfficeYOE,
		MinOSSYOE:          b.MinOSSYOE,
		RequiresOnlineTest: b.RequiresOnlineTest,
		MaxInterviewRounds: b.MaxInterviewRounds,
		AvgDaysToOffer:     b.AvgDaysToOffer,
	}
	if b.AddressID != "" {
		id := domain.AddressID(b.AddressID)
		out.AddressID = &id
	}
	for _, q := range b.Questions {
		out.Questions = append(out.Questions, domain.RoleQuestion{
			Question: q.Question, Expected: q.Expected,
		})
	}
	return out
}

// closeRequest is why a role stopped being open.
//
// Hired carries the ids of people ON THIS ROLE'S OWN CANDIDATE LIST, not typed
// addresses. A hirer picks from a list they already hold, which is both easier
// than retyping an email and strictly safer: there is no longer any address to
// guess, so the endpoint cannot be asked whether one has an account here.
type closeRequest struct {
	Reason string   `json:"reason"`
	Note   string   `json:"note"`
	Hired  []string `json:"hired"`
}

// settingsRequest is who may do what with a role.
type settingsRequest struct {
	RoleCreateAuthority string `json:"role_create_authority"`
	RoleUpdateAuthority string `json:"role_update_authority"`
	RoleCloseAuthority  string `json:"role_close_authority"`
}

// roleHireBody names one person a role was filled with.
//
// The USER ID and when, never the email. The organisation already has the
// address — it typed it — and re-emitting it here would put a contributor's
// email in a response that a role list reads in bulk.
type roleHireBody struct {
	UserID     domain.UserID `json:"user_id"`
	RecordedAt time.Time     `json:"recorded_at"`
}

// roleBody is one opening as the API reports it.
type roleBody struct {
	ID     domain.RoleID         `json:"id"`
	OrgID  domain.OrganizationID `json:"organization_id"`
	Status string                `json:"status"`

	Title       string `json:"title"`
	Description string `json:"description"`
	Engagement  string `json:"engagement"`

	Location  string  `json:"location"`
	AddressID *string `json:"address_id"`

	Currency      string `json:"currency"`
	YearlyCTC     *int64 `json:"yearly_ctc"`
	YearlyBase    *int64 `json:"yearly_base"`
	HourlyRate    *int64 `json:"hourly_rate"`
	ExpectedHours *int   `json:"expected_hours"`

	// EligibleCountries EMPTY MEANS ANYWHERE, not "nowhere". A client showing
	// this must say so, or it will read as a role nobody can take.
	EligibleCountries []string `json:"eligible_countries"`

	MinOfficeYOE *int `json:"min_office_yoe"`
	MinOSSYOE    *int `json:"min_oss_yoe"`

	// The process fields. Disclosed to the contacted contributor and to the
	// organisation, and to nobody else (ADR-0017, ADR-0019 §8).
	RequiresOnlineTest *bool `json:"requires_online_test"`
	MaxInterviewRounds *int  `json:"max_interview_rounds"`
	AvgDaysToOffer     *int  `json:"avg_days_to_offer"`

	Questions []roleQuestionBody `json:"questions"`

	OpenedAt *time.Time `json:"opened_at"`

	// CloseRequestedAt is somebody asking, not an outcome: the role is still
	// open, still matching, and still committing the company.
	CloseRequestedAt *time.Time `json:"close_requested_at"`

	ClosedAt    *time.Time `json:"closed_at"`
	CloseReason *string    `json:"close_reason"`
	CloseNote   string     `json:"close_note,omitempty"`

	Hires []roleHireBody `json:"hires"`

	// Advertised is whether a contributor can currently see this role.
	Advertised bool `json:"advertised"`

	// Supersedes is the role this one replaced. An open role is immutable, so a
	// change is a new row — and this is the thread back through its history.
	Supersedes *domain.RoleID `json:"supersedes"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func roleBodyOf(r domain.Role) roleBody {
	out := roleBody{
		ID: r.ID, OrgID: r.OrgID, Status: string(r.Status),
		Title: r.Title, Description: r.Description,
		Engagement: string(r.Engagement), Location: string(r.Location),
		Currency: r.Currency, YearlyCTC: r.YearlyCTC, YearlyBase: r.YearlyBase,
		HourlyRate: r.HourlyRate, ExpectedHours: r.ExpectedHours,
		EligibleCountries: r.EligibleCountries,
		MinOfficeYOE:      r.MinOfficeYOE, MinOSSYOE: r.MinOSSYOE,
		RequiresOnlineTest: r.RequiresOnlineTest,
		MaxInterviewRounds: r.MaxInterviewRounds,
		AvgDaysToOffer:     r.AvgDaysToOffer,
		OpenedAt:           r.OpenedAt,
		CloseRequestedAt:   r.CloseRequestedAt,
		ClosedAt:           r.ClosedAt, CloseNote: r.CloseNote,
		Advertised: r.Advertised,
		Supersedes: r.Supersedes,
		CreatedAt:  r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	if out.EligibleCountries == nil {
		out.EligibleCountries = []string{}
	}
	if r.AddressID != nil {
		id := string(*r.AddressID)
		out.AddressID = &id
	}
	if r.CloseReason != nil {
		reason := string(*r.CloseReason)
		out.CloseReason = &reason
	}

	out.Questions = make([]roleQuestionBody, 0, len(r.Questions))
	for _, q := range r.Questions {
		out.Questions = append(out.Questions, roleQuestionBody{
			ID: q.ID, Question: q.Question, Expected: q.Expected,
		})
	}

	out.Hires = make([]roleHireBody, 0, len(r.Hires))
	for _, h := range r.Hires {
		out.Hires = append(out.Hires, roleHireBody{
			UserID: h.UserID, RecordedAt: h.RecordedAt,
		})
	}
	return out
}

type settingsBody struct {
	RoleCreateAuthority string `json:"role_create_authority"`
	RoleUpdateAuthority string `json:"role_update_authority"`
	RoleCloseAuthority  string `json:"role_close_authority"`
}

func settingsBodyOf(s domain.OrgSettings) settingsBody {
	return settingsBody{
		RoleCreateAuthority: string(s.RoleCreateAuthority),
		RoleUpdateAuthority: string(s.RoleUpdateAuthority),
		RoleCloseAuthority:  string(s.RoleCloseAuthority),
	}
}

// --- handlers ----------------------------------------------------------------

// roleIDFrom reads and validates the role in the path.
//
// A malformed id is 400 rather than 404: a typo and a role that does not exist
// are different mistakes, and answering the same to both sends somebody looking
// for a role they never had.
func roleIDFrom(w http.ResponseWriter, r *http.Request) (domain.RoleID, bool) {
	raw := chi.URLParam(r, "roleID")
	if !isUUID(raw) {
		writeCode(w, http.StatusBadRequest, service.CodeInvalidID)
		return "", false
	}
	return domain.RoleID(raw), true
}

// roleContext resolves the principal and the organisation.
func (c *RoleController) roleContext(w http.ResponseWriter, r *http.Request) (domain.Principal, domain.OrganizationID, bool) {
	p, ok := require(w, r, domain.KindHirer)
	if !ok {
		return domain.Principal{}, "", false
	}
	orgID, valid := orgIDFrom(w, r)
	if !valid {
		return domain.Principal{}, "", false
	}
	return p, orgID, true
}

func (c *RoleController) create(w http.ResponseWriter, r *http.Request) {
	p, orgID, ok := c.roleContext(w, r)
	if !ok {
		return
	}

	var body roleRequest
	if err := decode(w, r, &body); err != nil {
		// 400, not 422: the body could not be READ. 422 is for a body that
		// parsed and then failed a rule.
		writeCode(w, http.StatusBadRequest, service.CodeInvalidRole)
		return
	}

	out, err := c.roles.Create(r.Context(), p, orgID, body.role())
	if err != nil {
		c.writeRoleError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, roleBodyOf(*out))
}

func (c *RoleController) update(w http.ResponseWriter, r *http.Request) {
	p, orgID, ok := c.roleContext(w, r)
	if !ok {
		return
	}
	roleID, valid := roleIDFrom(w, r)
	if !valid {
		return
	}

	var body roleRequest
	if err := decode(w, r, &body); err != nil {
		writeCode(w, http.StatusBadRequest, service.CodeInvalidRole)
		return
	}

	out, err := c.roles.Update(r.Context(), p, orgID, roleID, body.role())
	if err != nil {
		c.writeRoleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, roleBodyOf(*out))
}

// revise publishes a successor to an open role.
//
// 201, not 200: what comes back is a NEW role with a new id. Answering 200
// would suggest the one in the path had changed, which is the exact belief this
// endpoint exists to prevent.
func (c *RoleController) revise(w http.ResponseWriter, r *http.Request) {
	p, orgID, ok := c.roleContext(w, r)
	if !ok {
		return
	}
	roleID, valid := roleIDFrom(w, r)
	if !valid {
		return
	}

	var body roleRequest
	if err := decode(w, r, &body); err != nil {
		writeCode(w, http.StatusBadRequest, service.CodeInvalidRole)
		return
	}

	out, err := c.roles.Revise(r.Context(), p, orgID, roleID, body.role())
	if err != nil {
		c.writeRoleError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, roleBodyOf(*out))
}

func (c *RoleController) open(w http.ResponseWriter, r *http.Request) {
	p, orgID, ok := c.roleContext(w, r)
	if !ok {
		return
	}
	roleID, valid := roleIDFrom(w, r)
	if !valid {
		return
	}

	out, err := c.roles.Open(r.Context(), p, orgID, roleID)
	if err != nil {
		c.writeRoleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, roleBodyOf(*out))
}

// close ends a role, or records that somebody asked to.
//
// Both answer 200 with the role: under draft_and_approve the caller staged a
// request and the role is still open, which the body says plainly through
// `status` and `close_requested_at`. A 202 would be vaguer, not clearer.
func (c *RoleController) close(w http.ResponseWriter, r *http.Request) {
	p, orgID, ok := c.roleContext(w, r)
	if !ok {
		return
	}
	roleID, valid := roleIDFrom(w, r)
	if !valid {
		return
	}

	var body closeRequest
	if err := decode(w, r, &body); err != nil {
		writeCode(w, http.StatusBadRequest, service.CodeInvalidRole)
		return
	}

	hired := make([]domain.UserID, 0, len(body.Hired))
	for _, id := range body.Hired {
		hired = append(hired, domain.UserID(id))
	}

	out, err := c.roles.Close(r.Context(), p, orgID, roleID, port.CloseRequest{
		Reason: domain.CloseReason(body.Reason), Note: body.Note, Hired: hired,
	})
	if err != nil {
		c.writeRoleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, roleBodyOf(*out))
}

func (c *RoleController) list(w http.ResponseWriter, r *http.Request) {
	p, orgID, ok := c.roleContext(w, r)
	if !ok {
		return
	}

	var status *domain.RoleStatus
	if raw := r.URL.Query().Get("status"); raw != "" {
		s := domain.RoleStatus(raw)
		switch s {
		case domain.RoleDraft, domain.RoleOpen, domain.RoleClosed:
			status = &s
		default:
			writeCode(w, http.StatusBadRequest, service.CodeInvalidQuery)
			return
		}
	}

	out, err := c.roles.List(r.Context(), p, orgID, status)
	if err != nil {
		c.writeRoleError(w, err)
		return
	}

	bodies := make([]roleBody, 0, len(out))
	for _, role := range out {
		bodies = append(bodies, roleBodyOf(role))
	}
	writeJSON(w, http.StatusOK, map[string]any{"roles": bodies})
}

func (c *RoleController) get(w http.ResponseWriter, r *http.Request) {
	p, orgID, ok := c.roleContext(w, r)
	if !ok {
		return
	}
	roleID, valid := roleIDFrom(w, r)
	if !valid {
		return
	}

	out, err := c.roles.Role(r.Context(), p, orgID, roleID)
	if err != nil {
		c.writeRoleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, roleBodyOf(*out))
}

func (c *RoleController) settings(w http.ResponseWriter, r *http.Request) {
	p, orgID, ok := c.roleContext(w, r)
	if !ok {
		return
	}

	out, err := c.roles.Settings(r.Context(), p, orgID)
	if err != nil {
		c.writeRoleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, settingsBodyOf(*out))
}

func (c *RoleController) saveSettings(w http.ResponseWriter, r *http.Request) {
	p, orgID, ok := c.roleContext(w, r)
	if !ok {
		return
	}

	var body settingsRequest
	if err := decode(w, r, &body); err != nil {
		writeCode(w, http.StatusBadRequest, service.CodeInvalidRole)
		return
	}

	out, err := c.roles.SaveSettings(r.Context(), p, orgID, domain.OrgSettings{
		RoleCreateAuthority: domain.RoleAuthority(body.RoleCreateAuthority),
		RoleUpdateAuthority: domain.RoleAuthority(body.RoleUpdateAuthority),
		RoleCloseAuthority:  domain.RoleAuthority(body.RoleCloseAuthority),
	})
	if err != nil {
		c.writeRoleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, settingsBodyOf(*out))
}

// writeRoleError maps a service refusal to a status.
//
// role_not_found rather than a bare 404, so a caller can tell a missing role
// from a missing organisation without reading prose.
func (c *RoleController) writeRoleError(w http.ResponseWriter, err error) {
	code := service.CodeOf(err)
	switch {
	case code == service.CodeRoleNotFound, code == service.CodeOpeningNotFound:
		writeCode(w, http.StatusNotFound, code)
	case code == service.CodeRoleImmutable:
		writeCode(w, http.StatusConflict, code)
	case code == service.CodeOwnerApprovalRequired:
		writeCode(w, http.StatusForbidden, code)
	case errors.Is(err, service.ErrForbidden):
		if code == "" {
			code = service.CodeForbidden
		}
		writeCode(w, http.StatusForbidden, code)
	case code == "" && isNotFound(err):
		writeCode(w, http.StatusNotFound, service.CodeRoleNotFound)
	default:
		writeError(w, err)
	}
}

// roleCandidateBody is somebody already on a round for this role.
//
// No email, ever — not even for an accepted request. The hirer who needs it
// has it from the contact request itself, and a list read in bulk is the wrong
// place to hand out addresses (ADR-0005 §9).
type roleCandidateBody struct {
	UserID      domain.UserID `json:"user_id"`
	DisplayName string        `json:"display_name"`
	GitHubLogin string        `json:"github_login,omitempty"`

	ShortlistID   domain.ShortlistID `json:"shortlist_id"`
	ShortlistName string             `json:"shortlist_name"`

	// Empty while still STAGED — told nothing, and removable.
	ContactStatus string     `json:"contact_status"`
	NotifiedAt    *time.Time `json:"notified_at"`

	// Accepted is the only state a hire may be recorded from.
	Accepted bool `json:"accepted"`
}

// candidates lists everybody already approached for this role.
func (c *RoleController) candidates(w http.ResponseWriter, r *http.Request) {
	p, orgID, ok := c.roleContext(w, r)
	if !ok {
		return
	}
	roleID, valid := roleIDFrom(w, r)
	if !valid {
		return
	}

	out, err := c.roles.Candidates(r.Context(), p, orgID, roleID)
	if err != nil {
		c.writeRoleError(w, err)
		return
	}

	bodies := make([]roleCandidateBody, 0, len(out))
	for _, x := range out {
		bodies = append(bodies, roleCandidateBody{
			UserID: x.UserID, DisplayName: x.DisplayName, GitHubLogin: x.GitHubLogin,
			ShortlistID: x.ShortlistID, ShortlistName: x.ShortlistName,
			ContactStatus: string(x.ContactStatus), NotifiedAt: x.NotifiedAt,
			Accepted: x.Accepted,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"candidates": bodies})
}

/* --- public openings (ADR-0020) --------------------------------------------- */

// openingSkillBody is one skill and the score it asks for.
// openingSkillBody is one skill and the score it asks for.
//
// A hirer names a SLUG — "go" — because the catalogue is slug-keyed everywhere
// a human touches it: search, claims, the leaderboard. The id comes back on a
// read for a client that wants it, and is ignored on a write.
type openingSkillBody struct {
	Slug     string  `json:"slug"`
	SkillID  string  `json:"skill_id,omitempty"`
	Name     string  `json:"name,omitempty"`
	MinScore float64 `json:"min_score"`
}

// openingRequest is the bar a hirer sets.
//
// Every threshold is a pointer, because NULL means "no bar" and 0 means "a bar
// of zero" — a role open to anybody and one that has decided to accept the
// lowest score on the platform are different invitations.
type openingRequest struct {
	MinOverallScore    *float64           `json:"min_overall_score"`
	MinGeneralistScore *float64           `json:"min_generalist_score"`
	MinOSSYOE          *int               `json:"min_oss_yoe"`
	Skills             []openingSkillBody `json:"skills"`
}

// openingBody is an advert as the API reports it.
//
// No view count, and there never will be: reading an opening sends nothing,
// and a counter would become a ranking signal nobody consented to produce
// (ADR-0020 §8).
type openingBody struct {
	ID     domain.OpeningID `json:"id"`
	RoleID domain.RoleID    `json:"role_id"`

	MinOverallScore    *float64           `json:"min_overall_score"`
	MinGeneralistScore *float64           `json:"min_generalist_score"`
	MinOSSYOE          *int               `json:"min_oss_yoe"`
	Skills             []openingSkillBody `json:"skills"`

	// Live is published and not withdrawn, reported rather than left for a
	// client to derive from two timestamps and get subtly wrong.
	Live        bool       `json:"live"`
	PublishedAt *time.Time `json:"published_at"`
	WithdrawnAt *time.Time `json:"withdrawn_at"`
}

func openingBodyOf(o domain.Opening) openingBody {
	out := openingBody{
		ID: o.ID, RoleID: o.RoleID,
		MinOverallScore: o.MinOverallScore, MinGeneralistScore: o.MinGeneralistScore,
		MinOSSYOE: o.MinOSSYOE,
		Live:      o.Live(), PublishedAt: o.PublishedAt, WithdrawnAt: o.WithdrawnAt,
	}
	out.Skills = make([]openingSkillBody, 0, len(o.Skills))
	for _, s := range o.Skills {
		out.Skills = append(out.Skills, openingSkillBody{
			SkillID: string(s.SkillID), Slug: s.Slug, Name: s.Name, MinScore: s.MinScore,
		})
	}
	return out
}

// contributorOpeningBody is an advert as the person reading it sees it.
//
// The role comes through in the CONTACT-REQUEST shape (ADR-0019 §2): no
// status, no supersede chain, and no `hires`, which names other contributors.
// The bar is included because they have already cleared it, so it discloses
// nothing they could not infer — and a role that states what it wanted is more
// legible than one that simply appeared.
type contributorOpeningBody struct {
	ID           domain.OpeningID `json:"id"`
	Organization string           `json:"organization"`
	Role         *contactRoleBody `json:"role"`
	Bar          openingBarBody   `json:"bar"`

	// PostedAt is the ROLE'S opened_at, not the advert's. A company that
	// advertised late should not look fresher than one that published at once
	// (ADR-0020 §10).
	PostedAt *time.Time `json:"posted_at"`
}

type openingBarBody struct {
	MinOverallScore    *float64           `json:"min_overall_score"`
	MinGeneralistScore *float64           `json:"min_generalist_score"`
	MinOSSYOE          *int               `json:"min_oss_yoe"`
	Skills             []openingSkillBody `json:"skills"`
}

func contributorOpeningBodyOf(o domain.Opening) contributorOpeningBody {
	out := contributorOpeningBody{
		ID: o.ID, Organization: o.OrganizationName,
		Bar: openingBarBody{
			MinOverallScore: o.MinOverallScore, MinGeneralistScore: o.MinGeneralistScore,
			MinOSSYOE: o.MinOSSYOE,
			Skills:    make([]openingSkillBody, 0, len(o.Skills)),
		},
	}
	for _, s := range o.Skills {
		out.Bar.Skills = append(out.Bar.Skills, openingSkillBody{
			SkillID: string(s.SkillID), Slug: s.Slug, Name: s.Name, MinScore: s.MinScore,
		})
	}
	if o.Role != nil {
		role := contactRoleBodyOf(*o.Role)
		out.Role = &role
		out.PostedAt = o.Role.OpenedAt
	}
	return out
}

// opening returns the advert on a role.
func (c *RoleController) opening(w http.ResponseWriter, r *http.Request) {
	p, orgID, ok := c.roleContext(w, r)
	if !ok {
		return
	}
	roleID, valid := roleIDFrom(w, r)
	if !valid {
		return
	}

	out, err := c.roles.Opening(r.Context(), p, orgID, roleID)
	if err != nil {
		c.writeRoleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, openingBodyOf(*out))
}

// saveOpening writes the bar. PUT, because the form submits every threshold it
// shows and a merge would leave a skill somebody had just removed.
func (c *RoleController) saveOpening(w http.ResponseWriter, r *http.Request) {
	p, orgID, ok := c.roleContext(w, r)
	if !ok {
		return
	}
	roleID, valid := roleIDFrom(w, r)
	if !valid {
		return
	}

	var body openingRequest
	if err := decode(w, r, &body); err != nil {
		writeCode(w, http.StatusBadRequest, service.CodeInvalidOpening)
		return
	}

	in := domain.Opening{
		MinOverallScore:    body.MinOverallScore,
		MinGeneralistScore: body.MinGeneralistScore,
		MinOSSYOE:          body.MinOSSYOE,
	}
	for _, s := range body.Skills {
		in.Skills = append(in.Skills, domain.OpeningSkill{
			Slug: s.Slug, MinScore: s.MinScore,
		})
	}

	out, err := c.roles.SaveOpening(r.Context(), p, orgID, roleID, in)
	if err != nil {
		c.writeRoleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, openingBodyOf(*out))
}

func (c *RoleController) publishOpening(w http.ResponseWriter, r *http.Request) {
	p, orgID, ok := c.roleContext(w, r)
	if !ok {
		return
	}
	roleID, valid := roleIDFrom(w, r)
	if !valid {
		return
	}

	out, err := c.roles.PublishOpening(r.Context(), p, orgID, roleID)
	if err != nil {
		c.writeRoleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, openingBodyOf(*out))
}

func (c *RoleController) withdrawOpening(w http.ResponseWriter, r *http.Request) {
	p, orgID, ok := c.roleContext(w, r)
	if !ok {
		return
	}
	roleID, valid := roleIDFrom(w, r)
	if !valid {
		return
	}

	out, err := c.roles.WithdrawOpening(r.Context(), p, orgID, roleID)
	if err != nil {
		c.writeRoleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, openingBodyOf(*out))
}

// myOpenings is GET /openings, mounted by MeController.
//
// What this contributor CLEARS, and a bare count of what they do not. The
// count is the whole of the compromise: an empty list on its own lies about
// why it is empty — nobody hiring, or nothing they qualify for — and the
// number answers that without itemising anybody's shortfall or exposing a
// company's bar (ADR-0020 §6).
//
// There is NO WRITE PATH from here. Reading an opening sends nothing.
func myOpenings(roles port.RoleService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := require(w, r, domain.KindContributor)
		if !ok {
			return
		}

		limit, offset := 25, 0
		if raw := r.URL.Query().Get("limit"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed <= 0 || parsed > 100 {
				writeCode(w, http.StatusBadRequest, service.CodeInvalidQuery)
				return
			}
			limit = parsed
		}
		if raw := r.URL.Query().Get("offset"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 0 {
				writeCode(w, http.StatusBadRequest, service.CodeInvalidQuery)
				return
			}
			offset = parsed
		}

		out, err := roles.Openings(r.Context(), domain.UserID(p.Subject()), limit, offset)
		if err != nil {
			writeError(w, err)
			return
		}

		bodies := make([]contributorOpeningBody, 0, len(out.Openings))
		for _, o := range out.Openings {
			bodies = append(bodies, contributorOpeningBodyOf(o))
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"openings": bodies,
			"matched":  out.Matched,
			"missed":   out.Missed,
		})
	}
}
