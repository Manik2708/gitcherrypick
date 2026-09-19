// Command api serves the GitCherryPick HTTP surface.
//
// One wiring path, and no test-only branch anywhere in it (ADR-0010). The
// integration suite runs THIS binary, pointed at cmd/fakethirdparty through the
// same base-URL flags a deployment uses to point at the real hosts — so the
// binary under test is the binary that ships.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	"github.com/Manik2708/gitcherrypick/backend/internal/adapter/clock"
	"github.com/Manik2708/gitcherrypick/backend/internal/adapter/crypto"
	"github.com/Manik2708/gitcherrypick/backend/internal/adapter/github"
	"github.com/Manik2708/gitcherrypick/backend/internal/adapter/google"
	"github.com/Manik2708/gitcherrypick/backend/internal/adapter/places"
	"github.com/Manik2708/gitcherrypick/backend/internal/adapter/resend"
	"github.com/Manik2708/gitcherrypick/backend/internal/controller"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
	"github.com/Manik2708/gitcherrypick/backend/internal/repository/postgres"
	"github.com/Manik2708/gitcherrypick/backend/internal/service"
)

func main() {
	if err := command().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func command() *cobra.Command {
	var cfg config

	cmd := &cobra.Command{
		Use:   "api",
		Short: "Serve the GitCherryPick API",

		// main reports the error and sets the exit code; without these cobra
		// prints it too and every failure appears twice in a log.
		SilenceUsage:  true,
		SilenceErrors: true,

		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd.Context(), &cfg)
		},
	}

	cfg.bind(cmd)
	return cmd
}

func run(ctx context.Context, cfg *config) error {
	if err := cfg.validate(); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	app, err := build(ctx, cfg)
	if err != nil {
		return err
	}
	defer app.close()

	served := make(chan error, 1)
	go func() {
		fmt.Printf("api listening on %s\n", cfg.addr)
		served <- app.server.ListenAndServe(cfg.addr)
	}()

	select {
	case err := <-served:
		return err
	case <-ctx.Done():
	}

	// Drain rather than drop: a request in flight when the signal arrives has
	// already been accepted, and may be halfway through a transaction.
	if err := app.server.Shutdown(); err != nil {
		return err
	}
	return nil
}

// application is everything the process owns, and the order it must be torn
// down in.
type application struct {
	server *controller.Server
	pool   *pgxpool.Pool

	// closeClock stops the offset poller. A no-op when the system clock is in
	// use, which is every production deployment.
	closeClock func()
}

func (a *application) close() {
	a.closeClock()
	a.pool.Close()
}

