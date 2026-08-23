package controller

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
	"github.com/Manik2708/gitcherrypick/backend/internal/service"
)

// estimatedResultHours is what a contributor is told to expect after submitting.
//
// The Batch API is asynchronous and may take up to 24 hours (ADR-0006), so this
// is a promise about the ceiling rather than a prediction.
const estimatedResultHours = 24

// ClaimController serves /claims.
type ClaimController struct {
	claims port.ClaimService
	reeval port.ReevaluationService
}

// NewClaimController wires the claim lifecycle.
func NewClaimController(claims port.ClaimService, reeval port.ReevaluationService) *ClaimController {
	return &ClaimController{claims: claims, reeval: reeval}
}

var _ port.Controller = (*ClaimController)(nil)

// Routes mounts /claims. Every route is contributor-only: a claim is evidence
// about oneself, and there is no path by which anyone else edits one.
func (c *ClaimController) Routes() (string, http.Handler) {
	r := chi.NewRouter()

	r.Post("/", c.create)
	r.Get("/", c.list)

	r.Route("/{claimID}", func(r chi.Router) {
		r.Get("/", c.get)
		r.Put("/", c.replace)

		r.Post("/evidence/prs", c.setPREvidence)
		r.Post("/evidence/projects", c.setProjectEvidence)
		r.Post("/skills", c.setSkills)

		r.Post("/submit", c.submit)
		r.Get("/withdraw-preview", c.withdrawPreview)
		r.Post("/withdraw", c.withdraw)

		r.Post("/suggestions/{skillID}/accept", c.decideSuggestion(true))
		r.Post("/suggestions/{skillID}/dismiss", c.decideSuggestion(false))

		r.Post("/reevaluation", c.requestReevaluation)
	})

	return "/claims", r
}

// --- shapes ------------------------------------------------------------------

// prEvidenceRequest is one PR as a client submits it.
//
// A URL rather than owner/name/number, because that is what a contributor has
// in their clipboard. Parsing it here is IO validation, which is exactly what a
// controller is for.
type prEvidenceRequest struct {
	Position int    `json:"position"`
	URL      string `json:"url"`
	Role     string `json:"role"`
}

type prEvidenceListRequest struct {
	PRs []prEvidenceRequest `json:"prs"`
}

// maintainerRequest is a contributor's declaration of maintainer status.
//
// GitHub exposes no maintainer list, so the contributor names where the truth
// lives and the model checks it (ADR-0003). Nothing here grants the reach bonus
// — that follows the model's verdict, not the declaration.
type maintainerRequest struct {
	Sources       []string `json:"sources"`
	OtherEvidence string   `json:"other_evidence"`
}

type projectEvidenceRequest struct {
	URL                 string             `json:"url"`
	ContributionSummary string             `json:"contribution_summary"`
	Maintainer          *maintainerRequest `json:"maintainer,omitempty"`
}

type projectEvidenceListRequest struct {
	Projects []projectEvidenceRequest `json:"projects"`
}

type claimSkillRequest struct {
	Slug             string `json:"slug"`
	NominatedPrimary bool   `json:"nominated_primary"`
	Rationale        string `json:"rationale"`
}

type claimSkillListRequest struct {
	Skills []claimSkillRequest `json:"skills"`
}

// replaceClaimRequest is the whole-claim edit.
//
// Version is required and checked optimistically: two tabs editing one claim
// must not silently overwrite each other (ADR-0003).
type replaceClaimRequest struct {
	Version  int                      `json:"version"`
	PRs      []prEvidenceRequest      `json:"prs"`
	Projects []projectEvidenceRequest `json:"projects"`
	Skills   []claimSkillRequest      `json:"skills"`
}

type withdrawRequest struct {
	ConfirmDemotion bool `json:"confirm_demotion"`
}

type reevaluationRequestBody struct {
	Reason string `json:"reason"`
}

type prEvidenceBody struct {
	Position  int    `json:"position"`
	RepoOwner string `json:"repo_owner"`
	RepoName  string `json:"repo_name"`
	PRNumber  int    `json:"pr_number"`
	Role      string `json:"role"`
}

