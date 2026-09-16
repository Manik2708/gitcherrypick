package service_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/service"
	mocks "github.com/Manik2708/gitcherrypick/backend/mocks/port"
)

// The country check FAILS OPEN (ADR-0018 §PlaceService).
//
// That promise only means something if there is a check when the provider is
// up. Without one, "ZZ" was accepted and the service sat permanently in the
// state the ADR describes as a fallback — the graceful degradation was the
// only behaviour there was.
func TestCountryMembershipFailsOpen(t *testing.T) {
	newService := func(places *mocks.PlaceService) (*service.ProfileService, *mocks.ProfileRepository) {
		profiles := mocks.NewProfileRepository(t)
		users := mocks.NewUserRepository(t)
		// Read to establish whose pull requests these are. No URL is sent by
		// any case here, so nothing is fetched — but the save that survives
		// validation still needs somebody to save it for.
		users.EXPECT().ByID(mock.Anything, mock.Anything).
			Return(&domain.Contributor{ID: userID, GitHubUserID: 4242}, nil).Maybe()
		clock := mocks.NewClock(t)
		clock.EXPECT().Now().Return(time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)).Maybe()

		// The write is stubbed to fail. These tests are about VALIDATION, and
		// a save that succeeded would drag a read-back and a user lookup in
		// behind it — so the assertion is which error comes out, not whether
		// anything was written.
		tx := mocks.NewTxManager(t)
		tx.EXPECT().InTx(mock.Anything, mock.Anything).
			Return(errors.New("not this test's concern")).Maybe()

		// GitHub is never reached in these tests: every case fails validation
		// before a pull request is fetched, and a mock with no expectations
		// asserts exactly that.
		gh := mocks.NewGitHubClient(t)

		return service.NewProfileService(profiles, users, places, gh, tx, clock), profiles
	}

	t.Run("an unknown country is refused while the provider answers", func(t *testing.T) {
		places := mocks.NewPlaceService(t)
		places.EXPECT().Countries(mock.Anything).
			Return([]domain.Country{{Code: "GB", Name: "United Kingdom"}}, nil)
		svc, profiles := newService(places)
		profiles.EXPECT().WorkPreferences(mock.Anything, mock.Anything).
			Return(&domain.WorkPreferences{}, nil).Maybe()

		_, err := svc.SaveProfile(ctx(t), userID, domain.WorkPreferences{CurrentCountry: "ZZ"})
		if !errors.Is(err, service.ErrInvalid) {
			t.Fatalf("expected ErrInvalid, got %v", err)
		}
		if code := service.CodeOf(err); code != service.CodeInvalidProfile {
			t.Errorf("code = %q, want invalid_profile", code)
		}
	})

	t.Run("any well-formed code is accepted while the provider is down", func(t *testing.T) {
		// A provider having a bad day must not stop somebody saying where they
		// live. Nothing here is a security decision.
		places := mocks.NewPlaceService(t)
		places.EXPECT().Countries(mock.Anything).Return(nil, errors.New("provider is down"))
		svc, profiles := newService(places)
		profiles.EXPECT().WorkPreferences(mock.Anything, mock.Anything).
			Return(&domain.WorkPreferences{}, nil)

		// It gets past validation and fails later, at the write — which is the
		// assertion: the country was not what stopped it.
		_, err := svc.SaveProfile(ctx(t), userID, domain.WorkPreferences{CurrentCountry: "ZZ"})
		if service.CodeOf(err) == service.CodeInvalidProfile {
			t.Fatal("a country was refused while the provider was unreachable")
		}
	})

	t.Run("a malformed code is refused whatever the provider says", func(t *testing.T) {
		// The shape check needs no network, so it still holds when membership
		// cannot be checked. Otherwise failing open would accept anything.
		places := mocks.NewPlaceService(t)
		svc, _ := newService(places)

		_, err := svc.SaveProfile(ctx(t), userID, domain.WorkPreferences{CurrentCountry: "gb!"})
		if !errors.Is(err, service.ErrInvalid) {
			t.Fatalf("expected ErrInvalid, got %v", err)
		}
		// The provider is never consulted: mockery fails the test if it is.
	})
}
