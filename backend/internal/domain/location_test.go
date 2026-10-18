package domain_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
)

func at(country, city string) domain.RoleLocation {
	id := domain.AddressID(city)
	return domain.RoleLocation{
		Mode: domain.ModeOnsite, AddressID: &id,
		Address: &domain.Address{ID: id, Country: country, City: city, Street1: "1 High Street"},
	}
}

func remoteIn(country string) domain.RoleLocation {
	return domain.RoleLocation{Mode: domain.ModeRemote, Country: country}
}

func TestSortLocationsRemoteFirstThenCountryThenCity(t *testing.T) {
	locs := []domain.RoleLocation{
		at("US", "Austin"), remoteIn("IN"), at("IN", "Pune"), at("IN", "Bengaluru"), remoteIn("DE"),
	}
	domain.SortLocations(locs)

	var got []string
	for _, l := range locs {
		if l.Address != nil {
			got = append(got, l.Address.City)
		} else {
			got = append(got, l.Country)
		}
	}
	if want := "DE IN Bengaluru Pune Austin"; strings.Join(got, " ") != want {
		t.Errorf("order = %v, want %s", got, want)
	}
}

func TestLocationPhraseIsAtAContributorsDetail(t *testing.T) {
	names := func(code string) string {
		if code == "IN" {
			return "India"
		}
		return code
	}

	for _, c := range []phraseCase{
		{"anywhere", []domain.RoleLocation{remoteIn("")}, "remote"},
		{"countries, each once", []domain.RoleLocation{remoteIn("IN"), remoteIn("DE"), remoteIn("IN")}, "remote within DE or India"},
		{"cities, never streets", []domain.RoleLocation{at("US", "Austin"), at("IN", "Bengaluru")}, "onsite in Bengaluru, India or Austin, US"},
		{"both", []domain.RoleLocation{at("IN", "Bengaluru"), remoteIn("GB")}, "remote within GB, or onsite in Bengaluru, India"},
		{"nothing", nil, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := domain.LocationPhrase(c.in, names)
			if got != c.want {
				t.Errorf("phrase = %q, want %q", got, c.want)
			}
			if strings.Contains(got, "High Street") {
				t.Error("a street reached a contributor")
			}
		})
	}
}

// phraseCase is one set of entries and how a contributor reads it.
type phraseCase struct {
	name string
	in   []domain.RoleLocation
	want string
}

func TestKindsAndStatuses(t *testing.T) {
	if !domain.WorkplaceOffice.Valid() || !domain.WorkplaceFullyRemote.Valid() || domain.Workplace("hybrid").Valid() {
		t.Error("workplace is office or fully_remote and nothing else")
	}
	if !domain.ModeRemote.Valid() || !domain.ModeOnsite.Valid() || domain.LocationMode("hybrid").Valid() {
		t.Error("a mode is remote or onsite and nothing else")
	}
	if !remoteIn("").Anywhere() || remoteIn("IN").Anywhere() || at("IN", "Pune").Anywhere() {
		t.Error("anywhere is a remote entry with no country")
	}

	now := time.Now()
	var nothing *domain.Address
	if nothing.Removed() || (&domain.Address{}).Removed() || !(&domain.Address{RemovedAt: &now}).Removed() {
		t.Error("removed is a stamp")
	}

	id := domain.AddressID("a")
	r := domain.OfficeRequest{}
	if r.Status() != domain.OfficePending {
		t.Errorf("undecided = %s", r.Status())
	}
	r.DecidedAt = &now
	if r.Status() != domain.OfficeRejected {
		t.Errorf("decided, no address = %s", r.Status())
	}
	r.AddressID = &id
	if r.Status() != domain.OfficeApproved {
		t.Errorf("decided, with address = %s", r.Status())
	}
}
