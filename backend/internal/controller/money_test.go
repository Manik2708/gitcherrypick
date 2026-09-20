package controller_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
)

// currenciesResponse is the picker's shape.
type currenciesResponse struct {
	Currencies []currencyEntry `json:"currencies"`
	Degraded   bool            `json:"degraded"`
}

type currencyEntry struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

func TestCurrencyPicker(t *testing.T) {
	t.Run("answers anybody, with no account", func(t *testing.T) {
		// Public for the same reason the country picker is: a contributor
		// states what they expect to be paid before anybody has shortlisted
		// them, and a list of currencies is not information about a person.
		h := newHarness(t)
		h.money.EXPECT().Currencies(mock.Anything).Return([]domain.Currency{
			{Code: "GBP", Name: "Pound sterling"},
			{Code: "INR", Name: "Indian rupee"},
		}, nil)

		res := h.do(t, http.MethodGet, "/money/currencies", "", "")
		require.Equal(t, http.StatusOK, res.Status)

		var body currenciesResponse
		res.decode(t, &body)
		require.False(t, body.Degraded)
		require.Equal(t, []currencyEntry{
			{Code: "GBP", Name: "Pound sterling"},
			{Code: "INR", Name: "Indian rupee"},
		}, body.Currencies)
	})

	t.Run("a provider that is down is reported, not raised", func(t *testing.T) {
		// THE FAIL-OPEN CONTRACT. 200 with an empty list and degraded:true, so
		// a client knows the difference between "no currencies" — which is not
		// a thing — and "the picker could not be filled", and lets the person
		// type a code instead. A 502 here would stop a role being written
		// because somebody else's service was having a bad day.
		h := newHarness(t)
		h.money.EXPECT().Currencies(mock.Anything).Return(nil, errors.New("provider down"))

		res := h.do(t, http.MethodGet, "/money/currencies", "", "")
		require.Equal(t, http.StatusOK, res.Status)

		var body currenciesResponse
		res.decode(t, &body)
		require.True(t, body.Degraded)
		require.Empty(t, body.Currencies)
	})
}