// build assembles the dependency graph: adapters, repositories, services,
// controllers.
//
// Read top to bottom, it is the layering (CLAUDE.md): adapters know about
// vendors, repositories know about SQL, services know neither, and controllers
// know only services.
func build(ctx context.Context, cfg *config) (*application, error) {
	// --- adapters we own ------------------------------------------------
	verifyKeys, err := cfg.verifyKeyPaths()
	if err != nil {
		return nil, err
	}
	keys, err := crypto.LoadKeyset(cfg.signingKeyPath, verifyKeys)
	if err != nil {
		return nil, err
	}

	now, closeClock, err := clock.New(ctx, cfg.clockURL, cfg.clockPoll)
	if err != nil {
		return nil, err
	}

	tokens := crypto.NewTokenIssuer(keys, now)
	minter := crypto.NewMinter()
	hasher := crypto.NewArgon2Hasher()

	// --- adapters that talk to somebody else ----------------------------
	githubClient := github.New(github.Config{
		APIBaseURL: cfg.githubAPIURL, OAuthBaseURL: cfg.githubOAuthURL,
		ClientID: cfg.githubClientID, ClientSecret: cfg.githubClientSecret,
		Token: cfg.githubToken,
	})
	googleClient := google.New(google.Config{
		IssuerURL: cfg.googleIssuerURL, ClientID: cfg.googleClientID,
		ClientSecret: cfg.googleSecret, RedirectURI: cfg.googleRedirectURI,
	})
	places := places.New(places.Config{BaseURL: cfg.placesAPIURL})
	notifier := resend.New(resend.Config{
		BaseURL: cfg.resendAPIURL, APIKey: cfg.resendAPIKey, From: cfg.resendFrom,
		Renderer: resend.NewDefaultRenderer(cfg.appURL),
	})

	// --- storage ---------------------------------------------------------
	poolConfig, err := postgres.PoolConfig(cfg.databaseURL)
	if err != nil {
		closeClock()
		return nil, err
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		closeClock()
		return nil, fmt.Errorf("connecting to the database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		closeClock()
		pool.Close()
		return nil, fmt.Errorf("reaching the database: %w", err)
	}
	db := postgres.New(pool).WithClock(now)

	// --- services ---------------------------------------------------------
	access := service.NewAccessService(db.Hirers())

	auth := service.NewAuthService(
		db.Users(), db.Hirers(), db.Admins(), db.Sessions(),
		githubClient, googleClient, tokens, minter, hasher,
		db.ShareLinks(), db.Search(), db, now, cfg.rubricVersion,
	)
	orgs := service.NewOrganizationService(
		db.Organizations(), db.Hirers(), db.Sessions(), notifier,
		minter, tokens, hasher, db, now,
	)
	onboarding := service.NewOnboardingService(
		db.Onboarding(), db.Organizations(), db.EmailVerifications(),
		notifier, minter, hasher, db, now,
	)
	redemption := service.NewRedemptionService(
		db.Organizations(), db.Hirers(), db.EmailVerifications(), db.Sessions(),
		notifier, minter, tokens, hasher, db, now,
	)
	claims := service.NewClaimService(
		db.Claims(), db.Skills(), db.Evaluations(), db.Users(), githubClient, db.Queue(), db, now,
	)
	skills := service.NewSkillService(db.Skills(), db.Users(), db.Reevaluations(),
		db.Claims(), db.Evaluations(), now, cfg.rubricVersion)
	discovery := service.NewDiscoveryService(db.Search(), db.Skills(), db.Roles(), db.SavedSearches(), access, cfg.rubricVersion)
	shortlists := service.NewShortlistService(
		db.Shortlists(), db.Roles(), db.Contacts(), db.Users(), db.Hirers(),
		notifier, access, db, now,
	)
	contacts := service.NewContactService(
		db.Contacts(), db.Roles(), db.Users(), db.Hirers(), notifier, db, now,
	)
	admin := service.NewAdminService(
		db.Admins(), db.Skills(), db.Organizations(), db.Reevaluations(),
		db.Claims(), db.Queue(), db,
	)
	profiles := service.NewProfileService(db.Profiles(), db.Users(), places, githubClient, db, now)
	roles := service.NewRoleService(
		db.Roles(), db.Openings(), db.Skills(), db.OrgSettings(), db.Contacts(), db.Profiles(), db.Users(),
		db.Organizations(), db, now,
	)
	reeval := service.NewReevaluationService(db.Reevaluations(), db.Claims(), now)
	evaluation := service.NewEvaluationService(
		db.Claims(), db.Skills(), db.Users(), db.Evaluations(), db.Norms(),
		refusingAI{}, db.Queue(), githubClient, db, now, cfg.rubricVersion,
	)

	// --- HTTP ------------------------------------------------------------
	server := controller.NewServer(controller.NewPrincipalResolver(tokens, auth))
	server.Health()
	server.Mount(
		controller.NewAuthController(auth, redemption, cfg.secureCookies),
		controller.NewMeController(auth, orgs, skills, discovery, contacts, reeval, profiles, roles),
		controller.NewClaimController(claims, reeval),
		controller.NewSkillController(skills),
		controller.NewSkillRequestController(skills),
		controller.NewShortlistController(shortlists),
		controller.NewOrganizationController(orgs),
		controller.NewRoleController(roles),
		controller.NewOpeningsController(roles),
		controller.NewAdminController(admin, evaluation, onboarding, now),
		controller.NewPublicController(auth),
		controller.NewOrganizationsController(redemption, onboarding),
		controller.NewPlacesController(places),

		// Mounted last: it claims "/" and chi resolves the more specific
		// prefixes above it first.
		controller.NewDiscoveryController(discovery),
	)

	return &application{server: server, pool: pool, closeClock: closeClock}, nil
}

// refusingAI is the model client this binary does not have.
//
// The API never judges anything — that is cmd/evaluator's job (CLAUDE.md stage
// 5). EvaluationService is wired here only for the admin sweep, which is
// ENQUEUE-ONLY and calls no model (ADR-0004).
//
// It is not a fake: a fake would answer. This refuses, so an accidental call
// fails loudly here rather than returning an invented judgement that would be
// written to a contributor's scorecard.
type refusingAI struct{}

var _ port.AIClient = refusingAI{}

func (refusingAI) Judge(context.Context, port.JudgeRequest) (*port.JudgeResponse, error) {
	return nil, errors.New(
		"the api does not judge claims: evaluation runs in cmd/evaluator, and this " +
			"call means something asked the API to score evidence")
}
