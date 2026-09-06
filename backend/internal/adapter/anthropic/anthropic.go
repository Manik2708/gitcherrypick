// Package anthropic implements port.AIClient against the Anthropic Batch API.
//
// The base URL is configuration (ADR-0010): production points at
// api.anthropic.com, the integration suite points at cmd/fakethirdparty, and
// the code under test is identical either way. There is no fake implementation
// of this interface anywhere in the tree.
package anthropic

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/Manik2708/gitcherrypick/backend/internal/adapter/httpx"
	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// Config is what main wires from flags.
type Config struct {
	BaseURL string
	APIKey  string
	Model   string

	// PollEvery is how often a submitted batch is checked. Batches are
	// asynchronous and may take up to 24 hours in production (ADR-0006); the
	// suite's stand-in answers immediately, so this only governs how quickly
	// the first poll follows the submit.
	PollEvery time.Duration

	Client *httpx.Client
}

// DefaultPoll is the gap between batch polls.
const DefaultPoll = 250 * time.Millisecond

// Client is the Anthropic adapter.
type Client struct {
	base   string
	key    string
	model  string
	poll   time.Duration
	client *httpx.Client
}

// New builds the adapter.
func New(cfg Config) *Client {
	c := &Client{
		base: trimSlash(cfg.BaseURL), key: cfg.APIKey,
		model: cfg.Model, poll: cfg.PollEvery, client: cfg.Client,
	}
	if c.base == "" {
		c.base = "https://api.anthropic.com"
	}
	if c.model == "" {
		c.model = "claude-opus-5"
	}
	if c.poll <= 0 {
		c.poll = DefaultPoll
	}
	if c.client == nil {
		c.client = httpx.New(nil, nil)
	}
	return c
}

var _ port.AIClient = (*Client)(nil)

func trimSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

// judgementPayload is one (PR, skill) verdict on the wire.
type judgementPayload struct {
	PRNumber               int                         `json:"pr_number"`
	SkillSlug              string                      `json:"skill_slug"`
	Disqualified           bool                        `json:"disqualified"`
	DisqualificationReason *string                     `json:"disqualification_reason"`
	Dimensions             map[string]dimensionPayload `json:"dimensions"`
	RelativeShare          *float64                    `json:"relative_share"`
}

type dimensionPayload struct {
	Score  int    `json:"score"`
	Remark string `json:"remark"`
}

// suggestionPayload is a skill the model noticed that nobody declared.
type suggestionPayload struct {
	Slug      string `json:"slug"`
	Rationale string `json:"rationale"`
}

