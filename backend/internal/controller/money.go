package controller

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// MoneyController serves /money — the currency picker (ADR-0018 amendment 3).
//
// PUBLIC, like the country picker beside it. A contributor states what they
// expect to be paid before anybody has shortlisted them, a hirer writes a
// salary into a role, and a list of currencies is not information about a
// person either way.
type MoneyController struct {
	money port.MoneyService
}

// NewMoneyController wires the currency picker.
func NewMoneyController(money port.MoneyService) *MoneyController {
	return &MoneyController{money: money}
}

var _ port.Controller = (*MoneyController)(nil)

// Routes mounts /money.
func (c *MoneyController) Routes() (string, http.Handler) {
	r := chi.NewRouter()
	r.Get("/currencies", c.currencies)
	return "/money", r
}

type currencyBody struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// currencies backs every currency field on the platform.
//
// It FAILS OPEN, identically to the country picker: a provider that is down
// answers 200 with an empty list rather than an error, and the form behind it
// still submits because the service validates the SHAPE of a code and not its
// membership.
//
// An empty list is therefore not "there are no currencies". It is "the picker
// could not be filled", and a client should let the person type a code.
func (c *MoneyController) currencies(w http.ResponseWriter, r *http.Request) {
	currencies, err := c.money.Currencies(r.Context())
	if err != nil {
		// Logged by the middleware, not surfaced. There is nothing the caller
		// can do about it and nothing about this is a security decision.
		writeJSON(w, http.StatusOK, map[string]any{
			"currencies": []currencyBody{}, "degraded": true,
		})
		return
	}

	out := make([]currencyBody, 0, len(currencies))
	for _, currency := range currencies {
		out = append(out, currencyBody{Code: currency.Code, Name: currency.Name})
	}
	writeJSON(w, http.StatusOK, map[string]any{"currencies": out, "degraded": false})
}
