package controller

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// OpeningsController serves /openings — the catalogue of published roles
// (ADR-0020).
//
// CONTRIBUTOR-ONLY, and read-only. It is the first surface on the platform
// that shows somebody what exists rather than waiting for a company to
// approach them, and it inverts discovery in that one direction and nothing
// else: there is no apply route here, no way to register interest, and no
// counter recording that anybody looked. A company still approaches, and an
// address is still released only on acceptance (ADR-0005 §9).
//
// Not under /me, despite being filtered by the reader: /me is what the
// platform holds ABOUT somebody, and this is a catalogue that happens to be
// filtered BY them. Mounting it there would invite a later endpoint that
// writes something back.
type OpeningsController struct {
	roles port.RoleService
}

// NewOpeningsController wires the public catalogue.
func NewOpeningsController(roles port.RoleService) *OpeningsController {
	return &OpeningsController{roles: roles}
}

var _ port.Controller = (*OpeningsController)(nil)

// Routes mounts /openings. One route, and it will stay one route.
func (c *OpeningsController) Routes() (string, http.Handler) {
	r := chi.NewRouter()
	r.Get("/", myOpenings(c.roles))
	return "/openings", r
}
