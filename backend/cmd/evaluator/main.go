// Command evaluator drains the evaluation queue.
//
// It receives messages through port.Broker, calls the model through
// port.AIClient, does the arithmetic, and writes through the repository
// interfaces. It names no queue vendor and no model vendor anywhere
// (CLAUDE.md) — both arrive as interfaces, and both are redirected by base URL
// rather than substituted (ADR-0010).
//
// Two topics, drained in order: `claims.live` first and `claims.sweep` only
// when live is empty. A rubric sweep re-judges the whole corpus, and a
// contributor waiting on their own submission must not queue behind it
// (ADR-0004).
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	"github.com/Manik2708/gitcherrypick/backend/internal/adapter/anthropic"
	"github.com/Manik2708/gitcherrypick/backend/internal/adapter/clock"
	"github.com/Manik2708/gitcherrypick/backend/internal/adapter/github"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
	"github.com/Manik2708/gitcherrypick/backend/internal/repository/postgres"
	"github.com/Manik2708/gitcherrypick/backend/internal/service"
)

// config is every flag, in one place (CLAUDE.md).
type config struct {
	databaseURL string

	anthropicAPIURL string
	anthropicAPIKey string
	model           string

	githubAPIURL string
	githubToken  string

	clockURL  string
	clockPoll time.Duration

	rubricVersion string

	// batch is how many messages one drain leases at a time.
	batch int

	// lease is how long a worker holds a message before redelivery. Expiry is
	// what makes a crashed worker recoverable, and it is also why Process must
	// be idempotent.
	lease time.Duration

	// drain stops once both topics are empty, rather than waiting for more.
	// That is what a fixture's RUN_EVALUATOR step needs; a deployment leaves
	// it off and runs continuously.
	drain bool

	// poll is the gap between empty polls when not draining.
	poll time.Duration
}

// runSummary is what one invocation did.
//
// Reported because a run that said nothing would be indistinguishable from one
// that did nothing — for an operator and for a fixture alike.
type runSummary struct {
	Processed int `json:"processed"`
	Failed    int `json:"failed"`
}

func main() {
	if err := command().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func command() *cobra.Command {
	var c config

	cmd := &cobra.Command{
		Use:          "evaluator",
		Short:        "Judge submitted claims and persist their scores",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd.Context(), c)
		},
	}

	f := cmd.Flags()
	f.StringVar(&c.databaseURL, "database-url", "", "postgres connection string")
	f.StringVar(&c.anthropicAPIURL, "anthropic-api-url", "https://api.anthropic.com",
		"base URL for the model provider; redirected in tests, never substituted")
	f.StringVar(&c.anthropicAPIKey, "anthropic-api-key", "", "model provider credential")
	f.StringVar(&c.model, "model", "claude-opus-5", "model that judges a claim")
	f.StringVar(&c.githubAPIURL, "github-api-url", "https://api.github.com", "base URL for GitHub")
	f.StringVar(&c.githubToken, "github-token", "", "GitHub token for enrichment reads")
	f.StringVar(&c.clockURL, "clock-url", "", "clock source; the host's clock when empty (ADR-0012)")
	f.DurationVar(&c.clockPoll, "clock-poll", clock.DefaultPoll, "how often the clock offset refreshes")
	f.StringVar(&c.rubricVersion, "rubric-version", "v1", "rubric version stamped onto every score")
	f.IntVar(&c.batch, "batch", 10, "messages leased per drain")
	f.DurationVar(&c.lease, "lease", postgres.DefaultLease, "how long a worker holds a message")
	f.BoolVar(&c.drain, "drain", false, "stop once the queue is empty instead of waiting")
	f.DurationVar(&c.poll, "poll", time.Second, "gap between polls when the queue is empty")

	return cmd
}

func run(ctx context.Context, cfg config) error {
	if cfg.databaseURL == "" {
		return errors.New("--database-url is required")
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	poolConfig, err := postgres.PoolConfig(cfg.databaseURL)
	if err != nil {
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return fmt.Errorf("connecting to the database: %w", err)
	}
	defer pool.Close()

	now, closeClock, err := clock.New(ctx, cfg.clockURL, cfg.clockPoll)
	if err != nil {
		return err
	}
	defer closeClock()

	db := postgres.New(pool).WithClock(now)

	ai := anthropic.New(anthropic.Config{
		BaseURL: cfg.anthropicAPIURL, APIKey: cfg.anthropicAPIKey, Model: cfg.model,
	})
	gh := github.New(github.Config{APIBaseURL: cfg.githubAPIURL, Token: cfg.githubToken})

	svc := service.NewEvaluationService(
		db.Claims(), db.Skills(), db.Users(), db.Evaluations(), db.Norms(),
		ai, db.Queue(), gh, db, now, cfg.rubricVersion,
	)

	summary, err := drain(ctx, db.Queue(), svc, cfg)
	if err != nil {
		return err
	}

	fmt.Printf("{\"processed\":%d,\"failed\":%d}\n", summary.Processed, summary.Failed)
	return nil
}

// drain works the queue until it is empty, or forever when --drain is off.
func drain(ctx context.Context, broker port.Broker, svc port.EvaluationService, cfg config) (runSummary, error) {
	var summary runSummary

	for {
		messages, err := broker.Consume(ctx, cfg.lease, cfg.batch)
		if err != nil {
			return summary, fmt.Errorf("consuming: %w", err)
		}

		if len(messages) == 0 {
			if cfg.drain {
				return summary, nil
			}
			select {
			case <-ctx.Done():
				return summary, nil
			case <-time.After(cfg.poll):
				continue
			}
		}

		for _, msg := range messages {
			// A failure is NACKed rather than returned. One claim the model
			// choked on must not stop the queue, and the backoff is what stops
			// it spinning (ADR-0004).
			if err := svc.Process(ctx, msg); err != nil {
				// Reported, not swallowed. A queue that silently drops work is
				// indistinguishable from one with nothing to do.
				fmt.Fprintf(os.Stderr, "job %s failed: %v\n", msg.ID, err)
				summary.Failed++
				if nackErr := broker.Nack(ctx, msg.ID, err.Error()); nackErr != nil {
					return summary, fmt.Errorf("nacking %s: %w", msg.ID, nackErr)
				}
				continue
			}
			summary.Processed++
		}
	}
}
