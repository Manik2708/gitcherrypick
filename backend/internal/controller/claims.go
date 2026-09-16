package controller

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
	"github.com/Manik2708/gitcherrypick/backend/internal/service"
)

// MaxPREvidence is how many pull requests one claim may carry (ADR-0003).
//
// Five is the product: a contributor picks their BEST work, and a limit is what
// makes that a choice rather than a dump of everything they ever merged.
const MaxPREvidence = 5

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
	Position int `json:"position"`

	// Null on a row whose URL did not parse: there is no owner, and reporting
	// "" would read as a repository whose owner is blank.
	RepoOwner *string `json:"repo_owner"`
	RepoName  *string `json:"repo_name"`
	PRNumber  *int    `json:"pr_number"`

	Role          string `json:"role"`
	InvalidReason string `json:"invalid_reason,omitempty"`

	// What the model made of this PR, per skill. Absent until the claim has
	// been evaluated, which is also when it stops being editable.
	Scores []prScoreBody `json:"scores,omitempty"`
}

// prScoreBody is one skill's score on one PR.
type prScoreBody struct {
	Skill string  `json:"skill"`
	Score float64 `json:"score"`
}

// judgedPRBody is a pull request AFTER it has been judged.
//
// Position and number identify it; everything else is the verdict. The role is
// gone because it was the question and this is the answer — but the repo and
// the URL stay: "PR #922" names nothing a reader can open, and a verdict you
// cannot click through to is hard to check and harder to argue with.
type judgedPRBody struct {
	Position  int    `json:"position"`
	PRNumber  int    `json:"pr_number"`
	RepoOwner string `json:"repo_owner,omitempty"`
	RepoName  string `json:"repo_name,omitempty"`
	URL       string `json:"url,omitempty"`

	Scores     []prScoreBody              `json:"scores,omitempty"`
	Dimensions map[string]prDimensionBody `json:"dimensions,omitempty"`

	// A PR that counted toward nothing says so, and why. Silently omitting it
	// would leave a contributor comparing five submitted against four scored
	// with no explanation for the gap.
	//
	// Rejected and Skipped are different facts. Rejected is a VERDICT — the
	// model read it and it did not count. Skipped means it was never read, so
	// nobody has judged anything; the remedy is to check the URL rather than
	// to argue with a score (ADR-0004, partial enrichment).
	Rejected        bool   `json:"rejected,omitempty"`
	RejectionReason string `json:"rejection_reason,omitempty"`
	Skipped         bool   `json:"skipped,omitempty"`
	Reason          string `json:"reason,omitempty"`
	Message         string `json:"message,omitempty"`
}

type prDimensionBody struct {
	Score  int    `json:"score"`
	Remark string `json:"remark"`
}

// rejectionMessages explain a verdict in the contributor's terms.
var rejectionMessages = map[string]string{
	string(domain.TypoOrWording): "This PR did not count toward any skill. " +
		"A typo or wording correction does not evidence engineering work.",
	string(domain.FormattingOnly): "This PR did not count toward any skill. " +
		"A formatting-only change does not evidence engineering work.",
	string(domain.GeneratedOutput): "This PR did not count toward any skill. " +
		"Generated output is not authored work.",
	string(domain.MechanicalDependencyBump): "This PR did not count toward any skill. " +
		"A mechanical dependency bump does not evidence engineering work.",
	string(domain.RevertOnly): "This PR did not count toward any skill. " +
		"A revert does not evidence the work it undoes.",
	string(domain.NotTheClaimedSkill): "This PR did not count toward any skill. " +
		"It does not evidence the skill it was claimed for.",
	string(domain.AuthoredByOther): "This PR did not count toward any skill. " +
		"It was authored by someone else.",
	string(domain.UnrelatedToIssue): "This PR did not count toward any skill. " +
		"It does not address the issue it references.",
	string(domain.MaintainerFlaggedUnrelated): "This PR did not count toward any skill. " +
		"A maintainer flagged it as unrelated.",
	string(domain.BelowQualityFloor): "This PR did not count toward any skill. " +
		"Its assessed quality was too low to represent engineering work.",
}

