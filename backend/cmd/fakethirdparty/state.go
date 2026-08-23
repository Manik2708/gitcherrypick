package main

import (
	"encoding/json"
	"sync"
	"time"
)

// State is everything this server will say, and it comes entirely from the
// fixture (ADR-0010).
//
// The provider payloads are json.RawMessage rather than typed structs, and that
// is the point: this binary knows routes, status codes and envelopes, and
// nothing whatsoever about alice or how many stars kubernetes has. A typed
// field here would be a default, and a default is this server inventing test
// data — which is exactly what putting the data in the fixture was meant to
// stop.
type State struct {
	GitHub GitHubState `json:"github"`
	Google GoogleState `json:"google"`
}

// GitHubState is the GitHub half of a fixture's third_party block.
//
// Repositories are keyed "owner/name"; pull requests and reviews are keyed
// "owner/name#number".
type GitHubState struct {
	OAuthCodes   map[string]GitHubGrant     `json:"oauth_codes"`
	Repositories map[string]json.RawMessage `json:"repositories"`
	PullRequests map[string]json.RawMessage `json:"pull_requests"`
	Reviews      map[string]json.RawMessage `json:"reviews"`
}

// GitHubGrant is what one authorization code resolves to.
//
// User is served at /user and Emails at /user/emails, both verbatim. Splitting
// them mirrors GitHub, where the address is on a second endpoint unless the
// contributor made it public — which is the branch the adapter has to handle.
type GitHubGrant struct {
	User   json.RawMessage `json:"user"`
	Emails json.RawMessage `json:"emails"`
}

// GoogleState is the Google half.
type GoogleState struct {
	OAuthCodes map[string]GoogleGrant `json:"oauth_codes"`
}

// GoogleGrant is what one Google authorization code resolves to. UserInfo is
// served verbatim from the userinfo endpoint, including email_verified — so a
// fixture can exercise the unverified-address refusal.
type GoogleGrant struct {
	UserInfo json.RawMessage `json:"userinfo"`
}

// SentEmail is one message the API asked Resend to deliver.
//
// Recorded so a fixture can assert that confirming a shortlist produced exactly
// one email carrying the payment-verified disclosure (ADR-0002 §5).
type SentEmail struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	HTML    string   `json:"html"`
}

// Store holds the loaded state and everything the API has sent.
//
// Guarded by a mutex because the API is a concurrent client: a fixture step may
// submit a claim while a background job is still fetching PR facts.
type Store struct {
	mu    sync.RWMutex
	state State
	sent  []SentEmail

	// grants maps an issued access token back to the code it came from, so
	// /user can tell which identity is asking.
	grants map[string]string

	// offset is how far the API's clock has been advanced (ADR-0012). Held
	// here rather than in the API so that moving time is a request the harness
	// makes, not a handler the API ships.
	offset time.Duration
}

// NewStore returns an empty store. Every fixture calls Load before its first
// step, so starting empty means a fixture that forgot to declare something
// fails loudly rather than inheriting the previous one's data.
func NewStore() *Store {
	return &Store{grants: map[string]string{}}
}

// Load REPLACES everything.
//
// The same isolation rule the database follows, for the same reason: a fixture
// that passes only because the one before it left data behind is a fixture that
// proves nothing.
func (s *Store) Load(state State) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.state = state
	s.sent = nil
	s.grants = map[string]string{}

	// Time resets with everything else. A fixture inheriting the previous
	// one's advanced clock would see locks expire that it never waited out.
	s.offset = 0
}

// Grant records that a token now speaks for a code.
func (s *Store) Grant(token, code string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.grants[token] = code
}

// CodeFor resolves a bearer token to the authorization code it came from.
func (s *Store) CodeFor(token string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	code, ok := s.grants[token]
	return code, ok
}

// GitHubGrantFor resolves a bearer token to the identity behind it.
func (s *Store) GitHubGrantFor(token string) (GitHubGrant, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	code, ok := s.grants[token]
	if !ok {
		return GitHubGrant{}, false
	}
	grant, ok := s.state.GitHub.OAuthCodes[code]
	return grant, ok
}

// GitHubCode looks up an authorization code.
func (s *Store) GitHubCode(code string) (GitHubGrant, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	grant, ok := s.state.GitHub.OAuthCodes[code]
	return grant, ok
}

// GoogleCode looks up a Google authorization code.
func (s *Store) GoogleCode(code string) (GoogleGrant, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	grant, ok := s.state.Google.OAuthCodes[code]
	return grant, ok
}

// Repository, PullRequest and Reviews return declared payloads verbatim.
func (s *Store) Repository(key string) (json.RawMessage, bool) {
	return s.lookup(func(g GitHubState) map[string]json.RawMessage { return g.Repositories }, key)
}

// PullRequest returns the declared pull request payload.
func (s *Store) PullRequest(key string) (json.RawMessage, bool) {
	return s.lookup(func(g GitHubState) map[string]json.RawMessage { return g.PullRequests }, key)
}

// Reviews returns the declared review list.
func (s *Store) Reviews(key string) (json.RawMessage, bool) {
	return s.lookup(func(g GitHubState) map[string]json.RawMessage { return g.Reviews }, key)
}

func (s *Store) lookup(pick func(GitHubState) map[string]json.RawMessage, key string) (json.RawMessage, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	value, ok := pick(s.state.GitHub)[key]
	return value, ok
}

// Advance moves the clock offset forward and returns the new value.
func (s *Store) Advance(by time.Duration) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.offset += by
	return s.offset
}

// Offset is the current clock offset.
func (s *Store) Offset() time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.offset
}

// Record stores a sent email.
func (s *Store) Record(email SentEmail) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, email)
}

// Sent returns every email sent since the last Load, in order.
func (s *Store) Sent() []SentEmail {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]SentEmail, len(s.sent))
	copy(out, s.sent)
	return out
}
