package service_test

import (
	"errors"
	"testing"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
	"github.com/Manik2708/gitcherrypick/backend/internal/service"
	mocks "github.com/Manik2708/gitcherrypick/backend/mocks/port"
)

// Onboarding asks whether there is an office, and names the field it refuses
// (ADR-0024 decisions 11 and 17). Validation runs before anything is minted or
// written, so the mocks below expect nothing of a refused form.

// errValidationPassed is the error a form that PASSED validation runs into: the
// minter is the first thing touched afterwards.
var errValidationPassed = errors.New("validation passed")

func newOnboardingService(t *testing.T) (*service.OnboardingService, *mocks.TokenMinter) {
	t.Helper()
	minter := mocks.NewTokenMinter(t)
	clock := mocks.NewClock(t)
	clock.EXPECT().Now().Return(workplaceNow).Maybe()
	return service.NewOnboardingService(mocks.NewOnboardingRepository(t),
		mocks.NewOrganizationRepository(t), mocks.NewEmailVerificationRepository(t),
		mocks.NewNotifier(t), minter, mocks.NewPasswordHasher(t),
		mocks.NewTxManager(t), clock), minter
}

func officeForm() port.OnboardingForm {
	return port.OnboardingForm{
		Name: "Harbour Lights Ltd", Email: "founder@harbourlights.example",
		Workplace: domain.WorkplaceOffice,
		Country:   "gb", City: "Cardiff", Street1: "3 Bute Crescent",
	}
}

func TestOnboardingRefusalsNameTheirField(t *testing.T) {
	for _, c := range []onboardingCase{
		{"no name", func(f *port.OnboardingForm) { f.Name = " " }, "name"},
		{"no email", func(f *port.OnboardingForm) { f.Email = "" }, "email"},
		{"not a band", func(f *port.OnboardingForm) { f.Headcount = "lots" }, "headcount"},
		{"no workplace", func(f *port.OnboardingForm) { f.Workplace = "" }, "workplace"},
		{"not one of the two", func(f *port.OnboardingForm) { f.Workplace = "hybrid" }, "workplace"},
		{"an office with no address at all", func(f *port.OnboardingForm) {
			f.Country, f.City, f.Street1 = "", "", ""
		}, "address.country"},
		{"a country that is a name", func(f *port.OnboardingForm) { f.Country = "Britain" }, "address.country"},
		{"no city", func(f *port.OnboardingForm) { f.City = "" }, "address.city"},
		{"no street", func(f *port.OnboardingForm) { f.Street1 = "" }, "address.street1"},
		{"fully remote, and an address anyway", func(f *port.OnboardingForm) {
			f.Workplace = domain.WorkplaceFullyRemote
		}, "address"},
		{"fully remote, and only a postcode", func(f *port.OnboardingForm) {
			f.Workplace = domain.WorkplaceFullyRemote
			f.Country, f.City, f.Street1, f.PostalCode = "", "", "", "CF10"
		}, "address"},
	} {
		t.Run(c.name, func(t *testing.T) {
			svc, _ := newOnboardingService(t)
			in := officeForm()
			c.shape(&in)

			for route, err := range map[string]error{
				"submit": svc.Submit(ctx(t), in),
				// Revise validates BEFORE it spends the code: a refused form
				// leaves the code usable (amendment 1 §1.9).
				"revise": svc.Revise(ctx(t), "any-code", in),
			} {
				if service.CodeOf(err) != service.CodeInvalidOnboarding || !errors.Is(err, service.ErrInvalid) {
					t.Fatalf("%s: expected invalid_onboarding, got %v", route, err)
				}
				if got := service.DetailOf(err)["field"]; got != c.field {
					t.Errorf("%s: field = %v, want %s", route, got, c.field)
				}
			}
		})
	}
}

// onboardingCase is one wrong form and the field it is refused for.
type onboardingCase struct {
	name  string
	shape func(*port.OnboardingForm)
	field string
}

func TestOnboardingAcceptsWhatItShould(t *testing.T) {
	for _, c := range []acceptCase{
		{"an office with its address", officeForm()},
		{"fully remote with no address", port.OnboardingForm{
			Name: "Driftwood", Email: "hello@driftwood.example", Workplace: domain.WorkplaceFullyRemote,
		}},
		{"fully remote with every part blank — a disabled form", port.OnboardingForm{
			Name: "Hollow", Email: "founder@hollow.example", Workplace: domain.WorkplaceFullyRemote,
			Country: " ", City: "", PostalCode: "", Street1: "", Street2: "",
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			svc, minter := newOnboardingService(t)
			minter.EXPECT().Mint().Return("", nil, errValidationPassed)
			if err := svc.Submit(ctx(t), c.in); !errors.Is(err, errValidationPassed) {
				t.Fatalf("expected the form to pass validation, got %v", err)
			}
		})
	}
}

// acceptCase is one form validation lets through.
type acceptCase struct {
	name string
	in   port.OnboardingForm
}