func judgedPRBodies(in []domain.PREvidence) []judgedPRBody {
	out := make([]judgedPRBody, 0, len(in))
	for _, pr := range in {
		body := judgedPRBody{
			Position: pr.Position, PRNumber: pr.PRNumber,
			RepoOwner: pr.RepoOwner, RepoName: pr.RepoName, URL: pr.RawURL,
		}
		for _, s := range pr.Scores {
			body.Scores = append(body.Scores, prScoreBody{Skill: s.Skill, Score: s.Score})
		}
		if len(pr.Dimensions) > 0 {
			body.Dimensions = make(map[string]prDimensionBody, len(pr.Dimensions))
			for name, d := range pr.Dimensions {
				body.Dimensions[name] = prDimensionBody{Score: d.Score, Remark: d.Remark}
			}
		}
		switch {
		case pr.Rejected:
			body.Rejected = true
			body.RejectionReason = pr.RejectionReason
			body.Message = rejectionMessages[pr.RejectionReason]

		case pr.Facts == nil && len(pr.Scores) == 0:
			// Never enriched, so never judged.
			body.Skipped, body.Reason = true, "unreachable"
			body.Message = "We could not read this pull request from GitHub, " +
				"so it was not scored. Check the URL is correct and still public."
		}
		out = append(out, body)
	}
	return out
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
	RepoOwner string `json:"repo_owner"`
	RepoName  string `json:"repo_name"`
	// A project reports the stronger of the two things it can say. With a
	// maintainer declaration, that is the declaration and the model's verdict
	// on it, because the reach bonus turns on it (ADR-0005). Without one, it
	// is what the contributor says they did.
	ContributionSummary string `json:"contribution_summary,omitempty"`

	MaintainerDeclared bool `json:"maintainer_declared,omitempty"`

	// any rather than *bool so that null and absent stay distinguishable:
	// null means declared and not yet ruled on, absent means never declared.
	// omitempty on a *bool collapses both to absent.
	MaintainerValidated any `json:"maintainer_validated,omitempty"`
}

// claimSkillBody is one declared or suggested skill.
//
// Named by SLUG, not by id. The slug is the stable public identifier a
// contributor claims and a scorecard shows; the uuid is a storage detail, and
// putting it on the wire would invite a client to key on it.
// Three fields are conditional, and each answers a question that only exists
// at one point in the claim's life. ScoringMode is stated when the skill is
// ATTACHED, because that is when it is decided and the contributor is choosing.
// Score and Standing appear once the skill has been judged — before that there
// is nothing to report, and reporting a zero would claim a measurement nobody
// made (ADR-0007).
type claimSkillBody struct {
	Slug             string     `json:"slug"`
	Origin           string     `json:"origin"`
	NominatedPrimary bool       `json:"nominated_primary"`
	ScoringMode      string     `json:"scoring_mode,omitempty"`
	Score            *float64   `json:"score,omitempty"`
	Standing         string     `json:"standing,omitempty"`
	AcceptedAt       *time.Time `json:"accepted_at,omitempty"`
	DismissedAt      *time.Time `json:"dismissed_at,omitempty"`
}

// claimBody is a claim as a READ returns it.
//
// Empty collections are omitted: on a read, an absent `projects` and an empty
// one say the same thing, and the shorter answer is the one that does not
// invite a client to distinguish them. The write form below does the opposite,
// for a reason that only applies to writes.
type claimBody struct {
	ID               domain.ClaimID `json:"id"`
	Status           string         `json:"status"`
	Version          int            `json:"version"`
	NominatedPrimary string         `json:"nominated_primary,omitempty"`

	// any: a draft's PRs are evidence, a judged claim's are verdicts. Same
	// field, because it answers the same question — what is on this claim —
	// and the answer changes shape once there is one.
	PRs         any                   `json:"prs,omitempty"`
	Projects    []projectEvidenceBody `json:"projects,omitempty"`
	Skills      any                   `json:"skills,omitempty"`
	Suggestions []suggestionBody      `json:"suggestions,omitempty"`
	SubmittedAt *time.Time            `json:"submitted_at,omitempty"`
	EvaluatedAt *time.Time            `json:"evaluated_at,omitempty"`
	LockedUntil *time.Time            `json:"locked_until,omitempty"`
}

