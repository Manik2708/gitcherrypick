package resend_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Manik2708/gitcherrypick/backend/internal/adapter/resend"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// stubRenderer produces a fixed subject and body.
type stubRenderer struct {
	err error
}

func (r stubRenderer) Render(n port.Notification) (string, string, error) {
	if r.err != nil {
		return "", "", r.err
	}
	return "Subject for " + string(n.Kind), "<p>body</p>", nil
}

// captured is one request the stub Resend received.
type captured struct {
	Auth string
	Body map[string]any
}

func newNotifier(t *testing.T, h http.HandlerFunc) *resend.Notifier {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	return resend.New(resend.Config{
		BaseURL:    srv.URL,
		APIKey:     "re_test_key",
		From:       "GitCherryPick <no-reply@example.com>",
		HTTPClient: srv.Client(),
		Renderer:   stubRenderer{},
	})
}

func TestSend(t *testing.T) {
	var got captured
	n := newNotifier(t, func(w http.ResponseWriter, r *http.Request) {
		got.Auth = r.Header.Get("Authorization")
		require.Equal(t, "/emails", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got.Body))

		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"id": "msg-1"}))
	})

	err := n.Send(context.Background(), port.Notification{
		Kind:      port.NotifyContactRequest,
		Recipient: "alice@example.com",
	})
	require.NoError(t, err)

	require.Equal(t, "Bearer re_test_key", got.Auth)
	require.Equal(t, "GitCherryPick <no-reply@example.com>", got.Body["from"])
	require.Equal(t, []any{"alice@example.com"}, got.Body["to"])
	require.Equal(t, "Subject for contact_request", got.Body["subject"])
	require.Equal(t, "<p>body</p>", got.Body["html"])
}

func TestSendRefusals(t *testing.T) {
	ok := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg-1"}`))
	}

	t.Run("refuses without an API key", func(t *testing.T) {
		// Failing loudly means a misconfigured deployment is found on the
		// first notification, not by a contributor who never heard from us.
		n := resend.New(resend.Config{Renderer: stubRenderer{}})
		err := n.Send(context.Background(), port.Notification{
			Kind: port.NotifyContactRequest, Recipient: "a@b.c",
		})
		require.ErrorContains(t, err, "--resend-api-key")
	})

	t.Run("refuses with no recipient", func(t *testing.T) {
		n := newNotifier(t, ok)
		err := n.Send(context.Background(), port.Notification{Kind: port.NotifyContactRequest})
		require.ErrorContains(t, err, "no recipient")
	})

	t.Run("refuses with no renderer", func(t *testing.T) {
		n := resend.New(resend.Config{APIKey: "k"})
		err := n.Send(context.Background(), port.Notification{
			Kind: port.NotifyContactRequest, Recipient: "a@b.c",
		})
		require.ErrorContains(t, err, "renderer")
	})

	t.Run("reports a rendering failure", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(ok))
		t.Cleanup(srv.Close)
		n := resend.New(resend.Config{
			BaseURL: srv.URL, APIKey: "k", HTTPClient: srv.Client(),
			Renderer: stubRenderer{err: errors.New("template missing")},
		})

		err := n.Send(context.Background(), port.Notification{
			Kind: port.NotifyContactRequest, Recipient: "a@b.c",
		})
		require.ErrorContains(t, err, "template missing")
	})
}

func TestSendStatusMapping(t *testing.T) {
	t.Run("a 422 is permanent, not transient", func(t *testing.T) {
		// A malformed address or an unverified sending domain will never
		// succeed on retry, so it must not become ErrUnavailable.
		n := newNotifier(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"name":"validation_error","message":"Invalid to field"}`))
		})

		err := n.Send(context.Background(), port.Notification{
			Kind: port.NotifyContactRequest, Recipient: "bad",
		})
		require.ErrorContains(t, err, "validation_error")
		require.ErrorContains(t, err, "Invalid to field")
		require.NotErrorIs(t, err, port.ErrUnavailable)
	})

	t.Run("a 422 with no body still names the rejection", func(t *testing.T) {
		n := newNotifier(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnprocessableEntity)
		})

		err := n.Send(context.Background(), port.Notification{
			Kind: port.NotifyContactRequest, Recipient: "bad",
		})
		require.ErrorContains(t, err, "no reason given")
		require.NotErrorIs(t, err, port.ErrUnavailable)
	})

	t.Run("a 429 and a 5xx are transient", func(t *testing.T) {
		for _, status := range []int{http.StatusTooManyRequests, http.StatusBadGateway} {
			n := newNotifier(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
			})
			err := n.Send(context.Background(), port.Notification{
				Kind: port.NotifyContactRequest, Recipient: "a@b.c",
			})
			require.ErrorIs(t, err, port.ErrUnavailable, "status %d", status)
		}
	})

	t.Run("a 2xx with no id is a failure", func(t *testing.T) {
		// Resend accepted nothing. Treating it as success would lose the
		// notification silently.
		n := newNotifier(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
		})

		err := n.Send(context.Background(), port.Notification{
			Kind: port.NotifyContactRequest, Recipient: "a@b.c",
		})
		require.ErrorContains(t, err, "no message id")
	})
}

func TestDefaultBaseURL(t *testing.T) {
	require.Equal(t, "https://api.resend.com", resend.DefaultBaseURL)
}
