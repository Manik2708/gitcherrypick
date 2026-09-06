package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"testing"
)

// The five harness pseudo-methods (ADR-0012).
//
// None of them is an endpoint, and none adds a route to cmd/api. Time moves
// through the configured clock source; background work runs as a separate
// binary against the same database; and the one that looks like it needs a
// shortcut — REJECT_N_REEVALUATIONS — makes the real admin calls instead,
// because a shortcut there would test the shortcut.

// jobsBinaryEnv names the cmd/jobs binary the harness invokes. e2e.sh builds it
// once and exports this rather than paying `go run`'s compile per step.
const jobsBinaryEnv = "E2E_JOBS_BINARY"

// evaluatorBinaryEnv names the cmd/evaluator binary. Built once by e2e.sh,
// like the others.
const evaluatorBinaryEnv = "E2E_EVALUATOR_BINARY"

// databaseURLEnvForJobs is the connection string cmd/jobs is pointed at.
const databaseURLEnvForJobs = "E2E_DATABASE_URL"

// rejectNRequest is the body of a REJECT_N_REEVALUATIONS step.
type rejectNRequest struct {
	User  string `json:"user"`
	Count int    `json:"count"`
}

// advanceRequest is the body of an ADVANCE_CLOCK step, when it carries one.
type advanceRequest struct {
	Duration string `json:"duration"`
}

// runPseudo dispatches a harness-driven step.
func runPseudo(
	ctx context.Context,
	t *testing.T,
	control *Control,
	sessions *sessionCache,
	client *http.Client,
	bindings map[string]string,
	step Step,
	schema string,
) error {
	t.Helper()

	switch step.Request.Method {
	case MethodAdvanceClock:
		if err := advanceClock(ctx, control, step); err != nil {
			return err
		}
		// Access tokens live 15 minutes (ADR-0002). Every advance a fixture
		// makes is measured in days, so the cached bearer tokens are now
		// expired — correctly. Dropping them makes the next step sign in
		// again, which is what a real client would do.
		sessions.Reset()
		return nil

	case MethodRunOverdueSweep:
		return runJob(ctx, "overdue-sweep", schema)

	case MethodRunEvaluator:
		// Stage 5 (CLAUDE.md). Fixtures that judge evidence stay red until
		// cmd/evaluator exists, which is the pipeline working rather than a
		if err := control.ApplyFake(ctx, step.Fake); err != nil {
			return err
		}
		return runEvaluator(ctx, t, schema)

	case MethodRedeliverLastJob:
		// Delivery is at-least-once: a worker that died after processing but
		// before acking sees the job again. Re-running the evaluator against a
		// queue the previous pass emptied is that second delivery — and the
		// fixture asserts it changes nothing, which is ADR-0004's idempotency
		// requirement stated as an observation rather than a claim.
		if err := control.ApplyFake(ctx, step.Fake); err != nil {
			return err
		}
		return runEvaluator(ctx, t, schema)

	case MethodRejectNReevals:
		return rejectReevaluations(ctx, t, sessions, client, bindings, step)
	}

	return fmt.Errorf("unknown pseudo-method %q", step.Request.Method)
}

// advanceClock moves the API's clock forward.
//
// The duration may come from the step's `fake.clock.advance` or from its body.
// Both spellings appear in the approved fixtures, and neither is worth
// rewriting a fixture to normalise.
func advanceClock(ctx context.Context, control *Control, step Step) error {
	if step.Fake != nil && step.Fake.Clock != nil {
		return control.ApplyFake(ctx, step.Fake)
	}

	var body advanceRequest
	if len(step.Request.Body) > 0 {
		if err := json.Unmarshal(step.Request.Body, &body); err != nil {
			return fmt.Errorf("decoding the advance: %w", err)
		}
	}
	if body.Duration == "" {
		return fmt.Errorf(
			"ADVANCE_CLOCK needs a duration, either in fake.clock.advance or in the body")
	}
	return control.AdvanceClock(ctx, body.Duration)
}

// runJob executes one scheduled job to completion.
//
// A separate PROCESS against the same database, so no control listener has to
// exist inside cmd/api (ADR-0012). It is also exactly how production runs it.
// runEvaluator drains the queue once, exactly as a deployment's worker does.
//
// A separate PROCESS rather than an endpoint on cmd/api: the evaluator is its
// own binary (CLAUDE.md), and a control route that ran it inside the API would
// put test-only code in the shipping surface.
func runEvaluator(ctx context.Context, t *testing.T, schema string) error {
	t.Helper()

	binary := os.Getenv(evaluatorBinaryEnv)
	if binary == "" {
		return fmt.Errorf(
			"%s is not set: run the suite through backend/scripts/e2e.sh, which builds "+
				"cmd/evaluator and points the harness at it", evaluatorBinaryEnv)
	}

	databaseURL, err := schemaScopedDSN(os.Getenv(databaseURLEnvForJobs), schema)
	if err != nil {
		return err
	}

	control := ControlURL()
	out, err := exec.CommandContext(ctx, binary,
		"--database-url="+databaseURL,
		"--anthropic-api-url="+control+"/anthropic",
		"--anthropic-api-key=e2e",
		"--github-api-url="+control+"/github",
		"--github-token=e2e",
		"--clock-url="+control+"/_clock",
		"--drain",
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("running the evaluator: %w: %s", err, out)
	}
	t.Logf("evaluator: %s", out)
	return nil
}

