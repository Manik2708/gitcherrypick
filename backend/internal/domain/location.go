package domain

import (
	"sort"
	"strings"
	"time"
)

// Workplace is whether an organisation has an office (ADR-0024 decision 11).
//
// An enum rather than a boolean because both values are named in the API and
// read by a person, and "has_office = false" says less than "fully_remote".
type Workplace string

// The two kinds of organisation.
const (
	WorkplaceOffice      Workplace = "office"
	WorkplaceFullyRemote Workplace = "fully_remote"
)

// Valid reports whether w is one of the two.
func (w Workplace) Valid() bool {
	return w == WorkplaceOffice || w == WorkplaceFullyRemote
}

// LocationMode is how a role's location entry is worked (ADR-0024 decision 1).
type LocationMode string

// The two modes. Remote names a country or nothing; onsite names an address.
const (
	ModeRemote LocationMode = "remote"
	ModeOnsite LocationMode = "onsite"
)

// Valid reports whether m is one of the two.
func (m LocationMode) Valid() bool { return m == ModeRemote || m == ModeOnsite }

// RoleLocation is one place a role's work can be done.
//
// Three kinds and only three: remote from anywhere (Country empty), remote
// within a country (Country set), and onsite at one of the organisation's
// addresses (AddressID set). Remote never points at an address, so a company
// may hire remotely where it has no office; onsite names no country of its own,
// because the address already says which country it is in.
type RoleLocation struct {
	Mode LocationMode

	// Country is ISO 3166-1 alpha-2 on a remote entry, and empty for
	// "anywhere". Always empty on an onsite entry.
	Country string

	// AddressID is what an onsite entry points at.
	AddressID *AddressID

	// Address is the row AddressID names, resolved on a read so a list of
	// roles needs no second request. It is read even once removed: a closed
	// role still says where it was.
	Address *Address
}

// Anywhere reports a remote entry with no country.
func (l RoleLocation) Anywhere() bool { return l.Mode == ModeRemote && l.Country == "" }

// placeCountry is the country an entry is in: its own for remote, its
// address's for onsite.
func (l RoleLocation) placeCountry() string {
	if l.Mode == ModeOnsite && l.Address != nil {
		return l.Address.Country
	}
	return l.Country
}

// placeCity is the city of an onsite entry's address, or "".
func (l RoleLocation) placeCity() string {
	if l.Mode == ModeOnsite && l.Address != nil {
		return l.Address.City
	}
	return ""
}

// SortLocations orders entries remote first, then by country, then by city
// (ADR-0024 amendment 1 §1.7), so two reads of one role agree.
func SortLocations(locs []RoleLocation) {
	sort.SliceStable(locs, func(i, j int) bool {
		a, b := locs[i], locs[j]
		if a.Mode != b.Mode {
			return a.Mode == ModeRemote
		}
		if ac, bc := a.placeCountry(), b.placeCountry(); ac != bc {
			return ac < bc
		}
		return a.placeCity() < b.placeCity()
	})
}

// LocationPhrase is one sentence fragment describing where a role is, at the
// detail a contributor may see: a country or "anywhere" for remote, a city and
// a country for onsite, never a street (ADR-0024 decision 7).
//
// countryName turns a code into a name; it falls back to the code itself when
// it has nothing better, which is the caller's business.
func LocationPhrase(locs []RoleLocation, countryName func(code string) string) string {
	sorted := append([]RoleLocation(nil), locs...)
	SortLocations(sorted)

	var (
		anywhere  bool
		countries []string
		places    []string
		seen      = map[string]bool{}
	)
	for _, l := range sorted {
		switch {
		case l.Anywhere():
			anywhere = true
		case l.Mode == ModeRemote:
			name := countryName(l.Country)
			if !seen["r:"+name] {
				seen["r:"+name] = true
				countries = append(countries, name)
			}
		case l.Address != nil:
			place := l.Address.City + ", " + countryName(l.Address.Country)
			if !seen["o:"+place] {
				seen["o:"+place] = true
				places = append(places, place)
			}
		}
	}

	var parts []string
	if anywhere {
		parts = append(parts, "remote")
	}
	if len(countries) > 0 {
		parts = append(parts, "remote within "+strings.Join(countries, " or "))
	}
	if len(places) > 0 {
		parts = append(parts, "onsite in "+strings.Join(places, " or "))
	}
	return strings.Join(parts, ", or ")
}

// RoleSummary names a role without its contents: what a refusal lists when
// roles stand in the way of a change (ADR-0024 amendment 1 §1.4).
type RoleSummary struct {
	ID     RoleID
	Title  string
	Status RoleStatus
}

// OfficeRequestID identifies one request for an office.
type OfficeRequestID string

// OfficeRequestStatus is what has happened to a request.
type OfficeRequestStatus string

// A request is waiting, refused, or became the main office.
const (
	OfficePending  OfficeRequestStatus = "pending"
	OfficeRejected OfficeRequestStatus = "rejected"
	OfficeApproved OfficeRequestStatus = "approved"
)

// OfficeRequest is a fully remote organisation asking for its first address
// (ADR-0024 decision 13).
//
// Its own record rather than a pending address: until an administrator
// approves, the organisation is still fully remote and has no address at all.
type OfficeRequest struct {
	ID             OfficeRequestID
	OrganizationID OrganizationID

	// OrganizationName is resolved for the administrator's queue.
	OrganizationName string

	// The address, as submitted. Flat: there is no address row until approval.
	Country    string
	City       string
	PostalCode string
	Street1    string
	Street2    string

	RequestedBy HirerRef

	DecidedAt *time.Time
	DecidedBy *AdminID
	Reason    string

	// AddressID is the main office this became. Set only on approval.
	AddressID *AddressID

	// Supersedes is the organisation's previous request when that one was
	// rejected, so a reviewer of the second can find the first.
	Supersedes *OfficeRequestID

	CreatedAt time.Time
}

// Status reads what happened off the row, as the schema does.
func (r *OfficeRequest) Status() OfficeRequestStatus {
	switch {
	case r.AddressID != nil:
		return OfficeApproved
	case r.DecidedAt != nil:
		return OfficeRejected
	}
	return OfficePending
}