// projectEvidenceBody is one supporting project.
//
// The maintainer declaration is reported as two FLAT fields rather than a
// nested object, because a client only ever asks two things about it: was one
// made, and did the model uphold it.
//
// MaintainerValidated is a pointer: null means the model has not ruled yet,
// which is different from ruling against it — and only a true verdict applies
// the reach bonus (ADR-0005).
type projectEvidenceBody struct {
	RepoOwner           string `json:"repo_owner"`
	RepoName            string `json:"repo_name"`
	MaintainerDeclared  bool   `json:"maintainer_declared"`
	MaintainerValidated *bool  `json:"maintainer_validated"`
}

// claimSkillBody is one declared or suggested skill.
//
// Named by SLUG, not by id. The slug is the stable public identifier a
// contributor claims and a scorecard shows; the uuid is a storage detail, and
// putting it on the wire would invite a client to key on it.
type claimSkillBody struct {
	Slug             string     `json:"slug"`
	Origin           string     `json:"origin"`
	NominatedPrimary bool       `json:"nominated_primary"`
	AcceptedAt       *time.Time `json:"accepted_at,omitempty"`
	DismissedAt      *time.Time `json:"dismissed_at,omitempty"`
}

type claimBody struct {
	ID               domain.ClaimID        `json:"id"`
	Status           string                `json:"status"`
	Version          int                   `json:"version"`
	NominatedPrimary string                `json:"nominated_primary,omitempty"`
	PRs              []prEvidenceBody      `json:"prs,omitempty"`
	Projects         []projectEvidenceBody `json:"projects,omitempty"`
	Skills           []claimSkillBody      `json:"skills,omitempty"`
	SubmittedAt      *time.Time            `json:"submitted_at,omitempty"`
	EvaluatedAt      *time.Time            `json:"evaluated_at,omitempty"`
	LockedUntil      *time.Time            `json:"locked_until,omitempty"`
	CreatedAt        time.Time             `json:"created_at"`
}

// claimSummary is the list view: counts rather than the evidence itself.
type claimSummary struct {
	ID               domain.ClaimID `json:"id"`
	Status           string         `json:"status"`
	Version          int            `json:"version"`
	NominatedPrimary string         `json:"nominated_primary,omitempty"`
	PRCount          int            `json:"pr_count"`
	SkillCount       int            `json:"skill_count"`
	SubmittedAt      *time.Time     `json:"submitted_at"`
	EvaluatedAt      *time.Time     `json:"evaluated_at"`
	CreatedAt        time.Time      `json:"created_at"`
}

// submitAcceptedBody is the 202 for a queued claim.
type submitAcceptedBody struct {
	ID                         domain.ClaimID `json:"id"`
	Status                     string         `json:"status"`
	Version                    int            `json:"version"`
	SubmittedAt                *time.Time     `json:"submitted_at"`
	EstimatedResultWithinHours int            `json:"estimated_result_within_hours"`
}

// validationFailureBody names one failing evidence row.
//
// Every failure is reported, not just the first: the contributor is doing
// curation work and a vague rejection wastes it (port.ValidationFailure).
type validationFailureBody struct {
	Position           int             `json:"position"`
	Reason             string          `json:"reason"`
	Message            string          `json:"message"`
	ConflictingClaimID *domain.ClaimID `json:"conflicting_claim_id,omitempty"`
}

type demotionBody struct {
	Skill                string `json:"skill"`
	From                 string `json:"from"`
	To                   string `json:"to"`
	DistinctPRCountAfter int    `json:"distinct_pr_count_after"`
	Message              string `json:"message"`
}

// --- handlers ----------------------------------------------------------------

func (c *ClaimController) create(w http.ResponseWriter, r *http.Request) {
	p, ok := require(w, r, domain.KindContributor)
	if !ok {
		return
	}

	claim, err := c.claims.Create(r.Context(), p.Contributor.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, claimBodyOf(claim))
}

