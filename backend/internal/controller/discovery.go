package controller

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
	"github.com/Manik2708/gitcherrypick/backend/internal/service"
)

// knownFilters is the complete set of search parameters.
//
// An unrecognised one is REJECTED rather than ignored (fixtures:
// `unknown_filter`). Silently dropping `min_score` when the real name is
// `min_skill_score` would return a wider result set than the hirer asked for,
// and nothing in the response would say so.
var knownFilters = map[string]struct{}{
	"skills": {}, "min_skill_score": {}, "min_overall_score": {},
	"min_generalist_score": {}, "availability": {}, "evidence_within_months": {},
	"q": {}, "include_inactive": {}, "page": {}, "per_page": {},
}

// DiscoveryController serves search, leaderboards and scorecards.
type DiscoveryController struct {
	discovery port.DiscoveryService
}

// NewDiscoveryController wires the hirer-facing read surface.
func NewDiscoveryController(discovery port.DiscoveryService) *DiscoveryController {
	return &DiscoveryController{discovery: discovery}
}

var _ port.Controller = (*DiscoveryController)(nil)

// Routes mounts search at the root, alongside the other discovery reads.
//
// Four separate roots — /search, /leaderboard, /contributors, /saved-searches —
// so this controller mounts at "/" and claims only those paths.
func (c *DiscoveryController) Routes() (string, http.Handler) {
	r := chi.NewRouter()

	r.Get("/search", c.search)
	r.Get("/leaderboard", c.leaderboard)
	r.Get("/contributors/{userID}/scorecard", c.scorecard)

	r.Route("/saved-searches", func(r chi.Router) {
		r.Get("/", c.savedSearches)
		r.Post("/", c.saveSearch)
		r.Get("/{searchID}/results", c.replaySavedSearch)
		r.Delete("/{searchID}", c.deleteSavedSearch)
	})

	return "/", r
}

// --- shapes ------------------------------------------------------------------

// searchResultBody is one contributor as a hirer sees them.
//
// The contributor is `id` here and `user_id` on a leaderboard entry. That
// difference is in the approved fixtures: a search result IS the contributor,
// while a leaderboard entry is a ranking that REFERS to one.
type searchResultBody struct {
	Rank            int               `json:"rank"`
	UserID          domain.UserID     `json:"id"`
	DisplayName     string            `json:"display_name"`
	GitHubLogin     string            `json:"github_login"`
	Active          bool              `json:"active"`
	Availability    *availabilityBody `json:"availability,omitempty"`
	Skills          []resultSkillBody `json:"skills"`
	OverallScore    *float64          `json:"overall_score"`
	GeneralistScore *float64          `json:"generalist_score"`
}

// searchResultsBody carries the counts a hirer needs to trust the page.
//
// InactiveHidden is eligible-minus-visible: a hirer seeing 12 of 40 needs to
// know 28 were withheld by the availability toggle rather than not existing
// (ADR-0008 §1a). RankedBy names the ordering, because a multi-skill query has
// no single skill score and `rank` would otherwise mean different things in
// different responses.
type searchResultsBody struct {
	Total          int                `json:"total"`
	InactiveHidden int                `json:"inactive_hidden"`
	RankedBy       string             `json:"ranked_by"`
	Page           int                `json:"page"`
	PerPage        int                `json:"per_page"`
	Results        []searchResultBody `json:"results"`
}

type leaderboardEntryBody struct {
	Rank        int           `json:"rank"`
	UserID      domain.UserID `json:"user_id"`
	DisplayName string        `json:"display_name"`
	GitHubLogin string        `json:"github_login"`
	Score       float64       `json:"score"`
	Active      bool          `json:"active"`
}

// leaderboardSkillBody names the skill a skill-board ranks.
//
// An object rather than a bare slug: a client shows the name and keys on the
// slug, and returning only one would force it to look the other up.
type leaderboardSkillBody struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

type leaderboardBody struct {
	Kind          string                 `json:"kind"`
	Skill         *leaderboardSkillBody  `json:"skill"`
	RubricVersion string                 `json:"rubric_version"`
	Entries       []leaderboardEntryBody `json:"entries"`
}

type scorecardEvidenceBody struct {
	Repo     string  `json:"repo"`
	PRNumber int     `json:"pr_number"`
	Title    string  `json:"title"`
	Score    float64 `json:"score"`
}