// createdClaimBody is a claim as CREATE returns it.
//
// An empty draft, so there is no evidence to echo — and created_at, which is
// the one timestamp a creation is entitled to report and the later shapes are
// not, because after this point the interesting question is when it changed.
type createdClaimBody struct {
	ID        domain.ClaimID `json:"id"`
	Status    string         `json:"status"`
	Version   int            `json:"version"`
	CreatedAt time.Time      `json:"created_at"`
}

// writtenClaimBody is a claim as a whole-claim EDIT returns it.
//
// Collections are always present, empty ones included, because a replace can
// CLEAR them — and `"projects": []` is how the response says the projects that
// were there are gone. Omitting it would make a successful deletion
// indistinguishable from a field the response forgot.
//
// updated_at for the same reason: an edit reports when it landed, which is the
// value the caller needs to detect their own write. A read does not, because
// the version already tells them which state they are looking at.
type writtenClaimBody struct {
	ID      domain.ClaimID `json:"id"`
	Status  string         `json:"status"`
	Version int            `json:"version"`

	// No nominated_primary. It is a property of the skills below, and the
	// edit response already lists them with the flag set — restating it at the
	// top would be two places to read the same fact from.
	PRs         []prEvidenceBody      `json:"prs"`
	Projects    []projectEvidenceBody `json:"projects"`
	Skills      []claimSkillBody      `json:"skills"`
	SubmittedAt *time.Time            `json:"submitted_at,omitempty"`
	EvaluatedAt *time.Time            `json:"evaluated_at,omitempty"`
	LockedUntil *time.Time            `json:"locked_until,omitempty"`
	UpdatedAt   time.Time             `json:"updated_at"`
}

// claimSummary is the list view: counts rather than the evidence itself.
type claimSummary struct {
	ID      domain.ClaimID `json:"id"`
	Status  string         `json:"status"`
	Version int            `json:"version"`
	// A pointer so a claim that nominated nothing reports null rather than an
	// empty string. "" would read as a skill whose slug is blank.
	NominatedPrimary *string    `json:"nominated_primary"`
	PRCount          int        `json:"pr_count"`
	SkillCount       int        `json:"skill_count"`
	SubmittedAt      *time.Time `json:"submitted_at"`
	EvaluatedAt      *time.Time `json:"evaluated_at"`

	// No created_at. A list of claims is ordered by it, which makes it the one
	// timestamp the reader can infer from the position of the row.
	LockedUntil *time.Time `json:"locked_until"`
}

// queuedReevaluationBody is a dispute as it enters the queue.
type queuedReevaluationBody struct {
	ID        domain.RequestID `json:"id"`
	Status    string           `json:"status"`
	CreatedAt time.Time        `json:"created_at"`
}

// withdrawnClaimBody is a retired claim.
//
// Neither version nor evidence: a withdrawal ends the claim's editable life, so
// the version a client would hold it for is of no further use.
type withdrawnClaimBody struct {
	ID          domain.ClaimID `json:"id"`
	Status      string         `json:"status"`
	WithdrawnAt *time.Time     `json:"withdrawn_at"`
}

