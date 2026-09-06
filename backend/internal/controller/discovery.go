package controller

import (
	"encoding/json"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

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

// MaxPerPage bounds a page of results.
//
// A hirer asking for ten thousand rows is either paginating badly or scraping,
// and either way the answer is the same.
const MaxPerPage = 50

// MaxScore is the top of every 0..100 score (ADR-0005). A filter above it
// matches nothing, so it is a typo rather than a query.
const MaxScore = 100

// leaderboardKinds are the boards that exist.
var leaderboardKinds = []string{"overall", "generalist", "skill"}

// suggestFilter finds the filter a misspelling probably meant.
//
// Prefix matching rather than an edit distance: the real confusions are
// truncations — min_generalist for min_generalist_score — and a suggestion that
// is wrong is worse than none.
func suggestFilter(unknown string) string {
	for known := range knownFilters {
		if strings.HasPrefix(known, unknown) || strings.HasPrefix(unknown, known) {
			return known
		}
	}
	return ""
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
	Availability    any               `json:"availability,omitempty"`
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
	Skill         *leaderboardSkillBody  `json:"skill,omitempty"`
	RubricVersion string                 `json:"rubric_version"`
	Entries       []leaderboardEntryBody `json:"entries"`
}

type scorecardEvidenceBody struct {
	Repo     string    `json:"repo"`
	PRNumber int       `json:"pr_number"`
	MergedAt time.Time `json:"merged_at"`
	Score    float64   `json:"score"`
}

type scorecardSkillBody struct {
	Slug            string  `json:"slug"`
	Name            string  `json:"name"`
	Standing        string  `json:"standing"`
	DistinctPRCount int     `json:"distinct_pr_count"`
	Score           float64 `json:"score"`
	// any, not *int: an authenticated read reports the position and null when
	// there is none, while a PUBLIC one omits the field entirely — a share
	// link carries evidence, never a standing in a pool the reader cannot see.
	Rank     any                     `json:"rank,omitempty"`
	Evidence []scorecardEvidenceBody `json:"evidence"`
}

// lapsedAvailability is the detail a hirer needs about somebody who has gone
// quiet: what they last said, when they last confirmed it, and how long ago
// that was (ADR-0008 §1a).
type lapsedAvailability struct {
	Status          string     `json:"status"`
	LastConfirmedAt *time.Time `json:"last_confirmed_at"`
	InactiveForDays int        `json:"inactive_for_days"`
}

// scorecardUserBody identifies the contributor a scorecard describes.
//
// Availability is a bare STATUS while the window is live and an object once it
// has lapsed. The asymmetry is deliberate: "how long since they were last seen"
// is only a question worth answering about somebody who has gone quiet, and
// putting it on every result would be noise on the ones that are current.
//
// Active is likewise reported only when false — its presence IS the warning.
type scorecardUserBody struct {
	ID           domain.UserID `json:"id,omitempty"`
	DisplayName  string        `json:"display_name"`
	GitHubLogin  string        `json:"github_login"`
	Active       *bool         `json:"active,omitempty"`
	Availability any           `json:"availability,omitempty"`
}

type scorecardBody struct {
	User            scorecardUserBody    `json:"user"`
	Skills          []scorecardSkillBody `json:"skills"`
	OverallScore    *float64             `json:"overall_score"`
	GeneralistScore *float64             `json:"generalist_score"`
	RubricVersion   string               `json:"rubric_version"`

	// Present but null until a contact request is accepted (ADR-0002 §5). An
	// authenticated reader is told the field exists and is empty; a public
	// share link is not told there is an address at all, so `any` keeps
	// "withheld" and "not applicable" apart.
	Email any `json:"email,omitempty"`
}

// saveSearchRequest stores a QUESTION.
//
// The field is `filters`, matching what a saved search holds: a set of filters
// replayed later, never a result set (domain.SavedSearch.Filters).
// Filters stays raw so an unrecognised key can be NAMED. Decoding straight
// into a SearchQuery reports only that the body was wrong, and "your filter
// set is invalid" does not tell a hirer that they wrote min_generalist for
// min_generalist_score.
type saveSearchRequest struct {
	Name    string          `json:"name"`
	Filters json.RawMessage `json:"filters"`
}

type savedSearchBody struct {
	ID           domain.SavedSearchID `json:"id"`
	Name         string               `json:"name"`
	Filters      domain.SearchQuery   `json:"filters"`
	Organization *orgRef              `json:"organization,omitempty"`
	CreatedBy    domain.HirerID       `json:"created_by,omitempty"`
	CreatedAt    time.Time            `json:"created_at"`
}

// parseFilters turns a raw filter object into a query, naming what it refused.
//
// Same rule as the query string: an unknown key is refused rather than
// dropped. A saved search is worse than a one-off search to be lenient about,
// because the mistake is stored and every replay repeats it silently.
func parseFilters(raw json.RawMessage) (domain.SearchQuery, []fieldError, string) {
	if len(raw) == 0 {
		return domain.SearchQuery{}, nil, ""
	}

	var keyed map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keyed); err != nil {
		return domain.SearchQuery{}, nil, service.CodeInvalidFilter
	}

	var unknown []fieldError
	for name := range keyed {
		if _, ok := knownFilters[name]; ok {
			continue
		}
		unknown = append(unknown, fieldError{
			Field: name, Reason: "unknown_filter", DidYouMean: suggestFilter(name),
		})
	}
	if len(unknown) > 0 {
		sort.Slice(unknown, func(i, j int) bool { return unknown[i].Field < unknown[j].Field })
		return domain.SearchQuery{}, unknown, service.CodeUnknownFilter
	}

	var q domain.SearchQuery
	if err := json.Unmarshal(raw, &q); err != nil {
		return domain.SearchQuery{}, nil, service.CodeInvalidFilter
	}
	return q, nil, ""
}

