package e2e

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// The `repositories` seed set, projected into GitHub's wire shape.
//
// fixtures/seed/repositories.json is written in the RUBRIC's vocabulary —
// stars, contributors, dependents — because that is what a fixture author
// reasons about when choosing evidence (ADR-0007 §7). GitHub answers in a
// different one: stargazers_count, forks_count, private.
//
// The translation lives here rather than in cmd/fakethirdparty, which holds no
// data of its own and serves what it is given (ADR-0010). It is the same move
// as identities.go: seed data is fixture data, and this is a projection of it.

// seedRepository is one repository as the seed file states it.
type seedRepository struct {
	Public       bool   `json:"public"`
	Stars        int    `json:"stars"`
	Forks        int    `json:"forks"`
	Contributors int    `json:"contributors"`
	Dependents   *int   `json:"dependents"`
	Downloads    *int   `json:"downloads"`
	Archived     bool   `json:"archived"`
	Language     string `json:"primary_language"`
}

// seedPullRequest is one pull request as the seed file states it.
type seedPullRequest struct {
	Merged         bool    `json:"merged"`
	MergedAt       *string `json:"merged_at"`
	AuthorID       int64   `json:"author_github_user_id"`
	Title          string  `json:"title"`
	Additions      int     `json:"additions"`
	Deletions      int     `json:"deletions"`
	ChangedFiles   int     `json:"changed_files"`
	ReviewComments int     `json:"review_comments"`
	Reviews        int     `json:"reviews"`
	Participants   int     `json:"participants"`

	// Reviewers names who reviewed, for a pr-review claim. Absent means the
	// review count is synthesized from anonymous reviewers instead.
	Reviewers []int64 `json:"reviewers"`

	// NotFound makes GitHub answer 404 for this PR, so a fixture can assert
	// what happens when evidence points at something unreachable.
	NotFound bool `json:"not_found"`
}

// repositoriesSeed is the file's shape.
type repositoriesSeed struct {
	Repositories map[string]seedRepository  `json:"repositories"`
	PullRequests map[string]seedPullRequest `json:"pull_requests"`
}

// githubRepo is GitHub's repository payload.
type githubRepo struct {
	Private         bool   `json:"private"`
	Fork            bool   `json:"fork"`
	Archived        bool   `json:"archived"`
	StargazersCount int    `json:"stargazers_count"`
	ForksCount      int    `json:"forks_count"`
	Language        string `json:"language"`
}

// githubBase is a pull request's target branch, which carries the repository.
type githubBase struct {
	Repo githubRepo `json:"repo"`
}

// githubPR is GitHub's pull request payload.
type githubPR struct {
	Title          string     `json:"title"`
	Merged         bool       `json:"merged"`
	MergedAt       *string    `json:"merged_at"`
	Additions      int        `json:"additions"`
	Deletions      int        `json:"deletions"`
	ChangedFiles   int        `json:"changed_files"`
	ReviewComments int        `json:"review_comments"`
	Comments       int        `json:"comments"`
	User           userRef    `json:"user"`
	Base           githubBase `json:"base"`
}

// userRef is GitHub's abbreviated actor.
type userRef struct {
	ID int64 `json:"id"`
}

// githubReview is one review on a pull request.
type githubReview struct {
	User  userRef `json:"user"`
	State string  `json:"state"`
	Body  string  `json:"body"`
}

// LoadRepositories projects the seed file into the fake server's shape.
//
// Returns nil when the fixture does not seed `repositories`, so a case that
// declares no evidence gets a GitHub that knows nothing — and a claim pointing
// at an undeclared PR fails as a missing PR rather than passing on a default.
func LoadRepositories(sets []string) (json.RawMessage, error) {
	if !contains(sets, "repositories") {
		return nil, nil
	}

	var file repositoriesSeed
	if err := readSeed("repositories", &file); err != nil {
		return nil, fmt.Errorf("reading the repositories seed: %w", err)
	}

	repos := map[string]githubRepo{}
	for key, r := range file.Repositories {
		repos[key] = githubRepo{
			// The seed says what is PUBLIC; GitHub reports what is PRIVATE.
			Private:         !r.Public,
			Archived:        r.Archived,
			StargazersCount: r.Stars,
			ForksCount:      r.Forks,
			Language:        r.Language,
		}
	}

	prs := map[string]githubPR{}
	reviews := map[string][]githubReview{}

	for key, pr := range file.PullRequests {
		if pr.NotFound {
			// Left undeclared entirely, so the fake answers with GitHub's real
			// 404 rather than a payload flagged as missing.
			continue
		}

		owner, name, ok := splitPRKey(key)
		if !ok {
			return nil, fmt.Errorf("pull request key %q is not owner/name#number", key)
		}

		prs[key] = githubPR{
			Title: pr.Title, Merged: pr.Merged, MergedAt: pr.MergedAt,
			Additions: pr.Additions, Deletions: pr.Deletions,
			ChangedFiles: pr.ChangedFiles, ReviewComments: pr.ReviewComments,
			User: userRef{ID: pr.AuthorID},
			Base: githubBase{Repo: repos[owner+"/"+name]},
		}
		reviews[key] = reviewsFor(pr)
	}

	return mustMarshal(map[string]any{
		"repositories": repos, "pull_requests": prs, "reviews": reviews,
	}), nil
}

// reviewsFor builds the review list a PR's counts imply.
//
// Named reviewers come first, because a pr-review claim turns on WHO reviewed.
// The rest are anonymous filler up to the stated count: the rubric reads the
// count, and inventing identities for them would put people in the fixture that
// nobody declared.
func reviewsFor(pr seedPullRequest) []githubReview {
	out := make([]githubReview, 0, pr.Reviews)

	for _, id := range pr.Reviewers {
		out = append(out, githubReview{
			User: userRef{ID: id}, State: "APPROVED", Body: "seeded review",
		})
	}

	for i := len(out); i < pr.Reviews; i++ {
		// Ids well clear of the seeded principals', so filler can never be
		// mistaken for a claimant.
		out = append(out, githubReview{
			User:  userRef{ID: int64(900000 + i)},
			State: "COMMENTED", Body: "seeded review",
		})
	}
	return out
}

// splitPRKey parses "owner/name#number".
func splitPRKey(key string) (owner, name string, ok bool) {
	repo, number, found := strings.Cut(key, "#")
	if !found {
		return "", "", false
	}
	if _, err := strconv.Atoi(number); err != nil {
		return "", "", false
	}
	owner, name, found = strings.Cut(repo, "/")
	if !found || owner == "" || name == "" {
		return "", "", false
	}
	return owner, name, true
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
