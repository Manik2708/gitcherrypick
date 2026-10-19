package domain_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
)

type parsePRURLCase struct {
	name    string
	in      string
	owner   string
	repo    string
	number  int
	wantErr bool
}

func TestParsePRURL(t *testing.T) {
	const pr = "https://github.com/golang/go/pull/123"
	for _, c := range []parsePRURLCase{
		{name: "plain link", in: pr, owner: "golang", repo: "go", number: 123},
		{name: "trailing slash", in: pr + "/", owner: "golang", repo: "go", number: 123},
		{name: "query string", in: pr + "?diff=split", owner: "golang", repo: "go", number: 123},
		{name: "fragment", in: pr + "#discussion_r1", owner: "golang", repo: "go", number: 123},
		{name: "surrounding whitespace", in: "  " + pr + "\t\n", owner: "golang", repo: "go", number: 123},
		{name: "http scheme", in: "http://github.com/golang/go/pull/123", owner: "golang", repo: "go", number: 123},
		{name: "punctuation in names", in: "https://github.com/my-org/my.repo_1/pull/42", owner: "my-org", repo: "my.repo_1", number: 42},

		// These pass today but probably shouldn't: the host isn't checked,
		// and PR #0 can't exist on GitHub. Raised on the PR, not fixed here.
		{name: "any host", in: "https://gitlab.com/golang/go/pull/123", owner: "golang", repo: "go", number: 123},
		{name: "pr zero", in: "https://github.com/golang/go/pull/0", owner: "golang", repo: "go", number: 0},

		{name: "issue not pr", in: "https://github.com/golang/go/issues/123", wantErr: true},
		{name: "non-numeric number", in: "https://github.com/golang/go/pull/abc", wantErr: true},
		{name: "repository only", in: "https://github.com/golang/go", wantErr: true},
		{name: "empty", in: "", wantErr: true},
		{name: "no scheme", in: "github.com/golang/go/pull/123", wantErr: true},

		// You get these links by clicking the Files or Commits tab on a PR.
		// They're rejected for now; issue #8 leaves that call to the maintainer.
		{name: "files tab", in: pr + "/files", wantErr: true},
		{name: "commits tab", in: pr + "/commits", wantErr: true},

		{name: "unparseable url", in: "https://github.com/%zz", wantErr: true},
		{name: "number overflows int", in: "https://github.com/golang/go/pull/99999999999999999999", wantErr: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			owner, repo, number, err := domain.ParsePRURL(c.in)
			if c.wantErr {
				require.Error(t, err)
				require.Empty(t, owner)
				require.Empty(t, repo)
				require.Zero(t, number)
				return
			}
			require.NoError(t, err)
			require.Equal(t, c.owner, owner)
			require.Equal(t, c.repo, repo)
			require.Equal(t, c.number, number)
		})
	}
}