// --- handlers ----------------------------------------------------------------

func (c *DiscoveryController) search(w http.ResponseWriter, r *http.Request) {
	p, ok := requireAny(w, r)
	if !ok {
		return
	}

	query, problems, code := parseSearchQuery(r)
	if len(problems) > 0 {
		writeFieldErrors(w, http.StatusUnprocessableEntity, code, problems)
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
	if !slices.Contains(leaderboardKinds, string(kind)) {
		writeFieldErrors(w, http.StatusUnprocessableEntity, service.CodeInvalidQuery,
			[]fieldError{{Field: "kind", Reason: "unknown", Allowed: leaderboardKinds}})
		return
	}
	if kind == domain.LeaderboardKind("skill") && skill == "" {
		writeFieldErrors(w, http.StatusUnprocessableEntity, service.CodeInvalidQuery,
			[]fieldError{{Field: "skill", Reason: "required_for_kind_skill"}})
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
		// No organization: every row in this list belongs to the caller's own
		// org, so repeating it once per row says nothing the request did not.
		out = append(out, savedSearchBody{
			ID: s.ID, Name: s.Name, Filters: s.Filters,
			CreatedBy: s.CreatedBy, CreatedAt: s.CreatedAt,
		})
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

	filters, unknown, code := parseFilters(body.Filters)
	if code != "" {
		writeFieldErrors(w, http.StatusUnprocessableEntity, code, unknown)
		return
	}
	if problems := scoreRangeProblems(filters); len(problems) > 0 {
		writeFieldErrors(w, http.StatusUnprocessableEntity, service.CodeInvalidFilter, problems)
		return
	}

	saved, err := c.discovery.SaveSearch(r.Context(), p, body.Name, filters)
	if err != nil {
		c.writeCapabilityError(w, p, err)
		return
	}
	writeJSON(w, http.StatusCreated, savedSearchBody{
		ID: saved.ID, Name: saved.Name, Filters: saved.Filters,
		Organization: orgRefOf(p), CreatedBy: saved.CreatedBy, CreatedAt: saved.CreatedAt,
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
	// A contributor is refused for being the wrong kind of account, which is a
	// different fact from an unverified hirer's missing capability — and the
	// remedy differs too: one cannot be fixed at all.
	if p.Kind == domain.KindContributor {
		writeCode(w, http.StatusForbidden, service.CodeHirerRequired)
		return
	}
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
func parseSearchQuery(r *http.Request) (domain.SearchQuery, []fieldError, string) {
	values := r.URL.Query()

	var unknown []fieldError
	for name := range values {
		if _, known := knownFilters[name]; known {
			continue
		}
		unknown = append(unknown, fieldError{
			Field: name, Reason: "unknown_filter", DidYouMean: suggestFilter(name),
		})
	}
	if len(unknown) > 0 {
		return domain.SearchQuery{}, unknown, service.CodeUnknownFilter
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

	var problems []fieldError
	if raw := values.Get("per_page"); raw != "" {
		if requested, err := strconv.Atoi(raw); err == nil && requested > MaxPerPage {
			problems = append(problems, fieldError{
				Field: "per_page", Reason: "above_max", Max: intPtr(MaxPerPage),
			})
		}
	}
	problems = append(problems, scoreRangeProblems(q)...)
	if len(problems) > 0 {
		return domain.SearchQuery{}, problems, service.CodeInvalidQuery
	}
	return q, nil, ""
}

// scoreRangeProblems rejects thresholds no score can reach.
func scoreRangeProblems(q domain.SearchQuery) []fieldError {
	var problems []fieldError
	for field, value := range map[string]*float64{
		"min_skill_score":      q.MinSkillScore,
		"min_overall_score":    q.MinOverallScore,
		"min_generalist_score": q.MinGeneralistScore,
	} {
		// Generalist is deliberately UNBOUNDED (ADR-0007), so only the two
		// bounded scores have a ceiling to exceed.
		if field == "min_generalist_score" || value == nil {
			continue
		}
		if *value > MaxScore {
			problems = append(problems, fieldError{
				Field: field, Reason: "out_of_range", Max: intPtr(MaxScore),
			})
		}
	}
	return problems
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
			Availability: searchAvailability(item),
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
		User: scorecardUserBody{
			DisplayName: card.User.DisplayName,
			GitHubLogin: card.User.GitHubLogin,
		},
		OverallScore:    card.User.OverallScore,
		GeneralistScore: card.User.GeneralistScore,
		Skills:          make([]scorecardSkillBody, 0, len(card.Skills)),
	}

	// A published share link carries presentation and no handle to query the
	// contributor by, so the id and the rubric are for authenticated reads
	// only (ADR-0002).
	out.RubricVersion = card.RubricVersion
	if identified {
		out.User.ID = card.User.UserID
		out.User.Active, out.User.Availability = availabilityView(card.User)
		out.Email = card.Email
	}

	for _, s := range card.Skills {
		skill := scorecardSkillBody{
			Slug: s.Slug, Name: s.Name, Standing: string(s.Standing),
			Score:           s.Score,
			DistinctPRCount: s.DistinctPRCount,
			Evidence:        make([]scorecardEvidenceBody, 0, len(s.Evidence)),
		}
		if identified {
			skill.Rank = s.Rank
		}

		evidence := s.Evidence
		if !identified && len(evidence) > publicEvidenceLimit {
			// One PR, not five. A share link is something a contributor hands
			// to anyone, so it shows enough to be worth reading and not a full
			// dossier the reader never proved they may see (ADR-0002).
			evidence = evidence[:publicEvidenceLimit]
		}
		for _, e := range evidence {
			skill.Evidence = append(skill.Evidence, scorecardEvidenceBody{
				Repo: e.Repo, PRNumber: e.PRNumber, MergedAt: e.MergedAt, Score: e.Score,
			})
		}
		out.Skills = append(out.Skills, skill)
	}
	return out
}

// publicEvidenceLimit is how many scored PRs a share link shows per skill.
const publicEvidenceLimit = 1

// availabilityView renders availability at the detail the reader needs.
//
// While the window is live, the status alone answers the question. Once it has
// lapsed, a hirer is deciding whether to take a bet on somebody who has gone
// quiet, and that decision turns on HOW quiet — so the lapsed form carries the
// last confirmation and the days since (ADR-0008 §1a).
func availabilityView(u domain.SearchResult) (*bool, any) {
	if u.Availability == nil {
		return nil, nil
	}
	if u.Active {
		return nil, string(u.Availability.Status)
	}

	inactive := false
	view := lapsedAvailability{
		Status:          string(u.Availability.Status),
		LastConfirmedAt: &u.Availability.LastSetAt,
	}
	if u.InactiveForDays != nil {
		view.InactiveForDays = *u.InactiveForDays
	}
	return &inactive, view
}

// searchAvailability reports availability in the shape that fits its state.
//
// A live window answers "until when"; a lapsed one answers "how long ago" —
// twice, because how stale the signal is and how long they have been gone
// differ by the 15-day window (ADR-0008 §1a). Putting all three on every
// result would be noise on the ones that are current.
func searchAvailability(u domain.SearchResult) any {
	if u.Availability == nil {
		return nil
	}
	if u.Active {
		return availabilityBody{
			Status:    string(u.Availability.Status),
			ExpiresAt: u.Availability.ExpiresAt,
		}
	}

	view := lapsedAvailability{
		Status:          string(u.Availability.Status),
		LastConfirmedAt: &u.Availability.LastSetAt,
	}
	if u.InactiveForDays != nil {
		view.InactiveForDays = *u.InactiveForDays
	}
	return view
}

// errorsIsCapability reports a refusal that a hirer can act on by getting
// verified, as distinct from one they cannot.
func errorsIsCapability(err error) bool {
	code := service.CodeOf(err)
	return code == service.CodeHiringCapability || code == service.CodeVerificationNeeded ||
		(code == "" && isNotCapable(err))
}
