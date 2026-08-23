package main

import (
	"fmt"
	"net/http"
)

// discoveryDocument is the OIDC metadata the adapter fetches to find the token
// and userinfo endpoints.
type discoveryDocument struct {
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	UserInfoEndpoint      string   `json:"userinfo_endpoint"`
	JWKSURI               string   `json:"jwks_uri"`
	ResponseTypes         []string `json:"response_types_supported"`
	SubjectTypes          []string `json:"subject_types_supported"`
	SigningAlgValues      []string `json:"id_token_signing_alg_values_supported"`
}

// googleTokenGrant is the token endpoint's success body.
type googleTokenGrant struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	Scope       string `json:"scope"`
}

// googleTokenError is the failure body, which Google returns with a 4xx.
type googleTokenError struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// googleDiscovery serves the OIDC metadata pointing back at this server.
//
// Discovery has to name ABSOLUTE urls, which is why the binary is told its own
// address. Real Google serves its token and userinfo endpoints from different
// hosts entirely, and the adapter follows this document rather than assuming —
// so this is the route that proves it does.
func (s *Server) googleDiscovery(w http.ResponseWriter, _ *http.Request) {
	base := s.selfURL + "/google"
	writeJSON(w, http.StatusOK, discoveryDocument{
		Issuer:                base,
		AuthorizationEndpoint: base + "/o/oauth2/v2/auth",
		TokenEndpoint:         base + "/token",
		UserInfoEndpoint:      base + "/userinfo",
		JWKSURI:               base + "/certs",
		ResponseTypes:         []string{"code"},
		SubjectTypes:          []string{"public"},
		SigningAlgValues:      []string{"RS256"},
	})
}

// googleToken trades a declared code for a token.
func (s *Server) googleToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "unparseable form", http.StatusBadRequest)
		return
	}
	code := r.Form.Get("code")

	if _, ok := s.store.GoogleCode(code); !ok {
		// Google reports this as 400, unlike GitHub's 200-with-an-error-field.
		// The difference is real and each adapter handles its own.
		writeJSON(w, http.StatusBadRequest, googleTokenError{
			Error:            "invalid_grant",
			ErrorDescription: fmt.Sprintf("The code %q is not declared in this fixture's third_party.google.oauth_codes.", code),
		})
		return
	}

	token := "fake-google-token-" + code
	s.store.Grant(token, code)
	writeJSON(w, http.StatusOK, googleTokenGrant{
		AccessToken: token,
		TokenType:   "Bearer",
		ExpiresIn:   3600,
		Scope:       "openid email profile",
	})
}

// googleUserInfo serves the claim set verbatim.
//
// Including email_verified exactly as the fixture wrote it — present and true,
// present and false, or absent — so a fixture can exercise the adapter's
// refusal of an unverified address.
func (s *Server) googleUserInfo(w http.ResponseWriter, r *http.Request) {
	code, known := s.store.CodeFor(bearer(r))
	if !known {
		writeJSON(w, http.StatusUnauthorized, googleTokenError{
			Error: "invalid_token", ErrorDescription: "Invalid Credentials",
		})
		return
	}

	grant, ok := s.store.GoogleCode(code)
	if !ok || len(grant.UserInfo) == 0 {
		writeJSON(w, http.StatusNotFound, googleTokenError{
			Error: "not_found", ErrorDescription: "no userinfo declared for this code",
		})
		return
	}
	writeRaw(w, http.StatusOK, grant.UserInfo)
}