func (c *ClaimController) list(w http.ResponseWriter, r *http.Request) {
	p, ok := require(w, r, domain.KindContributor)
	if !ok {
		return
	}

	claims, err := c.claims.List(r.Context(), p.Contributor.ID)
	if err != nil {
		writeError(w, err)
		return
	}

	summaries := make([]claimSummary, 0, len(claims))
	for i := range claims {
		summaries = append(summaries, summarizeClaim(&claims[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"total": len(summaries), "claims": summaries})
}

func (c *ClaimController) get(w http.ResponseWriter, r *http.Request) {
	p, claimID, ok := c.claimContext(w, r)
	if !ok {
		return
	}

	claim, err := c.claims.Get(r.Context(), p.Contributor.ID, claimID)
	if err != nil {
		c.writeClaimError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, claimBodyOf(claim))
}

func (c *ClaimController) replace(w http.ResponseWriter, r *http.Request) {
	p, claimID, ok := c.claimContext(w, r)
	if !ok {
		return
	}

	var body replaceClaimRequest
	if err := decode(w, r, &body); err != nil {
		writeCode(w, http.StatusUnprocessableEntity, service.CodeInvalidClaim)
		return
	}

	prs, err := parsePREvidence(body.PRs)
	if err != nil {
		writeCode(w, http.StatusUnprocessableEntity, service.CodeInvalidEvidence)
		return
	}
	projects, err := parseProjectEvidence(body.Projects)
	if err != nil {
		writeCode(w, http.StatusUnprocessableEntity, service.CodeInvalidEvidence)
		return
	}

	claim, err := c.claims.Replace(r.Context(), p.Contributor.ID, claimID, body.Version, &domain.Claim{
		PREvidence:      prs,
		ProjectEvidence: projects,
		Skills:          parseClaimSkills(body.Skills),
	})
	if err != nil {
		c.writeClaimError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, claimBodyOf(claim))
}

func (c *ClaimController) setPREvidence(w http.ResponseWriter, r *http.Request) {
	p, claimID, ok := c.claimContext(w, r)
	if !ok {
		return
	}

	var body prEvidenceListRequest
	if err := decode(w, r, &body); err != nil {
		writeCode(w, http.StatusUnprocessableEntity, service.CodeInvalidEvidence)
		return
	}

	prs, err := parsePREvidence(body.PRs)
	if err != nil {
		writeCode(w, http.StatusUnprocessableEntity, service.CodeInvalidEvidence)
		return
	}

	claim, err := c.claims.SetPREvidence(r.Context(), p.Contributor.ID, claimID, prs)
	if err != nil {
		c.writeClaimError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"prs": prBodies(claim.PREvidence)})
}

func (c *ClaimController) setProjectEvidence(w http.ResponseWriter, r *http.Request) {
	p, claimID, ok := c.claimContext(w, r)
	if !ok {
		return
	}

	var body projectEvidenceListRequest
	if err := decode(w, r, &body); err != nil {
		writeCode(w, http.StatusUnprocessableEntity, service.CodeInvalidEvidence)
		return
	}

	projects, err := parseProjectEvidence(body.Projects)
	if err != nil {
		writeCode(w, http.StatusUnprocessableEntity, service.CodeInvalidEvidence)
		return
	}

	claim, err := c.claims.SetProjectEvidence(r.Context(), p.Contributor.ID, claimID, projects)
	if err != nil {
		c.writeClaimError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": projectBodies(claim.ProjectEvidence)})
}

func (c *ClaimController) setSkills(w http.ResponseWriter, r *http.Request) {
	p, claimID, ok := c.claimContext(w, r)
	if !ok {
		return
	}

	var body claimSkillListRequest
	if err := decode(w, r, &body); err != nil {
		writeCode(w, http.StatusUnprocessableEntity, service.CodeInvalidClaim)
		return
	}

	claim, err := c.claims.SetSkills(r.Context(), p.Contributor.ID, claimID, parseClaimSkills(body.Skills))
	if err != nil {
		c.writeClaimError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"skills": skillBodies(claim.Skills)})
}

// submit queues the claim, or reports every reason it cannot be.
//
// 202 rather than 200: the judgement is asynchronous, and a 200 would suggest
// the work finished. Validation failures are 422 with the full list.
func (c *ClaimController) submit(w http.ResponseWriter, r *http.Request) {
	p, claimID, ok := c.claimContext(w, r)
	if !ok {
		return
	}

	claim, failures, err := c.claims.Submit(r.Context(), p.Contributor.ID, claimID)
	if len(failures) > 0 {
		writeDetail(w, http.StatusUnprocessableEntity, map[string]any{
			"error":    service.CodeInvalidEvidence,
			"failures": failureBodies(failures),
		})
		return
	}
	if err != nil {
		c.writeClaimError(w, err)
		return
	}

	writeJSON(w, http.StatusAccepted, submitAcceptedBody{
		ID: claim.ID, Status: string(claim.Status), Version: claim.Version,
		SubmittedAt:                claim.SubmittedAt,
		EstimatedResultWithinHours: estimatedResultHours,
	})
}

// withdrawPreview names the skills that would demote, so withdrawal warns
// before it costs something (port.ClaimService).
func (c *ClaimController) withdrawPreview(w http.ResponseWriter, r *http.Request) {
	p, claimID, ok := c.claimContext(w, r)
	if !ok {
		return
	}

	demoting, err := c.claims.WithdrawPreview(r.Context(), p.Contributor.ID, claimID)
	if err != nil {
		c.writeClaimError(w, err)
		return
	}

	out := make([]demotionBody, 0, len(demoting))
	for _, s := range demoting {
		out = append(out, demotionBody{
			Skill: s.Slug,
			From:  string(domain.Primary),
			To:    string(domain.Secondary),
			// The count AFTER this claim's evidence is removed, which is what
			// decides whether the skill still reaches the threshold.
			DistinctPRCountAfter: s.DistinctPRCount,
			Message: fmt.Sprintf("Withdrawing this claim removes %s from ranking.",
				strings.ToUpper(s.Slug[:1])+s.Slug[1:]),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"demotions": out})
}

func (c *ClaimController) withdraw(w http.ResponseWriter, r *http.Request) {
	p, claimID, ok := c.claimContext(w, r)
	if !ok {
		return
	}

	var body withdrawRequest
	if err := decodeOptional(w, r, &body); err != nil {
		writeCode(w, http.StatusUnprocessableEntity, service.CodeInvalidClaim)
		return
	}

	claim, err := c.claims.Withdraw(r.Context(), p.Contributor.ID, claimID, body.ConfirmDemotion)
	if err != nil {
		c.writeClaimError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, claimBodyOf(claim))
}

// decideSuggestion builds the accept and dismiss handlers.
func (c *ClaimController) decideSuggestion(accept bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, claimID, ok := c.claimContext(w, r)
		if !ok {
			return
		}
		skillID := domain.SkillID(chi.URLParam(r, "skillID"))

		skill, err := c.claims.DecideSuggestion(r.Context(), p.Contributor.ID, claimID, skillID, accept)
		if err != nil {
			c.writeClaimError(w, err)
			return
		}

		// Origin stays ai_suggested: it records how the skill ARRIVED, while
		// accepted_at records that the contributor chose it. Overwriting the
		// origin would erase the distinction the scorecard depends on.
		body := map[string]any{"slug": skill.Slug, "origin": string(domain.AISuggested)}
		now := time.Now().UTC()
		if accept {
			body["accepted_at"] = now
		} else {
			body["dismissed_at"] = now
		}
		writeJSON(w, http.StatusOK, body)
	}
}

func (c *ClaimController) requestReevaluation(w http.ResponseWriter, r *http.Request) {
	p, claimID, ok := c.claimContext(w, r)
	if !ok {
		return
	}

	var body reevaluationRequestBody
	if err := decode(w, r, &body); err != nil {
		writeCode(w, http.StatusUnprocessableEntity, service.CodeReasonRequired)
		return
	}

	request, err := c.reeval.Request(r.Context(), p.Contributor.ID, claimID, body.Reason)
	if err != nil {
		c.writeClaimError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":         request.ID,
		"claim_id":   request.ClaimID,
		"status":     string(request.Status),
		"created_at": request.CreatedAt,
	})
}