// batchResult is what the results endpoint returns once a batch has finished.
type batchResult struct {
	Judgements  []judgementPayload  `json:"judgements"`
	Suggestions []suggestionPayload `json:"suggested_skills"`

	// Refused reflects a stop_reason of "refusal", which arrives as HTTP 200.
	// A caller checking only the status would score the claim zero on it.
	Refused bool `json:"refused"`

	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// batchCreated is the submit response.
type batchCreated struct {
	ID              string `json:"id"`
	ProcessingState string `json:"processing_state"`
}

// batchStatus is one poll of a submitted batch.
type batchStatus struct {
	ID              string `json:"id"`
	ProcessingState string `json:"processing_state"`
}

// batchRequest is what a claim looks like on the way in.
type batchRequest struct {
	Model            string    `json:"model"`
	ClaimID          string    `json:"claim_id"`
	RubricVersion    string    `json:"rubric_version"`
	ScoringMode      string    `json:"scoring_mode"`
	NominatedPrimary string    `json:"nominated_primary"`
	Skills           []string  `json:"skills"`
	PRs              []batchPR `json:"prs"`
}

type batchPR struct {
	Position  int    `json:"position"`
	RepoOwner string `json:"repo_owner"`
	RepoName  string `json:"repo_name"`
	PRNumber  int    `json:"pr_number"`
	Role      string `json:"role"`
}

// Judge submits one claim and waits for its verdicts.
//
// Submit, poll, fetch — the Batch API's three steps (ADR-0006). Batching is an
// implementation detail: the service asks for a judgement and does not know
// whether one request or a queue of them went out.
func (c *Client) Judge(ctx context.Context, req port.JudgeRequest) (*port.JudgeResponse, error) {
	started := time.Now()

	body := batchRequest{
		Model: c.model, ClaimID: string(req.ClaimID),
		RubricVersion: req.RubricVersion, ScoringMode: string(req.ScoringMode),
		NominatedPrimary: req.NominatedPrimary,
	}
	for _, s := range req.Skills {
		body.Skills = append(body.Skills, s.Slug)
	}
	for _, pr := range req.Evidence {
		body.PRs = append(body.PRs, batchPR{
			Position: pr.Position, RepoOwner: pr.RepoOwner, RepoName: pr.RepoName,
			PRNumber: pr.PRNumber, Role: string(pr.Role),
		})
	}

	var created batchCreated
	if err := c.post(ctx, "/v1/messages/batches", body, &created); err != nil {
		return nil, err
	}

	if err := c.await(ctx, created); err != nil {
		return nil, err
	}

	var result batchResult
	if err := c.get(ctx, "/v1/messages/batches/"+created.ID+"/results", &result); err != nil {
		return nil, err
	}
	return decode(result, started), nil
}

// await polls until the batch has ended.
func (c *Client) await(ctx context.Context, created batchCreated) error {
	if ended(created.ProcessingState) {
		return nil
	}

	ticker := time.NewTicker(c.poll)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for batch %s: %w", created.ID, ctx.Err())
		case <-ticker.C:
			var status batchStatus
			if err := c.get(ctx, "/v1/messages/batches/"+created.ID, &status); err != nil {
				return err
			}
			if ended(status.ProcessingState) {
				return nil
			}
		}
	}
}

func ended(state string) bool { return state == "" || state == "ended" }

// decode turns the wire payload into the domain's judgements.
func decode(r batchResult, started time.Time) *port.JudgeResponse {
	out := &port.JudgeResponse{
		Refused:      r.Refused,
		InputTokens:  r.InputTokens,
		OutputTokens: r.OutputTokens,
		DurationMS:   int(time.Since(started).Milliseconds()),
	}

	for _, j := range r.Judgements {
		judgement := domain.Judgement{
			PRNumber: j.PRNumber, SkillSlug: j.SkillSlug,
			Disqualified:  j.Disqualified,
			RelativeShare: j.RelativeShare,
		}
		if j.DisqualificationReason != nil {
			reason := domain.RejectionReason(*j.DisqualificationReason)
			judgement.DisqualificationReason = &reason
		}
		if len(j.Dimensions) > 0 {
			judgement.Dimensions = make(map[string]domain.Dimension, len(j.Dimensions))
			for name, d := range j.Dimensions {
				judgement.Dimensions[name] = domain.Dimension{Score: d.Score, Remark: d.Remark}
			}
		}
		out.Judgements = append(out.Judgements, judgement)
	}

	for _, s := range r.Suggestions {
		out.Suggestions = append(out.Suggestions, domain.ClaimSkill{
			Slug: s.Slug, Origin: domain.AISuggested, Rationale: s.Rationale,
		})
	}
	return out
}

func (c *Client) post(ctx context.Context, path string, body, into any) error {
	return c.client.PostJSON(ctx, c.base+path, "", "the anthropic batch api", body, into, c.headers())
}

func (c *Client) get(ctx context.Context, path string, into any) error {
	return c.client.GetJSON(ctx, c.base+path, "", "the anthropic batch api", into, c.headers())
}

func (c *Client) headers() http.Header {
	h := http.Header{}
	h.Set("x-api-key", c.key)
	h.Set("anthropic-version", "2023-06-01")
	h.Set("content-type", "application/json")
	return h
}
