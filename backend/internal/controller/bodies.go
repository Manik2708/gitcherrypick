package controller

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
)

// Shared response shapes.
//
// One definition per concept, used by every controller that returns it. Two
// controllers each declaring their own "skill" body would drift, and a client
// would find the same thing shaped differently depending on which endpoint it
// came from.

// userSkillBody is a contributor's standing in one skill.
//
// Stale is per skill as well as per response: a sweep reaches skills one at a
// time, so a contributor can hold one current standing and one superseded, and
// a single top-level flag would not say which is which.
type userSkillBody struct {
	Slug            string     `json:"slug"`
	Standing        string     `json:"standing"`
	DistinctPRCount int        `json:"distinct_pr_count"`
	Score           float64    `json:"score"`
	RubricVersion   string     `json:"rubric_version"`
	Stale           bool       `json:"stale"`
	PromotedAt      *time.Time `json:"promoted_at,omitempty"`
}

func userSkillBodies(skills []domain.UserSkill, current string) []userSkillBody {
	// Never nil: an empty list must serialize as [] rather than null, because
	// a client iterating the result should not have to special-case one.
	out := make([]userSkillBody, 0, len(skills))
	for _, s := range skills {
		out = append(out, userSkillBody{
			Slug:            s.Slug,
			Standing:        string(s.Standing),
			DistinctPRCount: s.DistinctPRCount,
			Score:           s.Score,
			RubricVersion:   s.RubricVersion,
			Stale:           s.RubricVersion != "" && s.RubricVersion != current,
			PromotedAt:      s.PromotedAt,
		})
	}
	return out
}

// rankPositionBody is a score and where it sits.
//
// Both are pointers. A contributor with no primary skill has no score, and one
// who opted out has a score but no position (ADR-0008 §1a) — collapsing either
// to zero would report a measurement that was never made.
type rankPositionBody struct {
	Score *float64 `json:"score"`
	Rank  *int     `json:"rank"`
	OutOf int      `json:"out_of"`
}

type skillRankBody struct {
	Slug     string  `json:"slug"`
	Name     string  `json:"name"`
	Standing string  `json:"standing"`
	Score    float64 `json:"score"`
	Rank     *int    `json:"rank"`
	OutOf    *int    `json:"out_of"`
}

type myRankBody struct {
	RubricVersion   string  `json:"rubric_version"`
	Ranked          bool    `json:"ranked"`
	UnrankedReason  *string `json:"unranked_reason"`
	Active          bool    `json:"active"`
	InactiveForDays *int    `json:"inactive_for_days,omitempty"`

	Overall    rankPositionBody `json:"overall"`
	Generalist rankPositionBody `json:"generalist"`
	Skills     []skillRankBody  `json:"skills"`
}

func rankBody(r *domain.Rank) myRankBody {
	if r == nil {
		return myRankBody{Skills: []skillRankBody{}}
	}

	out := myRankBody{
		RubricVersion:   r.RubricVersion,
		Ranked:          r.Ranked,
		Active:          r.Active,
		InactiveForDays: r.InactiveForDays,
		Overall:         positionBody(r.Overall),
		Generalist:      positionBody(r.Generalist),
		Skills:          make([]skillRankBody, 0, len(r.Skills)),
	}
	if r.UnrankedReason != "" {
		out.UnrankedReason = &r.UnrankedReason
	}
	for _, s := range r.Skills {
		out.Skills = append(out.Skills, skillRankBody{
			Slug: s.Slug, Name: s.Name, Standing: string(s.Standing),
			Score: s.Score, Rank: s.Rank, OutOf: s.OutOf,
		})
	}
	return out
}

func positionBody(p domain.RankPosition) rankPositionBody {
	return rankPositionBody{Score: p.Score, Rank: p.Rank, OutOf: p.OutOf}
}

// contactRequestBody is one hiring approach as the contributor sees it.
//
// The organization is named; the individual recruiter is not. A contributor
// decides about a company, and RequestedBy is an internal audit field.
// contactOrgBody is the company asking, as the contributor sees it.
//
// Name and payment status, and no id: a contributor decides about a company
// they can recognise, not one they can look up.
type contactOrgBody struct {
	Name            string `json:"name"`
	Verified        bool   `json:"verified"`
	PaymentVerified bool   `json:"payment_verified"`
}

