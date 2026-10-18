package controller

import (
	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
)

// Where a role's work is, as each audience reads it (ADR-0024 §Bodies).

// roleLocationRequest is one entry a hirer sends.
//
// `{"mode": "remote"}` is remote from anywhere, `{"mode": "remote", "country":
// "IN"}` remote within a country, `{"mode": "onsite", "address_id": "…"}` at an
// address. Nothing is checked here: a remote entry with an address id, or an
// onsite one with a country, is a refusal the service names.
type roleLocationRequest struct {
	Mode      string `json:"mode"`
	Country   string `json:"country"`
	AddressID string `json:"address_id"`
}

func (b roleLocationRequest) location() domain.RoleLocation {
	out := domain.RoleLocation{Mode: domain.LocationMode(b.Mode), Country: b.Country}
	if b.AddressID != "" {
		id := domain.AddressID(b.AddressID)
		out.AddressID = &id
	}
	return out
}

// addressBody is an address as a seat of its own organisation reads it: in
// full, whether it is the main office, and whether it has been removed. The
// same object everywhere a hirer meets an address (amendment 1 §1.8).
type addressBody struct {
	ID           domain.AddressID `json:"id"`
	Country      string           `json:"country"`
	City         string           `json:"city"`
	PostalCode   string           `json:"postal_code"`
	Street1      string           `json:"street1"`
	Street2      string           `json:"street2"`
	IsMainOffice bool             `json:"is_main_office"`
	Removed      bool             `json:"removed"`
}

func addressBodyOf(a domain.Address) addressBody {
	return addressBody{
		ID: a.ID, Country: a.Country, City: a.City, PostalCode: a.PostalCode,
		Street1: a.Street1, Street2: a.Street2,
		IsMainOffice: a.IsMainOffice, Removed: a.Removed(),
	}
}

// roleLocationBody is one entry as a hirer reads it. Every entry carries the
// same three keys, and the address is embedded so a list of roles needs no
// second request and a closed role at a removed address still renders.
type roleLocationBody struct {
	Mode    string       `json:"mode"`
	Country *string      `json:"country"`
	Address *addressBody `json:"address"`
}

func roleLocationBodies(locs []domain.RoleLocation) []roleLocationBody {
	out := make([]roleLocationBody, 0, len(locs))
	for _, l := range locs {
		body := roleLocationBody{Mode: string(l.Mode)}
		if l.Mode == domain.ModeRemote && l.Country != "" {
			country := l.Country
			body.Country = &country
		}
		if l.Address != nil {
			a := addressBodyOf(*l.Address)
			body.Address = &a
		}
		out = append(out, body)
	}
	return out
}

// contactLocationBody is one entry as a CONTRIBUTOR reads it: a country, or
// null for anywhere, on a remote entry; a city and a country on an onsite one.
// Never a street, a postcode or an address id (ADR-0024 decision 7) — the
// response cannot leak what it does not carry.
type contactLocationBody struct {
	Mode    string  `json:"mode"`
	Country *string `json:"country"`
	City    *string `json:"city,omitempty"`
}

func contactLocationBodies(locs []domain.RoleLocation) []contactLocationBody {
	sorted := append([]domain.RoleLocation(nil), locs...)
	domain.SortLocations(sorted)

	out := make([]contactLocationBody, 0, len(sorted))
	seen := map[string]bool{}
	for _, l := range sorted {
		body := contactLocationBody{Mode: string(l.Mode)}
		key := string(l.Mode) + "|"
		switch {
		case l.Mode == domain.ModeOnsite && l.Address != nil:
			country, city := l.Address.Country, l.Address.City
			body.Country, body.City = &country, &city
			key += country + "|" + city
		case l.Country != "":
			country := l.Country
			body.Country = &country
			key += country
		}
		// Two offices in one city read the same to a contributor, and are
		// shown once (amendment 1 §1.7).
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, body)
	}
	return out
}
