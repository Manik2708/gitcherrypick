package httpx_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Manik2708/gitcherrypick/backend/internal/adapter/httpx"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// The three adapters cover the happy paths through this package. What is left
// here is the plumbing they do not reach: construction failures, bodies that
// cannot be encoded, and the sentinel mapping in isolation.

func TestMapStatus(t *testing.T) {
	cases := map[int]error{
		200: nil,
		204: nil,
		404: port.ErrNotFound,
		429: port.ErrUnavailable,
		500: port.ErrUnavailable,
		503: port.ErrUnavailable,
	}

	for status, want := range cases {
		resp := &http.Response{StatusCode: status, Header: http.Header{}}
		err := httpx.MapStatus(resp, "the thing")

		if want == nil {
			require.NoError(t, err, "status %d", status)
			continue
		}
		require.ErrorIs(t, err, want, "status %d", status)
	}
}

func TestMapStatusLeavesClientErrorsUnclassified(t *testing.T) {
	// A 400 or a 422 is neither "missing" nor "try again" — it means the
	// request was wrong, and mapping it onto either sentinel would send a
	// caller down the wrong path.
	for _, status := range []int{400, 401, 422} {
		resp := &http.Response{StatusCode: status, Header: http.Header{}}
		err := httpx.MapStatus(resp, "the thing")

		require.Error(t, err)
		require.NotErrorIs(t, err, port.ErrNotFound)
		require.NotErrorIs(t, err, port.ErrUnavailable)
	}
}

func TestTrimBase(t *testing.T) {
	require.Equal(t, "https://api.example.com", httpx.TrimBase("https://api.example.com/", "https://fallback"))
	require.Equal(t, "https://api.example.com", httpx.TrimBase("  https://api.example.com  ", "https://fallback"))
	require.Equal(t, "https://fallback", httpx.TrimBase("", "https://fallback/"))
	require.Equal(t, "https://fallback", httpx.TrimBase("   ", "https://fallback"))
}

func TestDefaultsApply(t *testing.T) {
	// A nil client must not mean http.DefaultClient, which has no timeout.
	c := httpx.New(nil, nil)
	require.NotNil(t, c)
}

func TestBadURLIsReported(t *testing.T) {
	c := httpx.New(nil, nil)
	err := c.GetJSON(context.Background(), "://not-a-url", "", "the thing", nil, nil)
	require.ErrorContains(t, err, "building the request")
}

func TestUnencodableBodyIsReported(t *testing.T) {
	c := httpx.New(nil, nil)
	// A channel cannot be JSON. This is a programming error rather than a
	// transport one, so it must not surface as ErrUnavailable and get retried.
	err := c.PostJSON(context.Background(), "https://example.com", "", "the thing", make(chan int), nil, nil)
	require.ErrorContains(t, err, "encoding")
	require.NotErrorIs(t, err, port.ErrUnavailable)
}

func TestNilOutSkipsDecoding(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json at all"))
	}))
	t.Cleanup(srv.Close)

	c := httpx.New(srv.Client(), nil)
	require.NoError(t, c.GetJSON(context.Background(), srv.URL, "", "the thing", nil, nil))
}

func TestHeadersAndBearer(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	c := httpx.New(srv.Client(), nil)
	err := c.GetJSON(context.Background(), srv.URL, "the-token", "the thing", nil,
		http.Header{"Accept": []string{"application/vnd.custom+json"}})
	require.NoError(t, err)

	require.Equal(t, "Bearer the-token", got.Get("Authorization"))
	require.Equal(t, "application/vnd.custom+json", got.Get("Accept"),
		"an explicit Accept must override the JSON default")
}

func TestPostFormSendsFormEncoding(t *testing.T) {
	var contentType, accept, body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType = r.Header.Get("Content-Type")
		accept = r.Header.Get("Accept")
		require.NoError(t, r.ParseForm())
		body = r.Form.Get("code")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	c := httpx.New(srv.Client(), nil)
	err := c.PostForm(context.Background(), srv.URL, "the exchange",
		url.Values{"code": {"abc"}}, nil)
	require.NoError(t, err)

	require.Equal(t, "application/x-www-form-urlencoded", contentType)
	require.Equal(t, "application/json", accept, "OAuth endpoints answer in form encoding without this")
	require.Equal(t, "abc", body)
}

func TestCustomMapperTakesPrecedence(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	sentinel := "mapped by the adapter"
	c := httpx.New(srv.Client(), func(_ *http.Response, _ string) error {
		return errUnderTest{sentinel}
	})

	err := c.GetJSON(context.Background(), srv.URL, "", "the thing", nil, nil)
	require.EqualError(t, err, sentinel)
	require.NotErrorIs(t, err, port.ErrNotFound)
}

type errUnderTest struct{ msg string }

func (e errUnderTest) Error() string { return e.msg }
