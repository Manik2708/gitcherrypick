package e2e

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// Normalisation replaces values that differ every run, so a snapshot stays
// stable and reviewable. The table is fixtures/README.md's, in code.
//
// Scores are deliberately absent. They are the thing under test, and a rubric
// change that moves them must show up as a failing snapshot.

var (
	uuidRe = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	// RFC3339, with or without fractional seconds, Z or offset.
	timestampRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$`)
	// A bare date, as tentative_result_date uses.
	dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

// Placeholders a fixture may write in place of a real value.
const (
	PlaceholderUUID      = "<uuid>"
	PlaceholderTimestamp = "<timestamp>"
	PlaceholderToken     = "<token>"
	PlaceholderNumber    = "<number>"
	PlaceholderString    = "<string>"

	// PlaceholderAuthorizeURL matches a provider's authorization endpoint.
	//
	// The HOST is configuration, not behaviour: the suite redirects every
	// third party by base URL and runs the same code production does
	// (ADR-0010). Pinning github.com here would assert which environment the
	// test ran in. What the case is actually about — the authorize path, the
	// configured client id, the requested scopes, and the state just issued —
	// is checked instead.
	PlaceholderAuthorizeURL = "<authorize_url>"
)

// numeric fields whose exact value is an implementation detail.
var opaqueNumberFields = map[string]bool{
	"duration_ms":   true,
	"input_tokens":  true,
	"output_tokens": true,
}

// Compare checks an actual response body against the expected snapshot,
// applying normalisation. It returns a human-readable diff, or "" when they
// match.
//
// The expected side drives the comparison: a placeholder matches any value of
// the right shape, and anything else must be equal. Extra keys in the actual
// body ARE a failure — an endpoint that returns more than the snapshot records
// is an unreviewed change to the API surface.
func Compare(expected, actual json.RawMessage) string {
	if len(expected) == 0 {
		if len(actual) == 0 || string(actual) == "null" {
			return ""
		}
		return fmt.Sprintf("expected no body, got %s", truncate(string(actual)))
	}

	var want, got any
	if err := json.Unmarshal(expected, &want); err != nil {
		return fmt.Sprintf("expected body is not valid JSON: %v", err)
	}
	if err := json.Unmarshal(actual, &got); err != nil {
		return fmt.Sprintf("response is not valid JSON: %v\nbody: %s", err, truncate(string(actual)))
	}

	var problems []string
	compare("$", want, got, &problems)
	return strings.Join(problems, "\n")
}

func compare(path string, want, got any, problems *[]string) {
	switch w := want.(type) {

	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			*problems = append(*problems, mismatch(path, want, got))
			return
		}
		keys := make([]string, 0, len(w))
		for k := range w {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			gv, present := g[k]
			if !present {
				*problems = append(*problems, fmt.Sprintf("%s.%s: missing from response", path, k))
				continue
			}
			compare(path+"."+k, w[k], gv, problems)
		}
		// Unexpected keys matter: the snapshot is the whole contract.
		for k := range g {
			if _, expected := w[k]; !expected {
				*problems = append(*problems,
					fmt.Sprintf("%s.%s: present in response but not in the snapshot (%s)", path, k, truncateValue(g[k])))
			}
		}

	case []any:
		g, ok := got.([]any)
		if !ok {
			*problems = append(*problems, mismatch(path, want, got))
			return
		}
		if len(w) != len(g) {
			*problems = append(*problems,
				fmt.Sprintf("%s: expected %d element(s), got %d", path, len(w), len(g)))
			return
		}
		// Order is compared as-is. An endpoint returning an unstable order is a
		// defect in the endpoint, not something the runner should paper over.
		for i := range w {
			compare(fmt.Sprintf("%s[%d]", path, i), w[i], g[i], problems)
		}

	case string:
		if !matchScalar(path, w, got) {
			*problems = append(*problems, mismatch(path, want, got))
		}

	default:
		// Numbers, booleans, null. json.Unmarshal gives float64 for every
		// number, so this compares like with like.
		if !equalScalar(path, want, got) {
			*problems = append(*problems, mismatch(path, want, got))
		}
	}
}

// matchScalar handles the placeholder vocabulary.
func matchScalar(path, want string, got any) bool {
	switch want {
	case PlaceholderUUID:
		s, ok := got.(string)
		return ok && uuidRe.MatchString(s)
	case PlaceholderTimestamp:
		s, ok := got.(string)
		return ok && (timestampRe.MatchString(s) || dateRe.MatchString(s))
	case PlaceholderToken:
		s, ok := got.(string)
		// Opaque and unguessable is the whole point, so the only assertion
		// worth making is that it is not trivially short.
		return ok && len(s) >= 16
	case PlaceholderNumber:
		_, ok := got.(float64)
		return ok
	case PlaceholderString:
		_, ok := got.(string)
		return ok
	case PlaceholderAuthorizeURL:
		s, ok := got.(string)
		return ok && isAuthorizeURL(s)
	}
	s, ok := got.(string)
	return ok && s == want
}

// isAuthorizeURL checks the parts of an OAuth redirect that are behaviour.
func isAuthorizeURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || !strings.HasSuffix(u.Path, "/login/oauth/authorize") {
		return false
	}
	query := u.Query()

	// The scope is pinned EXACTLY, and it must include user:email.
	//
	// read:user alone cannot reach /user/emails, so a contributor whose GitHub
	// profile hides their address signs in and the platform has no way to
	// notify them. That failed against github.com and passed here, because the
	// stand-in serves whatever it is asked for — which is exactly why this
	// assertion is worth having.
	//
	// Exact rather than "contains": a scope quietly growing is a widening of
	// what the platform may read from someone's account, and that should
	// require editing this line.
	return query.Get("client_id") != "" &&
		query.Get("scope") == "read:user user:email" &&
		len(query.Get("state")) >= 16
}

func equalScalar(path string, want, got any) bool {
	// Fields whose exact value is an implementation detail are compared only
	// for type, even when a fixture wrote a literal.
	field := path[strings.LastIndex(path, ".")+1:]
	if opaqueNumberFields[field] {
		_, ok := got.(float64)
		return ok
	}
	return want == got
}

func mismatch(path string, want, got any) string {
	return fmt.Sprintf("%s: expected %s, got %s", path, truncateValue(want), truncateValue(got))
}

func truncateValue(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return truncate(string(b))
}

func truncate(s string) string {
	const max = 200
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// truncateLong bounds a whole response body rather than one value.
//
// Separate from truncate: 200 characters is the right size for "which value
// differed", and far too small for "here is what to write instead".
func truncateLong(s string) string {
	const max = 6000
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// Normalise rewrites a live response into the form a snapshot records, so
// `-update` can write it back. It is the inverse of the placeholder matching
// above and must stay consistent with it.
func Normalise(raw json.RawMessage) (json.RawMessage, error) {
	var node any
	if err := json.Unmarshal(raw, &node); err != nil {
		return nil, err
	}
	return json.Marshal(normalise("", node))
}

func normalise(field string, node any) any {
	switch v := node.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, val := range v {
			out[k] = normalise(k, val)
		}
		return out
	case []any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			out = append(out, normalise(field, item))
		}
		return out
	case string:
		switch {
		case uuidRe.MatchString(v):
			return PlaceholderUUID
		case timestampRe.MatchString(v):
			return PlaceholderTimestamp
		case isOpaqueToken(field, v):
			return PlaceholderToken
		}
		return v
	case float64:
		if opaqueNumberFields[field] {
			return PlaceholderNumber
		}
		return v
	default:
		return node
	}
}

func isOpaqueToken(field, value string) bool {
	switch field {
	case "token", "refresh_token", "access_token", "share_token":
		return len(value) >= 16
	case "url":
		// `url` is NOT opaque by itself, and treating it as such was a trap.
		//
		// It meant one thing when this rule was written — a share link,
		// "/public/scorecard/<43 random characters>", minted fresh every run.
		// It now also means a pull request, ".../acme/platform/pull/55", which
		// is stable and is the entire point of the field: a verdict you cannot
		// click through to is hard to check and harder to argue with.
		//
		// Normalising both wrote `"url": "<token>"` into 24 snapshots — a
		// placeholder matching any string over 16 characters. The link a
		// contributor is shown would have been asserted to be *something*, and
		// nothing more, while every fixture stayed green.
		//
		// The opacity lives in the LAST SEGMENT, which is exactly what differs
		// between the two: a minted token, or a small integer.
		return isOpaqueSegment(lastSegment(value))
	}
	return false
}

// lastSegment returns the part after the final "/".
func lastSegment(value string) string {
	if i := strings.LastIndexByte(value, '/'); i >= 0 {
		return value[i+1:]
	}
	return value
}

// isOpaqueSegment reports whether a path segment is a secret rather than a name.
//
// Length is the test, as it is for every other token here: a minted token is 43
// base64url characters, and no stable segment this suite records comes close —
// a PR number, a repo and an owner are all far shorter.
func isOpaqueSegment(segment string) bool { return len(segment) >= 16 }
