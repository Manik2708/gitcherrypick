package controller

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// PlacesController serves /places — the country picker (ADR-0018 §9).
//
// PUBLIC, because the organisation onboarding form needs it and has no account
// behind it either (ADR-0017 §2). It carries nothing about anybody: a list of
// countries is not information about a person.
type PlacesController struct {
	places port.PlaceService
}

// NewPlacesController wires the country picker.
func NewPlacesController(places port.PlaceService) *PlacesController {
	return &PlacesController{places: places}
}

var _ port.Controller = (*PlacesController)(nil)

// Routes mounts /places.
func (c *PlacesController) Routes() (string, http.Handler) {
	r := chi.NewRouter()
	r.Get("/countries", c.countries)
	return "/places", r
}

type countryBody struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// countries backs every country picker on the platform.
//
// It FAILS OPEN: a provider that is down answers 200 with an empty list rather
// than an error. The form behind this still submits — the service validates the
// SHAPE of a code and not its membership — so a signup path does not stop
// working on somebody else's bad day (ADR-0018 §PlaceService).
//
// An empty list is therefore not "there are no countries". It is "the picker
// could not be filled", and a client should let the person type a code rather
// than presenting an empty select as if it were the answer.
func (c *PlacesController) countries(w http.ResponseWriter, r *http.Request) {
	countries, err := c.places.Countries(r.Context())
	if err != nil {
		// Logged by the middleware, not surfaced. There is nothing the caller
		// can do about it and nothing about this is a security decision.
		writeJSON(w, http.StatusOK, map[string]any{
			"countries": []countryBody{}, "degraded": true,
		})
		return
	}

	out := make([]countryBody, 0, len(countries))
	for _, country := range countries {
		out = append(out, countryBody{Code: country.Code, Name: country.Name})
	}
	writeJSON(w, http.StatusOK, map[string]any{"countries": out, "degraded": false})
}
