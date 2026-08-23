package e2e

import ()

// Principals is the seeded cast, loaded from fixtures/seed/principals.json.
//
// The runner needs it for two things a fixture cannot supply: which sign-in
// path a named principal uses (contributors only exist through GitHub, admins
// have no OAuth path at all — ADR-0002), and the identity to prime the fake
// GitHub adapter with so a real callback resolves to a seeded user.
type Principals struct {
	Contributors  []Contributor  `json:"contributors"`
	Organizations []Organization `json:"organizations"`
	Hirers        []Hirer        `json:"hirers"`
	Admins        []Admin        `json:"admins"`
}

// Organization is a seeded hiring organization.
//
// Verified and PaymentVerified are separate: verification gates hiring
// capability, while payment status is disclosed to a contributor deciding
// whether to release their address (ADR-0002 §5).
type Organization struct {
	Key             string  `json:"key"`
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	Slug            string  `json:"slug"`
	LinkedInURL     *string `json:"linkedin_url"`
	Verified        bool    `json:"verified"`
	PaymentVerified bool    `json:"payment_verified"`
}

// Contributor is a seeded GitHub-authenticated user.
type Contributor struct {
	Key          string `json:"key"`
	ID           string `json:"id"`
	DisplayName  string `json:"display_name"`
	Email        string `json:"email"`
	GitHubUserID int64  `json:"github_user_id"`
	GitHubLogin  string `json:"github_login"`

	// Nil when the contributor never set one. Distinct from a lapsed window,
	// which is a present setting whose expiry has passed.
	Availability *Availability `json:"availability"`
}

// Availability is a seeded discovery window.
//
// ExpiresInDays is relative and may be negative, which is how a fixture seeds a
// lapsed window against the real clock (ADR-0010 §5).
type Availability struct {
	Status        string `json:"status"`
	ExpiresInDays *int   `json:"expires_in_days"`
}

// Hirer is a seeded recruiter. AuthProvider decides which sign-in path the
// runner uses, because a Google account has no password to present.
type Hirer struct {
	Key          string `json:"key"`
	ID           string `json:"id"`
	DisplayName  string `json:"display_name"`
	Email        string `json:"email"`
	AuthProvider string `json:"auth_provider"`
	GitHubUserID *int64 `json:"github_user_id"`
	Organization string `json:"organization"`
	Verified     bool   `json:"verified"`
	OrgRole      string `json:"org_role"`
}

// Admin is a seeded administrator.
type Admin struct {
	Key         string `json:"key"`
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
}

// LoadPrincipals reads the seeded cast.
func LoadPrincipals() (Principals, error) {
	var p Principals
	err := readSeed("principals", &p)
	return p, err
}

// Contributor looks up a seeded contributor by its fixture key.
func (p Principals) Contributor(key string) (Contributor, bool) {
	for _, c := range p.Contributors {
		if c.Key == key {
			return c, true
		}
	}
	return Contributor{}, false
}

// Hirer looks up a seeded recruiter seat by its fixture key.
func (p Principals) Hirer(key string) (Hirer, bool) {
	for _, h := range p.Hirers {
		if h.Key == key {
			return h, true
		}
	}
	return Hirer{}, false
}

// Admin looks up a seeded administrator by its fixture key.
func (p Principals) Admin(key string) (Admin, bool) {
	for _, a := range p.Admins {
		if a.Key == key {
			return a, true
		}
	}
	return Admin{}, false
}

// IsContributor reports whether the key names a seeded contributor, which is
// what decides the sign-in path the runner uses.
func (p Principals) IsContributor(key string) bool { _, ok := p.Contributor(key); return ok }

// IsHirer reports whether the key names a seeded recruiter seat.
func (p Principals) IsHirer(key string) bool { _, ok := p.Hirer(key); return ok }

// IsAdmin reports whether the key names a seeded administrator.
func (p Principals) IsAdmin(key string) bool { _, ok := p.Admin(key); return ok }