// --- helpers -----------------------------------------------------------------

// claimContext extracts the contributor and a well-formed claim id.
//
// A malformed id is 400 rather than 404: `not-a-uuid` is a bad request, and
// reporting it as absent would suggest the resource might exist under some
// other spelling.
func (c *ClaimController) claimContext(w http.ResponseWriter, r *http.Request) (domain.Principal, domain.ClaimID, bool) {
	p, ok := require(w, r, domain.KindContributor)
	if !ok {
		return domain.Principal{}, "", false
	}

	raw := chi.URLParam(r, "claimID")
	if !isUUID(raw) {
		writeCode(w, http.StatusBadRequest, service.CodeInvalidID)
		return domain.Principal{}, "", false
	}
	return p, domain.ClaimID(raw), true
}

// writeClaimError names the resource, which statusFor cannot.
//
// The service returns a generic ErrNotFound; only this controller knows the
// caller was asking about a claim.
func (c *ClaimController) writeClaimError(w http.ResponseWriter, err error) {
	if service.CodeOf(err) == "" && isNotFound(err) {
		writeCode(w, http.StatusNotFound, service.CodeClaimNotFound)
		return
	}
	writeError(w, err)
}

// prURL matches a GitHub pull request link.
//
// Anchored, and tolerant of a trailing slash or query string — a contributor
// pastes what their browser shows, which often carries #discussion anchors.
var prURL = regexp.MustCompile(`^/([^/]+)/([^/]+)/pull/(\d+)/?$`)