type scorecardSkillBody struct {
	resultSkillBody
	DistinctPRCount int                     `json:"distinct_pr_count"`
	Evidence        []scorecardEvidenceBody `json:"evidence"`
}

type scorecardBody struct {
	UserID          domain.UserID        `json:"user_id,omitempty"`
	DisplayName     string               `json:"display_name"`
	GitHubLogin     string               `json:"github_login"`
	Skills          []scorecardSkillBody `json:"skills"`
	OverallScore    *float64             `json:"overall_score"`
	GeneralistScore *float64             `json:"generalist_score"`
	RubricVersion   string               `json:"rubric_version,omitempty"`

	// Released only after a contact request is accepted (ADR-0002 §5).
	Email *string `json:"email,omitempty"`
}

// saveSearchRequest stores a QUESTION.
//
// The field is `filters`, matching what a saved search holds: a set of filters
// replayed later, never a result set (domain.SavedSearch.Filters).
type saveSearchRequest struct {
	Name    string             `json:"name"`
	Filters domain.SearchQuery `json:"filters"`
}

type savedSearchBody struct {
	ID           domain.SavedSearchID `json:"id"`
	Name         string               `json:"name"`
	Filters      domain.SearchQuery   `json:"filters"`
	Organization *orgRef              `json:"organization,omitempty"`
	CreatedBy    domain.HirerID       `json:"created_by,omitempty"`
}

// --- handlers ----------------------------------------------------------------

func (c *DiscoveryController) search(w http.ResponseWriter, r *http.Request) {
	p, ok := requireAny(w, r)
	if !ok {
		return
	}

	query, bad := parseSearchQuery(r)
	if bad != "" {
		writeCode(w, http.StatusUnprocessableEntity, service.CodeUnknownFilter)
		return
	}

	results, err := c.discovery.Search(r.Context(), p, query)
	if err != nil {
		c.writeCapabilityError(w, p, err)
		return
	}
	writeJSON(w, http.StatusOK, searchResultsOf(results))
}

