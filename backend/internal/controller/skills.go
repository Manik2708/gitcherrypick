package controller

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
	"github.com/Manik2708/gitcherrypick/backend/internal/service"
)

// SkillController serves the catalogue and skill requests.
type SkillController struct {
	skills port.SkillService
}

// NewSkillController wires the catalogue.
func NewSkillController(skills port.SkillService) *SkillController {
	return &SkillController{skills: skills}
}

var _ port.Controller = (*SkillController)(nil)

// Routes mounts the catalogue at its own root.
//
// GET /skills is one of the two genuinely PUBLIC endpoints: a contributor
// building a claim needs to know what they may declare before they have an
// account, and gating it would make the product unexplorable.
func (c *SkillController) Routes() (string, http.Handler) {
	r := chi.NewRouter()
	r.Get("/", c.search)
	return "/skills", r
}

// SkillRequestController serves /skill-requests, which is contributor-only.
//
// Separate from SkillController because port.Controller mounts one prefix, and
// these are two roots with different access rules — folding them together would
// mean a public router carrying an authenticated route.
type SkillRequestController struct {
	skills port.SkillService
}

// NewSkillRequestController wires the request queue.
func NewSkillRequestController(skills port.SkillService) *SkillRequestController {
	return &SkillRequestController{skills: skills}
}

var _ port.Controller = (*SkillRequestController)(nil)

// Routes mounts /skill-requests.
func (c *SkillRequestController) Routes() (string, http.Handler) {
	r := chi.NewRouter()
	r.Post("/", c.request)
	return "/skill-requests", r
}

// --- shapes ------------------------------------------------------------------

// skillMatchBody reports HOW a skill matched, not just that it did.
//
// A contributor searching "golang" and getting "Go" needs to see the alias that
// bridged them, or the result looks like a mistake.
type skillMatchBody struct {
	Slug         string `json:"slug"`
	Name         string `json:"name"`
	Category     string `json:"category"`
	MatchedVia   string `json:"matched_via"`
	MatchedAlias string `json:"matched_alias,omitempty"`
}

type skillRequestBody struct {
	ProposedName string `json:"proposed_name"`
	Rationale    string `json:"rationale"`
}

// --- handlers ----------------------------------------------------------------

// search returns catalogue matches for a query.
//
// A query is REQUIRED: SkillService refuses an empty one, because the catalogue
// is large enough that returning all of it would be a denial-of-service dressed
// as a feature. The refusal is named `invalid_query` rather than the generic
// invalid code, since only this controller knows what the caller was searching.
func (c *SkillController) search(w http.ResponseWriter, r *http.Request) {
	matches, err := c.skills.Search(r.Context(), r.URL.Query().Get("q"))
	if err != nil {
		if service.CodeOf(err) == "" && errors.Is(err, service.ErrInvalid) {
			writeCode(w, http.StatusUnprocessableEntity, service.CodeInvalidQuery)
			return
		}
		writeError(w, err)
		return
	}

	out := make([]skillMatchBody, 0, len(matches))
	for _, m := range matches {
		out = append(out, skillMatchBody{
			Slug:         m.Skill.Slug,
			Name:         m.Skill.Name,
			Category:     m.Skill.Category,
			MatchedVia:   m.MatchedVia,
			MatchedAlias: m.MatchedAlias,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": out})
}

// request proposes a catalogue addition.
//
// Contributor-only: the catalogue exists to describe contribution, and a hirer
// proposing entries would let demand shape what counts as evidence.
func (c *SkillRequestController) request(w http.ResponseWriter, r *http.Request) {
	p, ok := require(w, r, domain.KindContributor)
	if !ok {
		return
	}

	var body skillRequestBody
	if err := decode(w, r, &body); err != nil {
		writeCode(w, http.StatusUnprocessableEntity, service.CodeUnknownSkill)
		return
	}

	id, err := c.skills.RequestSkill(r.Context(), p.Contributor.ID, body.ProposedName, body.Rationale)
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"id":            id,
		"proposed_name": body.ProposedName,
		"status":        "pending",
		"created_at":    time.Now().UTC(),
	})
}
