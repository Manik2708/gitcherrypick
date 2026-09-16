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
// HiredEmails carries ADDRESSES because that is what a hirer has: they know who
// they hired, not what this platform calls them. The service resolves each
// against the contact requests the organisation was already given.
type closeRequest struct {
	Reason      string   `json:"reason"`
	Note        string   `json:"note"`
	HiredEmails []string `json:"hired_emails"`
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

	out, err := c.roles.Close(r.Context(), p, orgID, roleID, port.CloseRequest{
		Reason: domain.CloseReason(body.Reason), Note: body.Note,
		HiredEmails: body.HiredEmails,
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
	case code == service.CodeRoleNotFound:
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

// --- the contributor's half --------------------------------------------------

// myRoles is GET /me/roles, mounted by MeController.
//
// A contributor's own view: open roles whose countries include theirs, whose
// minimums they meet, whose engagement matches a shape they ticked, and whose
// pay is not below what they asked for. The compensation filter runs HERE,
// where only they can see the result — it never reaches a hirer (ADR-0018 §5).
//
// It answers in the CONTACT-REQUEST shape, not the hirer's. Three of the
// hirer's fields have no business here: `status` is always open by
// construction, `supersedes` is a company's editing history, and `hires` names
// OTHER CONTRIBUTORS — a list of who a company hired, handed to somebody
// browsing openings, would disclose people who agreed to talk to that company
// and not to anybody else (ADR-0019 §16).
func myRoles(roles port.RoleService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := require(w, r, domain.KindContributor)
		if !ok {
			return
		}

		limit, offset := 50, 0
		if raw := r.URL.Query().Get("limit"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed <= 0 || parsed > 200 {
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

		out, err := roles.Matching(r.Context(), domain.UserID(p.Subject()), limit, offset)
		if err != nil {
			writeError(w, err)
			return
		}

		bodies := make([]contactRoleBody, 0, len(out))
		for _, role := range out {
			bodies = append(bodies, contactRoleBodyOf(role))
		}
		writeJSON(w, http.StatusOK, map[string]any{"roles": bodies})
	}
}