func (c *DiscoveryController) leaderboard(w http.ResponseWriter, r *http.Request) {
	p, ok := require(w, r, domain.KindHirer)
	if !ok {
		return
	}

	kind := domain.LeaderboardKind(r.URL.Query().Get("kind"))
	skill := r.URL.Query().Get("skill")

	// A skill board without a skill has nothing to rank. Reported as a FIELD
	// error rather than a bare 422, because the caller can fix it only if they
	// are told which parameter is missing.
	if kind == domain.LeaderboardKind("skill") && skill == "" {
		writeDetail(w, http.StatusUnprocessableEntity, map[string]any{
			"error": service.CodeInvalidQuery,
			"items": []fieldError{{Field: "skill", Reason: "required_for_kind_skill"}},
		})
		return
	}

	board, err := c.discovery.Leaderboard(r.Context(), p, kind, skill)
	if err != nil {
		c.writeCapabilityError(w, p, err)
		return
	}

	out := leaderboardBody{
		Kind:          string(board.Kind),
		RubricVersion: board.RubricVersion,
		Entries:       make([]leaderboardEntryBody, 0, len(board.Entries)),
	}
	if board.Skill != nil {
		out.Skill = &leaderboardSkillBody{Slug: board.Skill.Slug, Name: board.Skill.Name}
	}
	for _, e := range board.Entries {
		out.Entries = append(out.Entries, leaderboardEntryBody{
			Rank: e.Rank, UserID: e.UserID, DisplayName: e.DisplayName,
			GitHubLogin: e.GitHubLogin, Score: e.Score, Active: e.Active,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (c *DiscoveryController) scorecard(w http.ResponseWriter, r *http.Request) {
	p, ok := requireAny(w, r)
	if !ok {
		return
	}

	raw := chi.URLParam(r, "userID")
	if !isUUID(raw) {
		writeCode(w, http.StatusBadRequest, service.CodeInvalidID)
		return
	}

	card, err := c.discovery.Scorecard(r.Context(), p, domain.UserID(raw))
	if err != nil {
		if service.CodeOf(err) == "" && isNotFound(err) {
			writeCode(w, http.StatusNotFound, service.CodeContributorNotFound)
			return
		}
		c.writeCapabilityError(w, p, err)
		return
	}
	writeJSON(w, http.StatusOK, scorecardOf(card, true))
}

func (c *DiscoveryController) savedSearches(w http.ResponseWriter, r *http.Request) {
	p, ok := require(w, r, domain.KindHirer)
	if !ok {
		return
	}

	searches, err := c.discovery.SavedSearches(r.Context(), p)
	if err != nil {
		c.writeCapabilityError(w, p, err)
		return
	}

	out := make([]savedSearchBody, 0, len(searches))
	for _, s := range searches {
		out = append(out, savedSearchBody{ID: s.ID, Name: s.Name, Filters: s.Filters, Organization: orgRefOf(p), CreatedBy: s.CreatedBy})
	}
	writeJSON(w, http.StatusOK, map[string]any{"total": len(out), "saved_searches": out})
}

// saveSearch stores a QUESTION, never an answer.
//
// Replaying it later evaluates the filters as the caller, so a saved search
// cannot become a stale snapshot of people who have since opted out.
func (c *DiscoveryController) saveSearch(w http.ResponseWriter, r *http.Request) {
	p, ok := require(w, r, domain.KindHirer)
	if !ok {
		return
	}

	var body saveSearchRequest
	if err := decode(w, r, &body); err != nil {
		writeCode(w, http.StatusUnprocessableEntity, service.CodeInvalidFilter)
		return
	}

	saved, err := c.discovery.SaveSearch(r.Context(), p, body.Name, body.Filters)
	if err != nil {
		c.writeCapabilityError(w, p, err)
		return
	}
	writeJSON(w, http.StatusCreated, savedSearchBody{
		ID: saved.ID, Name: saved.Name, Filters: saved.Filters,
		Organization: orgRefOf(p), CreatedBy: saved.CreatedBy,
	})
}

func (c *DiscoveryController) replaySavedSearch(w http.ResponseWriter, r *http.Request) {
	p, ok := require(w, r, domain.KindHirer)
	if !ok {
		return
	}

	raw := chi.URLParam(r, "searchID")
	if !isUUID(raw) {
		writeCode(w, http.StatusBadRequest, service.CodeInvalidID)
		return
	}

	results, err := c.discovery.ReplaySavedSearch(r.Context(), p, domain.SavedSearchID(raw))
	if err != nil {
		if service.CodeOf(err) == "" && isNotFound(err) {
			writeCode(w, http.StatusNotFound, service.CodeSavedSearchNotFound)
			return
		}
		c.writeCapabilityError(w, p, err)
		return
	}
	writeJSON(w, http.StatusOK, searchResultsOf(results))
}

func (c *DiscoveryController) deleteSavedSearch(w http.ResponseWriter, r *http.Request) {
	p, ok := require(w, r, domain.KindHirer)
	if !ok {
		return
	}

	raw := chi.URLParam(r, "searchID")
	if !isUUID(raw) {
		writeCode(w, http.StatusBadRequest, service.CodeInvalidID)
		return
	}

	if err := c.discovery.DeleteSavedSearch(r.Context(), p, domain.SavedSearchID(raw)); err != nil {
		if service.CodeOf(err) == "" && isNotFound(err) {
			writeCode(w, http.StatusNotFound, service.CodeSavedSearchNotFound)
			return
		}
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeCapabilityError reports WHICH verification is missing.
//
// "Get verified" is useless advice to someone who is verified and whose
// organization is not, so the two flags travel with the refusal.
func (c *DiscoveryController) writeCapabilityError(w http.ResponseWriter, p domain.Principal, err error) {
	if !errorsIsCapability(err) {
		writeError(w, err)
		return
	}

	status, code := statusFor(err)
	body := map[string]any{"error": code}
	if message := messages[code]; message != "" {
		body["message"] = message
	}
	if p.Hirer != nil {
		body["hirer_verified"] = p.Hirer.VerifiedAt != nil
		body["organization_verified"] = p.Hirer.Organization.IsVerified()
	}
	writeDetail(w, status, body)
}

// --- parsing -----------------------------------------------------------------

// parseSearchQuery reads the filters, rejecting any it does not recognise.
//
// Returns the offending parameter name so the caller can name it; an empty
// string means the query is well formed.
func parseSearchQuery(r *http.Request) (domain.SearchQuery, string) {
	values := r.URL.Query()

	for name := range values {
		if _, known := knownFilters[name]; !known {
			return domain.SearchQuery{}, name
		}
	}

	q := domain.SearchQuery{
		Query:           values.Get("q"),
		IncludeInactive: values.Get("include_inactive") == "true",
		Page:            atoiDefault(values.Get("page"), 1),
		PerPage:         atoiDefault(values.Get("per_page"), 20),
	}

	// Availability is repeatable AND comma-separated: a hirer may want both
	// looking_for_job and open_to_freelance, and either spelling should work.
	for _, raw := range values["availability"] {
		for _, status := range strings.Split(raw, ",") {
			if trimmed := strings.TrimSpace(status); trimmed != "" {
				q.Availability = append(q.Availability, domain.AvailabilityStatus(trimmed))
			}
		}
	}

	if raw := values.Get("skills"); raw != "" {
		// Comma-separated, because a hirer types "go,kubernetes" rather than
		// repeating the parameter.
		for _, slug := range strings.Split(raw, ",") {
			if trimmed := strings.TrimSpace(slug); trimmed != "" {
				q.Skills = append(q.Skills, trimmed)
			}
		}
	}

	q.MinSkillScore = floatPointer(values.Get("min_skill_score"))
	q.MinOverallScore = floatPointer(values.Get("min_overall_score"))
	q.MinGeneralistScore = floatPointer(values.Get("min_generalist_score"))
	q.EvidenceWithinMonths = intPointer(values.Get("evidence_within_months"))

	return q, ""
}

func atoiDefault(raw string, fallback int) int {
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func floatPointer(raw string) *float64 {
	if raw == "" {
		return nil
	}
	parsed, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return nil
	}
	return &parsed
}

func intPointer(raw string) *int {
	if raw == "" {
		return nil
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		return nil
	}
	return &parsed
}

// --- serialization -----------------------------------------------------------

func searchResultsOf(r *domain.SearchResults) searchResultsBody {
	if r == nil {
		return searchResultsBody{Results: []searchResultBody{}}
	}

	out := searchResultsBody{
		Total: r.Total, InactiveHidden: r.InactiveHidden,
		RankedBy: string(r.RankedBy), Page: r.Page, PerPage: r.PerPage,
		Results: make([]searchResultBody, 0, len(r.Results)),
	}
	for _, item := range r.Results {
		out.Results = append(out.Results, searchResultBody{
			Rank: item.Rank, UserID: item.UserID, DisplayName: item.DisplayName,
			GitHubLogin: item.GitHubLogin, Active: item.Active,
			Availability: availabilityOf(item.Availability),
			Skills:       resultSkillBodies(item.Skills),
			OverallScore: item.OverallScore, GeneralistScore: item.GeneralistScore,
		})
	}
	return out
}

// scorecardOf serializes a scorecard.
//
// identified is false for the public share-link view, which omits the user id:
// a published link proves nothing about who is reading it, so it carries the
// contributor's presentation and not a handle to query them by.
func scorecardOf(card *domain.Scorecard, identified bool) scorecardBody {
	if card == nil {
		return scorecardBody{Skills: []scorecardSkillBody{}}
	}

	out := scorecardBody{
		DisplayName:     card.User.DisplayName,
		GitHubLogin:     card.User.GitHubLogin,
		OverallScore:    card.User.OverallScore,
		GeneralistScore: card.User.GeneralistScore,
		Skills:          make([]scorecardSkillBody, 0, len(card.Skills)),
		Email:           card.Email,
	}
	if identified {
		out.UserID = card.User.UserID
		out.RubricVersion = card.RubricVersion
	}

	for _, s := range card.Skills {
		skill := scorecardSkillBody{
			resultSkillBody: resultSkillBody{
				Slug: s.Slug, Standing: string(s.Standing),
				Score: s.Score, Rank: s.Rank,
			},
			DistinctPRCount: s.DistinctPRCount,
			Evidence:        make([]scorecardEvidenceBody, 0, len(s.Evidence)),
		}
		for _, e := range s.Evidence {
			skill.Evidence = append(skill.Evidence, scorecardEvidenceBody{
				Repo: e.Repo, PRNumber: e.PRNumber, Title: e.Title, Score: e.Score,
			})
		}
		out.Skills = append(out.Skills, skill)
	}
	return out
}

// errorsIsCapability reports a refusal that a hirer can act on by getting
// verified, as distinct from one they cannot.
func errorsIsCapability(err error) bool {
	code := service.CodeOf(err)
	return code == service.CodeHiringCapability || code == service.CodeVerificationNeeded ||
		(code == "" && isNotCapable(err))
}
