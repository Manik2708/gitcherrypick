// Command jobs runs the platform's scheduled work.
//
// Production runs these on a timer; the integration suite runs one on demand,
// which is how a fixture's RUN_OVERDUE_SWEEP step is served without adding a
// control listener to cmd/api (ADR-0012). Nothing here is test-only — the same
// invocation is what a cron entry uses.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	"github.com/Manik2708/gitcherrypick/backend/internal/adapter/clock"
	"github.com/Manik2708/gitcherrypick/backend/internal/adapter/resend"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
	"github.com/Manik2708/gitcherrypick/backend/internal/repository/postgres"
	"github.com/Manik2708/gitcherrypick/backend/internal/service"
)

// config is every flag, in one place. CLAUDE.md: configuration comes from CLI
// flags wired with cobra, never from os.Getenv in business code.
type config struct {
	databaseURL string

	clockURL  string
	clockPoll time.Duration

	resendAPIURL string
	resendAPIKey string
	resendFrom   string
}

// job is one unit of scheduled work.
//
// The count is returned so an operator — and a fixture — can see what the run
// did. A job that reported nothing would be indistinguishable from one that did
// nothing.
type job struct {
	summary string
	run     func(ctx context.Context, svc port.JobService) (int, error)
}

// jobs is the registry. Adding one here is the only thing needed to make it
// runnable and to have it appear in --help.
var jobs = map[string]job{
	"expire-availability": {
		summary: "remind contributors whose discovery window is about to lapse",
		run: func(ctx context.Context, svc port.JobService) (int, error) {
			return svc.ExpireAvailability(ctx)
		},
	},
	"overdue-sweep": {
		summary: "flag hiring rounds past their stated result date",
		run: func(ctx context.Context, svc port.JobService) (int, error) {
			return svc.FlagOverdueShortlists(ctx)
		},
	},
	"expire-contact-requests": {
		summary: "expire contact requests a contributor never answered",
		run: func(ctx context.Context, svc port.JobService) (int, error) {
			return svc.ExpireContactRequests(ctx)
		},
	},
	"recompute-norms": {
		summary: "refresh the global maxima and quantiles every score is measured against",
		run: func(ctx context.Context, svc port.JobService) (int, error) {
			return 0, svc.RecomputeNorms(ctx)
		},
	},
}

func main() {
	if err := command().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func command() *cobra.Command {
	var cfg config

	root := &cobra.Command{
		Use:   "jobs",
		Short: "Run the platform's scheduled work",

		// main reports the error and sets the exit code. Without these cobra
		// prints it too, and an operator reading a cron log sees every failure
		// twice.
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.PersistentFlags().StringVar(&cfg.databaseURL, "database-url", "",
		"PostgreSQL connection string (required)")
	root.PersistentFlags().StringVar(&cfg.clockURL, "clock-url", "",
		"clock source; unset means the system clock (ADR-0012)")
	root.PersistentFlags().DurationVar(&cfg.clockPoll, "clock-poll", clock.DefaultPoll,
		"how often to refresh the clock offset")
	root.PersistentFlags().StringVar(&cfg.resendAPIURL, "resend-api-url", resend.DefaultBaseURL,
		"Resend API base URL")
	root.PersistentFlags().StringVar(&cfg.resendAPIKey, "resend-api-key", "", "Resend API key")
	root.PersistentFlags().StringVar(&cfg.resendFrom, "resend-from",
		"GitCherryPick <no-reply@gitcherrypick.dev>", "envelope sender")

	root.AddCommand(&cobra.Command{
		Use:   "run <job>",
		Short: "Run one job to completion",
		Long:  "Available jobs:\n" + jobList(),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runJob(cmd.Context(), cfg, args[0])
		},
	})

	root.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List the available jobs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprint(cmd.OutOrStdout(), jobList())
			return err
		},
	})

	return root
}

func jobList() string {
	names := make([]string, 0, len(jobs))
	for name := range jobs {
		names = append(names, name)
	}
	sort.Strings(names)

	var out strings.Builder
	for _, name := range names {
		fmt.Fprintf(&out, "  %-24s %s\n", name, jobs[name].summary)
	}
	return out.String()
}

func runJob(ctx context.Context, cfg config, name string) error {
	selected, ok := jobs[name]
	if !ok {
		return fmt.Errorf("unknown job %q. Available:\n%s", name, jobList())
	}
	if cfg.databaseURL == "" {
		return fmt.Errorf("no database configured: pass --database-url")
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.databaseURL)
	if err != nil {
		return fmt.Errorf("connecting to the database: %w", err)
	}
	defer pool.Close()

	now, closeClock, err := clock.New(ctx, cfg.clockURL, cfg.clockPoll)
	if err != nil {
		return err
	}
	defer closeClock()

	db := postgres.New(pool)
	svc := service.NewJobService(
		db.Users(), db.Shortlists(), db.Contacts(), db.Norms(), db.Hirers(),
		resend.New(resend.Config{
			BaseURL:  cfg.resendAPIURL,
			APIKey:   cfg.resendAPIKey,
			From:     cfg.resendFrom,
			Renderer: resend.NewDefaultRenderer(),
		}),
		now,
	)

	affected, err := selected.run(ctx, svc)
	if err != nil {
		return fmt.Errorf("running %s: %w", name, err)
	}

	fmt.Printf("%s: %d affected\n", name, affected)
	return nil
}