// submitAcceptedBody is the 202 for a queued claim.
type submitAcceptedBody struct {
	ID                         domain.ClaimID `json:"id"`
	Status                     string         `json:"status"`
	Version                    int            `json:"version"`
	SubmittedAt                *time.Time     `json:"submitted_at"`
	EstimatedResultWithinHours int            `json:"estimated_result_within_hours"`
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
	writeJSON(w, http.StatusCreated, createdClaimBodyOf(claim))
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

	if problems := validateClaim(body); len(problems) > 0 {
		writeFieldErrors(w, http.StatusUnprocessableEntity, service.CodeInvalidClaim, problems)
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
	writeJSON(w, http.StatusOK, writtenClaimBodyOf(claim))
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
	// claim_status and version appear only when the edit CHANGED the claim's
	// standing — editing a scored claim resets it to draft, and the
	// contributor needs to know their evidence is no longer judged. Editing a
	// draft that stays a draft changed nothing worth reporting.
	out := map[string]any{"prs": prBodies(claim.PREvidence)}
	if claim.Version > 1 {
		out["claim_status"] = string(claim.Status)
		out["version"] = claim.Version
	}
	writeJSON(w, http.StatusOK, out)
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
		// A pair conflict is a CONFLICT rather than an invalid claim: the
		// evidence is fine, it is already spent on the same skill elsewhere,
		// and the fix is to drop it rather than correct it (ADR-0007).
		code, status := service.CodeInvalidEvidence, http.StatusUnprocessableEntity
		for _, f := range failures {
			if f.ConflictingClaimID != nil {
				code, status = service.CodeEvidencePairConflict, http.StatusConflict
				break
			}
		}
		body := map[string]any{"error": code, "items": failureItems(failures)}
		if code == service.CodeInvalidEvidence {
			// The submit did not merely fail — it moved the claim to
			// `invalid`, and a client that re-read it would otherwise find a
			// state the response never mentioned. A pair conflict says
			// nothing here: that evidence is valid, just already spent.
			body["claim_status"] = string(domain.ClaimInvalid)
		}
		writeDetail(w, status, body)
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
			Skill:                s.Skill.Slug,
			From:                 string(domain.Primary),
			To:                   string(domain.Secondary),
			DistinctPRCountAfter: s.DistinctPRCountAfter,
			Message: fmt.Sprintf("Withdrawing this claim removes %s from ranking.",
				strings.ToUpper(s.Skill.Slug[:1])+s.Skill.Slug[1:]),
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
	writeJSON(w, http.StatusOK, withdrawnClaimBody{
		ID: claim.ID, Status: string(claim.Status), WithdrawnAt: claim.WithdrawnAt,
	})
}

// decidedSuggestion is the response to accepting or dismissing a suggestion.
//
// Standing, the count and the score are pointers because a DISMISSED
// suggestion has none — it never became a skill. Reporting them as zeroes
// would read as "measured, and nothing".
type decidedSuggestion struct {
	Slug            string     `json:"slug"`
	Origin          string     `json:"origin"`
	AcceptedAt      *time.Time `json:"accepted_at,omitempty"`
	DismissedAt     *time.Time `json:"dismissed_at,omitempty"`
	Standing        *string    `json:"standing,omitempty"`
	DistinctPRCount *int       `json:"distinct_pr_count,omitempty"`
	Score           *float64   `json:"score,omitempty"`
}

// decideSuggestion builds the accept and dismiss handlers.
func (c *ClaimController) decideSuggestion(accept bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, claimID, ok := c.claimContext(w, r)
		if !ok {
			return
		}
		skillID := domain.SkillID(chi.URLParam(r, "skillID"))

		decision, err := c.claims.DecideSuggestion(r.Context(), p.Contributor.ID, claimID, skillID, accept)
		if err != nil {
			c.writeClaimError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, decidedSuggestionBody(decision))
	}
}

