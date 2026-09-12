// Command fakethirdparty stands in for GitHub, Google and Resend in the
// integration suite.
//
// It is a SEPARATE BINARY, and that is a guarantee rather than a preference:
// Go forbids importing a main package, so cmd/api cannot link this code even by
// accident. There is no build tag to forget and no flag to misconfigure — the
// two processes speak HTTP, exactly as the API and github.com would (ADR-0010).
//
// It holds no data of its own. Every byte it returns comes from the fixture's
// third_party block, installed by POST /_load before each fixture. A route with
// no matching entry answers with the provider's real 404, so a fixture that
// forgot to declare a PR fails as a missing PR instead of passing on a default.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

// shutdownGrace bounds how long in-flight requests have to finish.
const shutdownGrace = 5 * time.Second

func main() {
	if err := command().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func command() *cobra.Command {
	var (
		addr          string
		selfURL       string
		pinned        string
		oauthCallback string
		hirerCallback string
	)

	cmd := &cobra.Command{
		Use:   "fakethirdparty",
		Short: "Fixture-driven stand-in for GitHub, Google and Resend",
		Long: "Serves provider-shaped responses from a fixture's third_party block.\n" +
			"Test infrastructure only — never deployed, and never linked into cmd/api.",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if selfURL == "" {
				selfURL = "http://" + addr
			}
			return run(cmd.Context(), addr, selfURL, pinned, oauthCallback, hirerCallback)
		},
	}

	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:8081", "address to listen on")
	cmd.Flags().StringVar(&pinned, "now", "",
		"RFC3339 instant to start the clock at; defaults to the host's clock. "+
			"Pinning it is what makes a suite whose fixtures carry absolute dates "+
			"produce the same result on every calendar day")
	cmd.Flags().StringVar(&oauthCallback, "oauth-callback-url", "",
		"where /login/oauth/authorize sends a browser back to, e.g. "+
			"http://localhost:8080/auth/github/callback. Empty disables the page: "+
			"the fixture suite calls the callback directly and never visits it")
	cmd.Flags().StringVar(&hirerCallback, "hirer-oauth-callback-url", "",
		"where the sign-in page sends a browser for a HIRER session. One identity "+
			"may own both account types (ADR-0009), so the page offers both when set")
	cmd.Flags().StringVar(&selfURL, "self-url", "",
		"absolute base URL clients reach this server at; defaults to http://<addr>. "+
			"OIDC discovery must advertise absolute endpoints, so the server has to know its own address")

	return cmd
}

func run(ctx context.Context, addr, selfURL, pinned, oauthCallback, hirerCallback string) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	store := NewStore()
	if pinned != "" {
		at, err := time.Parse(time.RFC3339, pinned)
		if err != nil {
			return fmt.Errorf("parsing --now %q: %w", pinned, err)
		}
		// A NEGATIVE offset, which /_clock/advance would refuse. The rule it
		// enforces is that time cannot run backwards DURING a run; where the
		// run starts is configuration, and pinning it is the only way a
		// fixture asserting 2026-08-17 means the same thing every day.
		store.PinTo(at)
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           NewServer(store, selfURL).WithOAuthCallback(oauthCallback, hirerCallback).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errs := make(chan error, 1)
	go func() {
		fmt.Printf("fakethirdparty listening on %s (advertising %s)\n", addr, selfURL)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- err
			return
		}
		errs <- nil
	}()

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
	}

	// Drain rather than drop: a fixture's last assertion may still be in
	// flight when the harness tears the server down.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutting down: %w", err)
	}
	return nil
}
