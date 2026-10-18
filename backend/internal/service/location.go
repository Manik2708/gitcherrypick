package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// Field-named refusals (ADR-0024 decisions 16–18).
//
// The code a client branches on stays what it was; `field` is what lets it put
// its own words beside the right control, and `position` (1-based) names the
// entry of a list (amendment 1 §1.6).

// invalidRole refuses a role, a closure, a settings change or a contact step,
// naming the request key it is about.
func invalidRole(field, format string, args ...any) *Error {
	return Coded(ErrInvalid, CodeInvalidRole, format, args...).
		WithDetail(map[string]any{"field": field})
}

// invalidRoleAt is invalidRole for one entry of a list.
func invalidRoleAt(field string, position int, format string, args ...any) *Error {
	return Coded(ErrInvalid, CodeInvalidRole, format, args...).
		WithDetail(map[string]any{"field": field, "position": position})
}

// locationRefusal names the entry of `locations` a refusal is about.
func locationRefusal(code string, position int, format string, args ...any) *Error {
	return Coded(ErrInvalid, code, format, args...).
		WithDetail(map[string]any{"field": "locations", "position": position})
}

// invalidAddress names the part of an address body that is wrong.
func invalidAddress(field, format string, args ...any) *Error {
	return Coded(ErrInvalid, CodeInvalidAddress, format, args...).
		WithDetail(map[string]any{"field": field})
}

// invalidOnboarding names the part of the onboarding form that is wrong.
func invalidOnboarding(field, format string, args ...any) *Error {
	return Coded(ErrInvalid, CodeInvalidOnboarding, format, args...).
		WithDetail(map[string]any{"field": field})
}

// rolesIn is what address_in_use and onsite_roles_exist carry: the roles in the
// way, so the owner knows what to close or revise (amendment 1 §1.4).
func rolesIn(code, format string, roles []domain.RoleSummary) *Error {
	listed := make([]map[string]any, 0, len(roles))
	for _, r := range roles {
		listed = append(listed, map[string]any{
			"id": string(r.ID), "title": r.Title, "status": string(r.Status),
		})
	}
	return Coded(ErrConflict, code, "%s", format).WithDetail(map[string]any{"roles": listed})
}

// addressProblem names the first missing or malformed part of an address, or
// "" when there is none. Country, city and street are required; a postcode
// and a second line are not, because several countries have no postcodes
// (ADR-0017).
//
// The country is normalised in place: two letters, upper-cased.
func addressProblem(country *string, city, street1 string) string {
	*country = strings.ToUpper(strings.TrimSpace(*country))
	switch {
	case !validCountryCode(*country):
		return "country"
	case strings.TrimSpace(city) == "":
		return "city"
	case strings.TrimSpace(street1) == "":
		return "street1"
	}
	return ""
}

// locationKey identifies an entry for collapsing repeats.
func locationKey(l domain.RoleLocation) string {
	address := ""
	if l.AddressID != nil {
		address = string(*l.AddressID)
	}
	return string(l.Mode) + "|" + l.Country + "|" + address
}

// resolveLocations checks a role's entries against the organisation and
// returns them normalised and collapsed.
//
// MUST run inside the transaction that writes the role, after the
// organisation's row is locked: the kind and the addresses it reads are what
// the switch to fully remote and an address removal change (ADR-0024
// §Invariants).
//
// Precedence is amendment 1 §1.5: an empty list first; then each entry in
// request order — malformed, onsite in a fully remote organisation, onsite
// with no address, an address not this organisation's, a removed address;
// and only then remote-anywhere beside remote-in-a-country, naming the later
// of the two.
func (s *RoleService) resolveLocations(ctx context.Context, tx port.Tx, org domain.OrganizationID, in []domain.RoleLocation) ([]domain.RoleLocation, error) {
	if len(in) == 0 {
		return nil, Coded(ErrInvalid, CodeRoleLocationRequired,
			"a role says where its work is — at least one entry, and "+
				"\"anywhere\" is a remote entry with no country").
			WithDetail(map[string]any{"field": "locations"})
	}

	workplace, err := s.orgs.LockWorkplace(ctx, tx, org)
	if err != nil {
		return nil, fmt.Errorf("locking organization %s: %w", org, err)
	}
	all, err := s.orgs.AllAddresses(ctx, tx, org)
	if err != nil {
		return nil, fmt.Errorf("reading the addresses of organization %s: %w", org, err)
	}
	addresses := make(map[domain.AddressID]domain.Address, len(all))
	for _, a := range all {
		addresses[a.ID] = a
	}

	var (
		out                 = make([]domain.RoleLocation, 0, len(in))
		seen                = map[string]bool{}
		anywhere, inCountry bool
		exclusiveAt         int
	)
	for i, raw := range in {
		position := i + 1
		l := domain.RoleLocation{Mode: raw.Mode, Country: strings.ToUpper(strings.TrimSpace(raw.Country))}
		if raw.AddressID != nil && strings.TrimSpace(string(*raw.AddressID)) != "" {
			id := domain.AddressID(strings.TrimSpace(string(*raw.AddressID)))
			l.AddressID = &id
		}

		switch l.Mode {
		case domain.ModeRemote:
			if l.AddressID != nil {
				return nil, locationRefusal(CodeRoleLocationInvalid, position,
					"a remote entry never points at an address")
			}
			if l.Country != "" && !validCountryCode(l.Country) {
				return nil, locationRefusal(CodeRoleLocationInvalid, position,
					"%q is not a two-letter country code", raw.Country)
			}
		case domain.ModeOnsite:
			if l.Country != "" {
				return nil, locationRefusal(CodeRoleLocationInvalid, position,
					"an onsite entry names no country of its own — its address does")
			}
			if workplace == domain.WorkplaceFullyRemote {
				return nil, locationRefusal(CodeOrganizationFullyRemote, position,
					"a fully remote organisation posts no onsite entry")
			}
			if l.AddressID == nil {
				return nil, locationRefusal(CodeOnsiteNeedsAddress, position,
					"an onsite entry needs one of the organisation's addresses")
			}
			a, ok := addresses[*l.AddressID]
			if !ok {
				return nil, locationRefusal(CodeAddressNotFound, position,
					"no such address in this organisation")
			}
			if a.Removed() {
				return nil, locationRefusal(CodeAddressRemoved, position,
					"that address has been removed")
			}
			l.Address = &a
		default:
			return nil, locationRefusal(CodeRoleLocationInvalid, position,
				"%q is not remote or onsite", raw.Mode)
		}

		// A repeated entry is collapsed, not refused (decision 3).
		key := locationKey(l)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, l)

		if l.Mode == domain.ModeRemote {
			if l.Country == "" {
				anywhere = true
			} else {
				inCountry = true
			}
			if anywhere && inCountry && exclusiveAt == 0 {
				exclusiveAt = position
			}
		}
	}

	if exclusiveAt > 0 {
		return nil, locationRefusal(CodeRemoteAnywhereExclusive, exclusiveAt,
			"remote from anywhere and remote within a country contradict each other")
	}
	domain.SortLocations(out)
	return out, nil
}
