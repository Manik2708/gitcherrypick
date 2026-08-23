package github

import (
	"context"
	"fmt"
	"time"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// userRef is GitHub's abbreviated user object, which appears wherever an actor
// is named. Only the immutable numeric id is read (ADR-0002).
type userRef struct {
	ID int64 `json:"id"`
}

// prBase is the branch a pull request targets. It carries the repository, which
// is why PullRequest needs no second request for it.
type prBase struct {
	Repo repoPayload `json:"repo"`
}

// prPayload is GitHub's pull request object, narrowed to what the rubric uses.
type prPayload struct {
	Title          string     `json:"title"`
	Merged         bool       `json:"merged"`
	MergedAt       *time.Time `json:"merged_at"`
	Additions      int        `json:"additions"`
	Deletions      int        `json:"deletions"`
	ChangedFiles   int        `json:"changed_files"`
	ReviewComments int        `json:"review_comments"`
	Comments       int        `json:"comments"`
	User           userRef    `json:"user"`
	Base           prBase     `json:"base"`
}

// repoPayload is GitHub's repository object.
type repoPayload struct {
	Private         bool `json:"private"`
	Fork            bool `json:"fork"`
	StargazersCount int  `json:"stargazers_count"`
	ForksCount      int  `json:"forks_count"`
}

// reviewPayload is one review on a pull request.
type reviewPayload struct {
	User        userRef    `json:"user"`
	State       string     `json:"state"`
	Body        string     `json:"body"`
	SubmittedAt *time.Time `json:"submitted_at"`
}

func (r repoPayload) facts() domain.RepoFacts {
	return domain.RepoFacts{
		Public: !r.Private,
		Stars:  r.StargazersCount,
		Forks:  r.ForksCount,
		IsFork: r.Fork,

		// Dependents and Downloads stay zero: GitHub's REST API exposes
		// neither. The dependency graph is HTML-only and download counts belong
		// to whichever package registry the project publishes to. Reach drops
		// absent metrics and renormalises the rest (ADR-0005), so a project is
		// not punished for a number we cannot obtain — but the caller must pass
		// these as ABSENT rather than as zero, or it will be.
	}
}

// PullRequest reads the facts a PR claim is validated and scored against.
//
// One request. The PR payload embeds its repository under base.repo, so
// fetching the repo separately would double the rate-limit cost for data
// already in hand.
func (c *Client) PullRequest(ctx context.Context, owner, repo string, number int) (*domain.PRFacts, error) {
	var pr prPayload
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d", owner, repo, number)
	if err := c.get(ctx, path, &pr); err != nil {
		return nil, err
	}

	reviews, err := c.Reviews(ctx, owner, repo, number)
	if err != nil {
		return nil, err
	}

	return &domain.PRFacts{
		Title:          pr.Title,
		Merged:         pr.Merged,
		MergedAt:       pr.MergedAt,
		AuthorUserID:   pr.User.ID,
		Additions:      pr.Additions,
		Deletions:      pr.Deletions,
		ChangedFiles:   pr.ChangedFiles,
		ReviewComments: pr.ReviewComments,
		Reviews:        len(reviews),
		Participants:   participants(pr.User.ID, reviews),
		Repository:     pr.Base.Repo.facts(),
	}, nil
}

// participants counts the distinct people involved.
//
// Derived from the author and the reviewers, because GitHub publishes no
// participant count. It therefore UNDERCOUNTS: someone who only left an issue
// comment is invisible here. Engagement weights participants at 0.30
// (ADR-0005), so this is a real and deliberate conservatism — reading the
// comment threads would cost two more paginated calls per PR on the claim
// submission path, and would still miss reactions.
func participants(author int64, reviews []port.Review) int {
	seen := map[int64]struct{}{}
	if author != 0 {
		seen[author] = struct{}{}
	}
	for _, r := range reviews {
		if r.AuthorUserID != 0 {
			seen[r.AuthorUserID] = struct{}{}
		}
	}
	return len(seen)
}

// Repository reads a project's reach facts, for supporting projects that are
// not attached to a PR.
func (c *Client) Repository(ctx context.Context, owner, repo string) (*domain.RepoFacts, error) {
	var payload repoPayload
	if err := c.get(ctx, fmt.Sprintf("/repos/%s/%s", owner, repo), &payload); err != nil {
		return nil, err
	}
	facts := payload.facts()
	return &facts, nil
}

// reviewsPerPage is GitHub's maximum. Fewer pages means fewer requests against
// a rate limit one claim submission can already exhaust.
const reviewsPerPage = 100

// maxReviewPages bounds pagination.
//
// A PR with more than 1000 reviews is pathological, and an unbounded loop over
// a paginated endpoint is how one malformed response becomes an infinite
// request storm against a third party.
const maxReviewPages = 10

// Reviews returns every review on a PR, following pagination.
//
// Backs the pr-review scoring mode, where the claimant must have reviewed the
// PR and must not have written it — so a truncated list could silently turn a
// valid claim into "carries no review by you".
func (c *Client) Reviews(ctx context.Context, owner, repo string, number int) ([]port.Review, error) {
	var out []port.Review

	for page := 1; page <= maxReviewPages; page++ {
		var payload []reviewPayload

		path := fmt.Sprintf("/repos/%s/%s/pulls/%d/reviews?per_page=%d&page=%d",
			owner, repo, number, reviewsPerPage, page)
		if err := c.get(ctx, path, &payload); err != nil {
			return nil, err
		}

		for _, r := range payload {
			review := port.Review{
				AuthorUserID: r.User.ID,
				State:        r.State,
				Body:         r.Body,
				// CommentCount stays zero: a review's inline comments live on
				// a separate endpoint, and PRFacts.ReviewComments already
				// carries the PR-wide total that Engagement actually reads.
			}
			if r.SubmittedAt != nil {
				review.SubmittedAt = *r.SubmittedAt
			}
			out = append(out, review)
		}

		if len(payload) < reviewsPerPage {
			return out, nil
		}
	}
	return out, nil
}