func runJob(ctx context.Context, name, schema string) error {
	binary := os.Getenv(jobsBinaryEnv)
	if binary == "" {
		return fmt.Errorf(
			"%s is not set: run the suite through backend/scripts/e2e.sh, which builds "+
				"cmd/jobs and points the harness at it", jobsBinaryEnv)
	}

	// Scoped to the fixture's schema, exactly as the API is. A job reading
	// the default search_path would run against whatever tables happen to be
	// there — which is none of them, since every case builds its own.
	databaseURL, err := schemaScopedDSN(os.Getenv(databaseURLEnvForJobs), schema)
	if err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, binary, "run", name,
		"--database-url="+databaseURL,
		"--resend-api-url="+ControlURL()+"/resend",
		"--resend-api-key=e2e",
		"--clock-url="+ControlURL()+"/_clock")

	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("running job %s: %w\n%s", name, err, out)
	}
	return nil
}

// rejectReevaluations makes N real admin rejections.
//
// No shortcut: the escalating cooldown is what the fixture is about, and
// reaching around the endpoint that advances it would leave the mechanism
// untested (ADR-0012).
func rejectReevaluations(
	ctx context.Context,
	t *testing.T,
	sessions *sessionCache,
	client *http.Client,
	bindings map[string]string,
	step Step,
) error {
	t.Helper()

	var body rejectNRequest
	if err := json.Unmarshal(step.Request.Body, &body); err != nil {
		return fmt.Errorf("decoding the rejection count: %w", err)
	}
	if body.Count <= 0 {
		return fmt.Errorf("REJECT_N_REEVALUATIONS needs a positive count, got %d", body.Count)
	}

	admin, err := sessions.For(ctx, client, adminPrincipalFor(step))
	if err != nil {
		return fmt.Errorf("signing in as an admin: %w", err)
	}

	for i := range body.Count {
		// Each round raises its own request and refuses it. Reusing one id
		// would advance the cooldown once and assert nothing about escalation.
		id, err := openReevaluation(ctx, client, sessions, bindings, body.User)
		if err != nil {
			return fmt.Errorf("rejection %d of %d: %w", i+1, body.Count, err)
		}

		decision := map[string]string{
			"decision": "rejected",
			"reason":   fmt.Sprintf("harness rejection %d of %d", i+1, body.Count),
		}
		if err := call(ctx, client, http.MethodPost,
			"/admin/reevaluations/"+id+"/decide", decision, admin.AccessToken, nil); err != nil {
			return fmt.Errorf("rejection %d of %d: %w", i+1, body.Count, err)
		}
	}
	return nil
}

// adminPrincipalFor names the admin to act as.
//
// `as` when the step gives one, so a fixture with more than one admin can say
// which; otherwise the seeded root.
// `as: system` marks a step the HARNESS drives rather than a principal — it
// names no seeded account, so a pseudo-step that still needs admin
// credentials falls back to the seeded administrator.
func adminPrincipalFor(step Step) string {
	if step.As != "" && step.As != "system" {
		return step.As
	}
	return "root"
}

// openReevaluation raises a dispute as the named contributor and returns its id.
func openReevaluation(
	ctx context.Context,
	client *http.Client,
	sessions *sessionCache,
	bindings map[string]string,
	user string,
) (string, error) {
	session, err := sessions.For(ctx, client, user)
	if err != nil {
		return "", fmt.Errorf("signing in as %s: %w", user, err)
	}

	claimID, ok := bindings[user+"_go_claim.id"]
	if !ok {
		claimID, ok = bindings["claim_id"]
	}
	if !ok {
		return "", fmt.Errorf(
			"no claim is bound for %s: REJECT_N_REEVALUATIONS needs one to dispute", user)
	}

	var created struct {
		ID string `json:"id"`
	}
	request := map[string]string{"reason": "harness-raised dispute for cooldown escalation"}
	if err := call(ctx, client, http.MethodPost,
		"/claims/"+claimID+"/reevaluation", request, session.AccessToken, &created); err != nil {
		return "", err
	}
	if created.ID == "" {
		return "", fmt.Errorf("the reevaluation request returned no id")
	}
	return created.ID, nil
}