// parsePREvidence turns submitted URLs into domain evidence.
//
// Returns the first error rather than every one: unlike a validation failure,
// a malformed URL is a client bug and the client fixes them all at once.
//
// An ERROR rather than the offending string, because the offending string may
// legitimately be empty — signalling failure with it made a missing url read as
// success.
func parsePREvidence(in []prEvidenceRequest) ([]domain.PREvidence, error) {
	out := make([]domain.PREvidence, 0, len(in))

	for _, pr := range in {
		owner, name, number, err := parsePRURL(pr.URL)
		if err != nil {
			return nil, err
		}

		role := domain.EvidenceRole(pr.Role)
		if role == "" {
			// Authorship is the default because it is the overwhelmingly
			// common claim; a reviewer says so explicitly (ADR-0003).
			role = domain.RoleAuthor
		}
		out = append(out, domain.PREvidence{
			Position: pr.Position, RepoOwner: owner, RepoName: name,
			PRNumber: number, Role: role,
		})
	}
	return out, nil
}

func parsePRURL(raw string) (owner, name string, number int, err error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", "", 0, fmt.Errorf("unparseable url %q: %w", raw, err)
	}

	match := prURL.FindStringSubmatch(parsed.Path)
	if match == nil {
		return "", "", 0, fmt.Errorf("%q is not a pull request url", raw)
	}
	number, err = strconv.Atoi(match[3])
	if err != nil {
		return "", "", 0, fmt.Errorf("%q has no pr number: %w", raw, err)
	}
	return match[1], match[2], number, nil
}

// repoURL matches a repository link, for supporting projects.
var repoURL = regexp.MustCompile(`^/([^/]+)/([^/]+)/?$`)

func parseProjectEvidence(in []projectEvidenceRequest) ([]domain.ProjectEvidence, error) {
	out := make([]domain.ProjectEvidence, 0, len(in))

	for _, project := range in {
		parsed, err := url.Parse(strings.TrimSpace(project.URL))
		if err != nil {
			return nil, fmt.Errorf("unparseable repository url %q: %w", project.URL, err)
		}
		match := repoURL.FindStringSubmatch(parsed.Path)
		if match == nil {
			return nil, fmt.Errorf("%q is not a repository url", project.URL)
		}
		evidence := domain.ProjectEvidence{
			RepoOwner: match[1], RepoName: match[2],
			ContributionSummary: project.ContributionSummary,
		}

		declaration, err := parseMaintainer(project.Maintainer)
		if err != nil {
			return nil, err
		}
		evidence.Maintainer = declaration

		out = append(out, evidence)
	}
	return out, nil
}