// decidedSuggestionBody reports the decision and, when it created one, the
// standing that followed.
//
// Origin stays ai_suggested: it records how the skill ARRIVED, while
// accepted_at records that the contributor chose it. Overwriting the origin
// would erase the distinction the scorecard depends on.
func decidedSuggestionBody(d *domain.SuggestionDecision) decidedSuggestion {
	body := decidedSuggestion{
		Slug:        d.Skill.Slug,
		Origin:      string(d.Skill.Origin),
		AcceptedAt:  d.Skill.AcceptedAt,
		DismissedAt: d.Skill.DismissedAt,
	}
	if d.Standing != nil {
		standing := string(d.Standing.Standing)
		count := d.Standing.DistinctPRCount
		score := d.Standing.Score
		body.Standing, body.DistinctPRCount, body.Score = &standing, &count, &score
	}
	return body
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
	// No claim_id. The request was raised against a claim the caller named in
	// the path, so echoing it back tells them something they already typed.
	writeJSON(w, http.StatusCreated, queuedReevaluationBody{
		ID: request.ID, Status: string(request.Status), CreatedAt: request.CreatedAt,
	})
}

// --- helpers -----------------------------------------------------------------

// validateClaim checks the shape of a whole-claim edit.
//
// Exactly one skill must be nominated primary. The nomination decides which
// skill the model scores the PRs AGAINST, so two nominations have no meaning
// and none leaves the evaluator with nothing to judge (ADR-0003).
func validateClaim(body replaceClaimRequest) []fieldError {
	var problems []fieldError

	if len(body.PRs) > MaxPREvidence {
		problems = append(problems, fieldError{
			Field: "prs", Reason: "too_many",
			Max: intPtr(MaxPREvidence), Provided: intPtr(len(body.PRs)),
		})
	}

	nominated := 0
	for _, s := range body.Skills {
		if s.NominatedPrimary {
			nominated++
		}
	}
	switch {
	case nominated > 1:
		problems = append(problems, fieldError{
			Field: "skills", Reason: "multiple_nominated_primary", Provided: intPtr(nominated),
		})
	case nominated == 0 && len(body.Skills) > 0:
		problems = append(problems, fieldError{
			Field: "skills", Reason: "no_nominated_primary", Provided: intPtr(0),
		})
	}
	return problems
}

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
		role := domain.EvidenceRole(pr.Role)
		if role == "" {
			// Authorship is the default because it is the overwhelmingly
			// common claim; a reviewer says so explicitly (ADR-0003).
			role = domain.RoleAuthor
		}

		row := domain.PREvidence{Position: pr.Position, RawURL: pr.URL, Role: role}

		owner, name, number, err := parsePRURL(pr.URL)
		if err != nil {
			// RECORDED, not refused. Accepting evidence does not validate it:
			// a draft may hold anything, and the checks run at submit — which
			// is what lets a contributor paste five URLs and fix them after
			// seeing which ones the platform rejected (ADR-0003).
			reason := domain.MalformedURL
			row.InvalidReason = &reason
			out = append(out, row)
			continue
		}

		row.RepoOwner, row.RepoName, row.PRNumber = owner, name, number
		out = append(out, row)
	}
	return out, nil
}

// parsePRURL delegates to the domain, which owns the one regex. The profile
// service needs the same parse for ADR-0019 §7, and two copies would drift.
func parsePRURL(raw string) (owner, name string, number int, err error) {
	return domain.ParsePRURL(raw)
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
		PRs:              readPRBodies(c),
		Projects:         projectBodies(c.ProjectEvidence),
		Skills:           readSkillBodies(c),
		Suggestions:      suggestionBodies(c.Skills),
		SubmittedAt:      c.SubmittedAt,
		EvaluatedAt:      c.EvaluatedAt,
		LockedUntil:      c.LockedUntil,
	}
}

// createdClaimBodyOf renders the response to a create.
func createdClaimBodyOf(c *domain.Claim) createdClaimBody {
	if c == nil {
		return createdClaimBody{}
	}
	return createdClaimBody{
		ID: c.ID, Status: string(c.Status),
		Version: c.Version, CreatedAt: c.CreatedAt,
	}
}

