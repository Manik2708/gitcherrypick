// Package e2e is a runner, not a collection of test cases. Every case lives in
// fixtures/ as JSON; adding one means adding a file, never writing Go.
//
// The format is specified in fixtures/README.md and enforced by
// fixtures/fixture.schema.json plus scripts/validate_fixtures.py. This file is
// the Go view of that same format — if the two disagree, the schema wins,
// because it is what CI runs against.
package e2e

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Case is one fixture file: a named sequence of request/expect pairs run against
// a database seeded with the named sets.
type Case struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Seed        []string `json:"seed"`
	Steps       []Step   `json:"steps"`

	// Path is where this came from, so a failure names a file rather than a
	// subtest. Not part of the JSON.
	Path string `json:"-"`
}

// Step is a single interaction. `As` names a seeded principal; empty means
// anonymous. `Capture` binds response values for later steps.
type Step struct {
	As      string            `json:"as,omitempty"`
	Request Request           `json:"request"`
	Expect  Expect            `json:"expect"`
	Capture map[string]string `json:"capture,omitempty"`
	Fake    *Fake             `json:"fake,omitempty"`
}

// Request is what the step sends. Method may be an HTTP verb or one of the
// harness pseudo-methods below, which drive the evaluator, the clock, and the
// daily jobs rather than the HTTP surface.
type Request struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    json.RawMessage   `json:"body,omitempty"`
}

// Pseudo-methods. These are not endpoints and must never appear in the routing
// table — they exist because a fixture has to be able to advance time and run a
// background job deterministically.
const (
	MethodRunEvaluator     = "RUN_EVALUATOR"
	MethodRunOverdueSweep  = "RUN_OVERDUE_SWEEP"
	MethodAdvanceClock     = "ADVANCE_CLOCK"
	MethodRedeliverLastJob = "REDELIVER_LAST_JOB"
	MethodRejectNReevals   = "REJECT_N_REEVALUATIONS"
)

// IsPseudo reports whether the step is driven by the harness rather than sent
// over HTTP.
func (r Request) IsPseudo() bool {
	switch r.Method {
	case MethodRunEvaluator, MethodRunOverdueSweep, MethodAdvanceClock,
		MethodRedeliverLastJob, MethodRejectNReevals:
		return true
	}
	return false
}

// Expect is the snapshot. Body is compared after normalisation; DB assertions
// cover state the API does not return.
type Expect struct {
	Status int                    `json:"status"`
	Body   json.RawMessage        `json:"body,omitempty"`
	DB     map[string]DBAssertion `json:"db,omitempty"`
}

// DBAssertion is a SQL check against one table. Count, AllMatch, NoneMatch and
// SameValue are independent; any combination may be present.
type DBAssertion struct {
	Where     map[string]any `json:"where,omitempty"`
	Count     *int           `json:"count,omitempty"`
	AllMatch  map[string]any `json:"all_match,omitempty"`
	NoneMatch map[string]any `json:"none_match,omitempty"`
	SameValue []string       `json:"same_value,omitempty"`
}

// Fake is per-step external state. Every dependency is an interface (ADR-0001),
// so a fixture states the facts it needs and nothing is fetched.
type Fake struct {
	GitHub json.RawMessage `json:"github,omitempty"`
	Google json.RawMessage `json:"google,omitempty"`
	AI     json.RawMessage `json:"ai,omitempty"`
	Clock  *ClockFake      `json:"clock,omitempty"`
	Rubric json.RawMessage `json:"rubric,omitempty"`
}

// ClockFake sets or advances time. Exactly one field is populated — the schema
// enforces maxProperties: 1, because "set and then advance" has two readings.
type ClockFake struct {
	Set     string `json:"set,omitempty"`
	Advance string `json:"advance,omitempty"`
}

// UnmarshalJSON drops `_`-prefixed comment keys before decoding. Comments are
// part of the format: a fixture nobody can explain is a fixture nobody can
// safely change.
func stripComments(raw []byte) ([]byte, error) {
	var node any
	if err := json.Unmarshal(raw, &node); err != nil {
		return nil, err
	}
	return json.Marshal(strip(node))
}

func strip(node any) any {
	switch v := node.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, value := range v {
			if strings.HasPrefix(key, "_") {
				continue
			}
			out[key] = strip(value)
		}
		return out
	case []any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			out = append(out, strip(item))
		}
		return out
	default:
		return node
	}
}

// LoadCase reads and decodes one fixture.
func LoadCase(path string) (*Case, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read fixture %s: %w", path, err)
	}
	clean, err := stripComments(raw)
	if err != nil {
		return nil, fmt.Errorf("parse fixture %s: %w", path, err)
	}

	var c Case
	decoder := json.NewDecoder(strings.NewReader(string(clean)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&c); err != nil {
		return nil, fmt.Errorf("decode fixture %s: %w", path, err)
	}
	c.Path = path

	if len(c.Steps) == 0 {
		return nil, fmt.Errorf("fixture %s has no steps", path)
	}
	return &c, nil
}

// LoadAll walks the controllers tree and returns every case, sorted by path so
// the suite runs in a stable order.
func LoadAll(root string) ([]*Case, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".json") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %s: %w", root, err)
	}
	sort.Strings(paths)

	cases := make([]*Case, 0, len(paths))
	for _, path := range paths {
		c, err := LoadCase(path)
		if err != nil {
			return nil, err
		}
		cases = append(cases, c)
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("no fixtures found under %s", root)
	}
	return cases, nil
}

// SubtestName is the fixture's path relative to the controllers root, without
// the extension — "claims/seven_day_lock". Using the path rather than the
// human name keeps `-run TestFixtures/claims` working.
func (c *Case) SubtestName(root string) string {
	rel, err := filepath.Rel(root, c.Path)
	if err != nil {
		rel = filepath.Base(c.Path)
	}
	return strings.TrimSuffix(filepath.ToSlash(rel), ".json")
}