// parseMaintainer validates a declaration's sources.
//
// An unknown source is rejected here rather than at the database: a contributor
// typo becomes a named 422 instead of an enum violation nobody can read. And
// "other" without supporting text is refused, because the whole point of that
// source is the text.
func parseMaintainer(in *maintainerRequest) (*domain.MaintainerDeclaration, error) {
	if in == nil {
		return nil, nil
	}
	if len(in.Sources) == 0 {
		return nil, fmt.Errorf("a maintainer declaration needs at least one source")
	}

	sources := make([]domain.MaintainerSource, 0, len(in.Sources))
	other := false
	for _, raw := range in.Sources {
		source := domain.MaintainerSource(raw)
		if !domain.ValidMaintainerSource(source) {
			return nil, fmt.Errorf("%q is not a maintainer source", raw)
		}
		if source == domain.SourceOtherEvidence {
			other = true
		}
		sources = append(sources, source)
	}

	if other && strings.TrimSpace(in.OtherEvidence) == "" {
		return nil, fmt.Errorf(`the "other" source needs other_evidence describing it`)
	}

	return &domain.MaintainerDeclaration{
		Sources: sources, OtherEvidence: in.OtherEvidence,
	}, nil
}

func parseClaimSkills(in []claimSkillRequest) []domain.ClaimSkill {
	out := make([]domain.ClaimSkill, 0, len(in))
	for _, s := range in {
		out = append(out, domain.ClaimSkill{
			Slug:               s.Slug,
			Origin:             domain.UserDeclared,
			IsNominatedPrimary: s.NominatedPrimary,
			Rationale:          s.Rationale,
		})
	}
	return out
}

// --- serialization -----------------------------------------------------------

func claimBodyOf(c *domain.Claim) claimBody {
	if c == nil {
		return claimBody{}
	}
	return claimBody{
		ID:               c.ID,
		Status:           string(c.Status),
		Version:          c.Version,
		NominatedPrimary: nominatedPrimary(c.Skills),
		PRs:              prBodies(c.PREvidence),
		Projects:         projectBodies(c.ProjectEvidence),
		Skills:           skillBodies(c.Skills),
		SubmittedAt:      c.SubmittedAt,
		EvaluatedAt:      c.EvaluatedAt,
		LockedUntil:      c.LockedUntil,
		CreatedAt:        c.CreatedAt,
	}
}

func summarizeClaim(c *domain.Claim) claimSummary {
	return claimSummary{
		ID:               c.ID,
		Status:           string(c.Status),
		Version:          c.Version,
		NominatedPrimary: nominatedPrimary(c.Skills),
		PRCount:          len(c.PREvidence),
		SkillCount:       len(c.Skills),
		SubmittedAt:      c.SubmittedAt,
		EvaluatedAt:      c.EvaluatedAt,
		CreatedAt:        c.CreatedAt,
	}
}

func nominatedPrimary(skills []domain.ClaimSkill) string {
	for _, s := range skills {
		if s.IsNominatedPrimary {
			return s.Slug
		}
	}
	return ""
}

func prBodies(in []domain.PREvidence) []prEvidenceBody {
	out := make([]prEvidenceBody, 0, len(in))
	for _, pr := range in {
		out = append(out, prEvidenceBody{
			Position: pr.Position, RepoOwner: pr.RepoOwner, RepoName: pr.RepoName,
			PRNumber: pr.PRNumber, Role: string(pr.Role),
		})
	}
	return out
}

func projectBodies(in []domain.ProjectEvidence) []projectEvidenceBody {
	out := make([]projectEvidenceBody, 0, len(in))
	for _, project := range in {
		body := projectEvidenceBody{
			RepoOwner: project.RepoOwner, RepoName: project.RepoName,
		}
		if m := project.Maintainer; m != nil {
			body.MaintainerDeclared = true
			body.MaintainerValidated = m.Validated
		}
		out = append(out, body)
	}
	return out
}

func skillBodies(in []domain.ClaimSkill) []claimSkillBody {
	out := make([]claimSkillBody, 0, len(in))
	for _, s := range in {
		out = append(out, claimSkillBody{
			Slug: s.Slug, Origin: string(s.Origin),
			NominatedPrimary: s.IsNominatedPrimary,
			AcceptedAt:       s.AcceptedAt, DismissedAt: s.DismissedAt,
		})
	}
	return out
}

func failureBodies(in []port.ValidationFailure) []validationFailureBody {
	out := make([]validationFailureBody, 0, len(in))
	for _, f := range in {
		out = append(out, validationFailureBody{
			Position: f.Position, Reason: string(f.Reason), Message: f.Message,
			ConflictingClaimID: f.ConflictingClaimID,
		})
	}
	return out
}