// judgedSkillBody is a skill on a claim that has been scored.
//
// Standing and the count, not the declaration: once a verdict exists, what a
// contributor wants to know is where the skill now sits and how far it is from
// the next threshold — the origin and the nomination were inputs.
type judgedSkillBody struct {
	Slug            string  `json:"slug"`
	SkillID         string  `json:"skill_id,omitempty"`
	Standing        string  `json:"standing"`
	DistinctPRCount int     `json:"distinct_pr_count"`
	Score           float64 `json:"score,omitempty"`

	// Set when one more surviving PR would promote. The threshold is the whole
	// mechanism (ADR-0003), and a contributor one short should be told so
	// rather than left to count.
	Note string `json:"note,omitempty"`
}

// A skill the contributor DECLARED. An inert AI suggestion is not one of
// these — it appears under `suggestions`, unscored, until they accept it
// (ADR-0003 §10).
func judgedSkillBodies(in []domain.ClaimSkill) []judgedSkillBody {
	out := make([]judgedSkillBody, 0, len(in))
	for _, s := range in {
		if s.IsInert() {
			continue
		}
		body := judgedSkillBody{
			Slug: s.Slug, Standing: string(s.Standing), DistinctPRCount: s.DistinctPRCount,
		}
		if s.DistinctPRCount == domain.PrimaryThreshold-1 {
			body.Note = "Needs one more surviving PR to become primary and enter ranking."
		}
		out = append(out, body)
	}
	return out
}

// suggestionBody is a skill the model noticed that nobody declared.
//
// INERT: no score, no standing. Scoring it would leak a number the contributor
// has not opted into, and the model suggests while only the contributor
// promotes (ADR-0003 §10).
type suggestionBody struct {
	SkillID     domain.SkillID `json:"skill_id"`
	Slug        string         `json:"slug"`
	Name        string         `json:"name"`
	Rationale   string         `json:"rationale"`
	AcceptedAt  *time.Time     `json:"accepted_at"`
	DismissedAt *time.Time     `json:"dismissed_at"`
}

func suggestionBodies(in []domain.ClaimSkill) []suggestionBody {
	var out []suggestionBody
	for _, s := range in {
		if !s.IsInert() {
			continue
		}
		out = append(out, suggestionBody{
			SkillID: s.SkillID, Slug: s.Slug, Name: s.Name, Rationale: s.Rationale,
			AcceptedAt: s.AcceptedAt, DismissedAt: s.DismissedAt,
		})
	}
	return out
}

// readSkillBodies picks the shape that fits the claim's state.
func readSkillBodies(c *domain.Claim) any {
	if c.Status == domain.ClaimEvaluated {
		return judgedSkillBodies(c.Skills)
	}
	return scoredSkillBodies(c.Skills)
}

// readPRBodies picks the shape that fits the claim's state.
func readPRBodies(c *domain.Claim) any {
	if c.Status == domain.ClaimEvaluated {
		return judgedPRBodies(c.PREvidence)
	}
	return prBodies(c.PREvidence)
}

// writtenClaimBodyOf renders the response to a whole-claim edit.
func writtenClaimBodyOf(c *domain.Claim) writtenClaimBody {
	if c == nil {
		return writtenClaimBody{}
	}
	return writtenClaimBody{
		ID:          c.ID,
		Status:      string(c.Status),
		Version:     c.Version,
		PRs:         prBodies(c.PREvidence),
		Projects:    projectBodies(c.ProjectEvidence),
		Skills:      declaredSkillBodies(c.Skills),
		SubmittedAt: c.SubmittedAt,
		EvaluatedAt: c.EvaluatedAt,
		LockedUntil: c.LockedUntil,
		UpdatedAt:   c.UpdatedAt,
	}
}

