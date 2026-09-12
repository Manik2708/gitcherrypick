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

	// AI is served verbatim. The shape is the fixture's `fake.ai` block, which
	// is what the evaluator's adapter decodes — this server never looks inside.
	AI AIState `json:"ai"`
}

// AIState is the model's half of a fixture.
type AIState struct {
	Judgements  json.RawMessage `json:"judgements,omitempty"`
	Suggestions json.RawMessage `json:"suggested_skills,omitempty"`

	// Refused mirrors a stop_reason of "refusal", which the real API delivers
	// as an HTTP 200 — the case an evaluator checking only the status would
	// score as an empty judgement.
	Refused bool `json:"refused,omitempty"`
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

	// origin is where the clock was pinned at startup. Kept separately so
	// loading a fixture can reset the offset without un-pinning the run.
	origin time.Duration
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
	// Back to the PINNED origin, not to zero. /_load resets the data a fixture
	// declares; where the clock starts is configuration for the whole run, and
	// resetting it here would un-pin time on the first fixture.
	s.offset = s.origin
}

// AI returns the model's half of the loaded fixture.
func (s *Store) AI() AIState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.AI
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

// GitHubGrants returns every loaded authorization code.
//
// Only the development sign-in page uses this: it needs the whole cast to
// render a list, where every other reader resolves one code it was given.
func (s *Store) GitHubGrants() map[string]GitHubGrant {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make(map[string]GitHubGrant, len(s.state.GitHub.OAuthCodes))
	for code, grant := range s.state.GitHub.OAuthCodes {
		out[code] = grant
	}
	return out
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

// PinTo starts the clock at a fixed instant.
//
// Expressed as an offset from the host's clock because that is what the
// adapter polls: the API adds it to its own time, so pinning here pins there
// without either side needing to agree on an absolute.
func (s *Store) PinTo(at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.origin = time.Until(at)
	s.offset = s.origin
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