type contactRequestBody struct {
	ID           domain.ContactID `json:"id"`
	Organization contactOrgBody   `json:"organization"`
	Status       string           `json:"status"`

	// A DAY, not an instant: "we will decide by the 30th" is the promise that
	// was made, and rendering it with a time attaches precision nobody offered.
	TentativeResultDate Date `json:"tentative_result_date"`

	// Disclosure is the warning ADR-0002 §5 requires when a company's payment
	// is unverified, and null when there is nothing to warn about. Present
	// either way, so a client renders one field rather than inferring it.
	Disclosure *string `json:"disclosure"`

	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

// unverifiedPaymentDisclosure is shown before a contributor releases their
// address to a company whose payment has not been verified.
const unverifiedPaymentDisclosure = "This organization is hiring for the first time and has not verified payment capability."

func contactBody(c domain.ContactRequest) contactRequestBody {
	out := contactRequestBody{
		ID: c.ID,
		Organization: contactOrgBody{
			Name:     c.OrganizationName,
			Verified: c.OrganizationVerified, PaymentVerified: c.PaymentVerified,
		},
		Status:              string(c.Status),
		TentativeResultDate: Date{Time: c.TentativeResultDate},
		ExpiresAt:           c.ExpiresAt,
		CreatedAt:           c.CreatedAt,
	}
	if !c.PaymentVerified {
		disclosure := unverifiedPaymentDisclosure
		out.Disclosure = &disclosure
	}
	return out
}

func contactBodies(requests []domain.ContactRequest) []contactRequestBody {
	out := make([]contactRequestBody, 0, len(requests))
	for _, c := range requests {
		out = append(out, contactBody(c))
	}
	return out
}

// fieldError names one invalid input.
//
// A machine-readable field and reason so a client can highlight the offending
// input rather than parse a sentence, plus whatever context makes the failure
// actionable: the maximum that was exceeded, the spelling that was probably
// meant, the other claim that already owns a pair.
//
// Everything but Reason is optional, because what a caller needs differs by
// failure — "too many" needs a limit, "unknown filter" needs a suggestion, and
// a bad evidence row needs its position rather than a field name.
type fieldError struct {
	Field    string `json:"field,omitempty"`
	Position int    `json:"position,omitempty"`
	Reason   string `json:"reason"`
	Message  string `json:"message,omitempty"`

	Max      *int `json:"max,omitempty"`
	Provided *int `json:"provided,omitempty"`

	Allowed    []string `json:"allowed,omitempty"`
	DidYouMean string   `json:"did_you_mean,omitempty"`
	Value      string   `json:"value,omitempty"`

	Skill              string          `json:"skill,omitempty"`
	ConflictingClaimID *domain.ClaimID `json:"conflicting_claim_id,omitempty"`
}

// intPtr is for the optional numeric context above, which must distinguish a
// limit of zero from no limit at all.
func intPtr(v int) *int { return &v }

// writeFieldErrors reports one or more invalid inputs.
//
// Every failure is reported rather than only the first: a contributor fixing a
// claim, or a hirer fixing a query, should not have to submit repeatedly to
// discover one problem at a time.
func writeFieldErrors(w http.ResponseWriter, status int, code string, items []fieldError) {
	writeDetail(w, status, map[string]any{"error": code, "items": items})
}

// resultSkillBody is a skill as it appears on a search result or scorecard.
//
// No name: a search result lists slugs a hirer already filtered on, and the
// catalogue is where names live.
type resultSkillBody struct {
	Slug     string  `json:"slug"`
	Standing string  `json:"standing"`
	Score    float64 `json:"score"`
	Rank     *int    `json:"rank"`
}

func resultSkillBodies(skills []domain.ResultSkill) []resultSkillBody {
	out := make([]resultSkillBody, 0, len(skills))
	for _, s := range skills {
		out = append(out, resultSkillBody{
			Slug: s.Slug, Standing: string(s.Standing),
			Score: s.Score, Rank: s.Rank,
		})
	}
	return out
}

// Date is a calendar day, without a time.
//
// A tentative result date is a DAY — "we will decide by the 15th" — and the
// fixtures state it as 2026-09-15. Decoding that into a time.Time directly
// fails, because encoding/json wants RFC 3339 and would reject the shape every
// client actually sends.
//
// It accepts a full timestamp too, so a client that sends one is not punished
// for being precise.
type Date struct{ time.Time }

const dateLayout = "2006-01-02"

// UnmarshalJSON accepts a calendar day or a full timestamp.
func (d *Date) UnmarshalJSON(raw []byte) error {
	text := strings.Trim(string(raw), `"`)
	if text == "" || text == "null" {
		return nil
	}

	if parsed, err := time.Parse(dateLayout, text); err == nil {
		d.Time = parsed
		return nil
	}
	parsed, err := time.Parse(time.RFC3339, text)
	if err != nil {
		return fmt.Errorf("%q is not a date (2006-01-02) or a timestamp", text)
	}
	d.Time = parsed
	return nil
}

// MarshalJSON writes the day back in the shape it arrived in.
func (d Date) MarshalJSON() ([]byte, error) {
	if d.IsZero() {
		return []byte("null"), nil
	}
	return []byte(`"` + d.Format(dateLayout) + `"`), nil
}

// orgRef names the organization a resource belongs to.
//
// Filled from the ACTING PRINCIPAL rather than fetched: a service refuses any
// hirer who reaches outside their own organization, so by the time a response
// is built the two are the same. Fetching it again would be a query to confirm
// something already enforced.
type orgRef struct {
	ID   domain.OrganizationID `json:"id"`
	Name string                `json:"name"`
}

func orgRefOf(p domain.Principal) *orgRef {
	if p.Hirer == nil || p.Hirer.Organization == nil {
		return nil
	}
	return &orgRef{ID: p.Hirer.Organization.ID, Name: p.Hirer.Organization.Name}
}