func summarizeClaim(c *port.ClaimSummary) claimSummary {
	return claimSummary{
		ID:               c.ID,
		Status:           string(c.Status),
		Version:          c.Version,
		NominatedPrimary: nominatedOrNil(c.NominatedPrimary),
		PRCount:          c.PRCount,
		SkillCount:       c.SkillCount,
		SubmittedAt:      c.SubmittedAt,
		EvaluatedAt:      c.EvaluatedAt,
		LockedUntil:      c.LockedUntil,
	}
}

// declaredSkillBodies renders skills on a whole-claim edit: what was declared,
// with no verdict, because an edit resets the claim to unjudged.
func declaredSkillBodies(in []domain.ClaimSkill) []claimSkillBody {
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

// scoredSkillBodies renders skills on a READ, carrying the standing this claim
// contributed to once there is one.
func scoredSkillBodies(in []domain.ClaimSkill) []claimSkillBody {
	out := make([]claimSkillBody, 0, len(in))
	for _, s := range in {
		body := claimSkillBody{
			Slug: s.Slug, Origin: string(s.Origin),
			NominatedPrimary: s.IsNominatedPrimary,
			AcceptedAt:       s.AcceptedAt, DismissedAt: s.DismissedAt,
		}
		if s.Score != nil {
			body.Score = s.Score
			body.Standing = string(s.Standing)
		}
		out = append(out, body)
	}
	return out
}

// nominatedOrNil keeps "nominated nothing" distinct from "nominated a skill
// with an empty slug", which cannot happen but would look identical.
func nominatedOrNil(slug string) *string {
	if slug == "" {
		return nil
	}
	return &slug
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
		body := prEvidenceBody{Position: pr.Position, Role: string(pr.Role)}
		if pr.RepoOwner != "" {
			body.RepoOwner, body.RepoName = &pr.RepoOwner, &pr.RepoName
			body.PRNumber = &pr.PRNumber
		}
		if pr.InvalidReason != nil {
			body.InvalidReason = string(*pr.InvalidReason)
		}
		for _, s := range pr.Scores {
			body.Scores = append(body.Scores, prScoreBody{Skill: s.Skill, Score: s.Score})
		}
		out = append(out, body)
	}
	return out
}

func projectBodies(in []domain.ProjectEvidence) []projectEvidenceBody {
	out := make([]projectEvidenceBody, 0, len(in))
	for _, project := range in {
		body := projectEvidenceBody{RepoOwner: project.RepoOwner, RepoName: project.RepoName}
		if m := project.Maintainer; m != nil {
			body.MaintainerDeclared = true
			body.MaintainerValidated = m.Validated
		} else {
			body.ContributionSummary = project.ContributionSummary
		}
		out = append(out, body)
	}
	return out
}

// skillBodies renders skills as the ATTACH endpoint returns them, stating how
// each one will be judged.
func skillBodies(in []domain.ClaimSkill) []claimSkillBody {
	out := make([]claimSkillBody, 0, len(in))
	for _, s := range in {
		mode := s.ScoringMode
		if mode == "" {
			// Everything but pr-review is scored the standard way, so an
			// unresolved mode reports the default rather than an empty string
			// a client would have to interpret.
			mode = domain.ScoringStandard
		}
		out = append(out, claimSkillBody{
			Slug: s.Slug, Origin: string(s.Origin), ScoringMode: string(mode),
			NominatedPrimary: s.IsNominatedPrimary,
			AcceptedAt:       s.AcceptedAt, DismissedAt: s.DismissedAt,
		})
	}
	return out
}

// failureItems renders every failing evidence row.
//
// Keyed on POSITION rather than a field name, because a contributor fixing a
// claim is looking at a numbered list of pull requests.
func failureItems(in []port.ValidationFailure) []fieldError {
	out := make([]fieldError, 0, len(in))
	for _, f := range in {
		out = append(out, fieldError{
			Position: f.Position, Reason: string(f.Reason), Message: f.Message,
			Skill: f.Skill, ConflictingClaimID: f.ConflictingClaimID,
		})
	}
	return out
}
