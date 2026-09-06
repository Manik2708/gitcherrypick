package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Manik2708/gitcherrypick/backend/internal/repository/postgres"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Manik2708/gitcherrypick/backend/internal/adapter/crypto"
)

// Seeding writes rows with direct SQL rather than through the repository layer.
//
// That is deliberate. An e2e suite that seeds through the code under test
// cannot detect a repository bug — the same wrong mapping would be used to
// write the fixture and to read it back, and the test would pass. Seed data is
// test infrastructure, so it goes in by the shortest honest path.

const seedRoot = "fixtures/seed"

// SeededPassword is shared by every seeded account that has one.
//
// The fixtures state it literally, so the stored hash must agree with it. It is
// DERIVED from this constant at seed time rather than written out as a literal:
// a hardcoded hash and a hardcoded plaintext are two facts that can drift, and
// the drift presents as every sign-in fixture failing with "invalid
// credentials" and no clue why.
const SeededPassword = "correct-horse-battery"

// seededHash is computed once, by the REAL hasher the API verifies with.
//
// Using the production hasher here is what makes the fixtures prove anything
// about passwords: a stub hash would only prove that two stubs agree.
var (
	seededHashOnce sync.Once
	seededHash     string
	seededHashErr  error
)

func passwordHashForSeed() (string, error) {
	seededHashOnce.Do(func() {
		hash, err := crypto.NewArgon2Hasher().Hash(SeededPassword)
		if err != nil {
			seededHashErr = fmt.Errorf("hashing the seeded password: %w", err)
			return
		}
		seededHash = string(hash)
	})
	return seededHash, seededHashErr
}

// Seed loads the named seed sets into a schema, in the order given. Sets are
// not commutative: scored_population extends alice_go_primary.
// Seed returns the bindings a fixture may reference as {{key.field}} —
// {{alice.id}}, {{pending_pat.verification_id}}. They come from seed data
// rather than from a response, so nothing in the step sequence can bind them.
func Seed(ctx context.Context, pool *pgxpool.Pool, schema string, sets []string) (map[string]string, error) {
	bindings := map[string]string{}
	if len(sets) == 0 {
		return bindings, nil
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, fmt.Sprintf("SET search_path TO %q, ext", schema)); err != nil {
		return nil, fmt.Errorf("set search_path: %w", err)
	}

	for _, set := range sets {
		if err := seedSet(ctx, conn.Conn(), set, bindings); err != nil {
			return nil, fmt.Errorf("seed set %q: %w", set, err)
		}
	}
	return bindings, nil
}

func seedSet(ctx context.Context, conn *pgx.Conn, set string, bindings map[string]string) error {
	switch set {
	case "principals":
		return seedPrincipals(ctx, conn, bindings)
	case "catalogue":
		return seedCatalogue(ctx, conn, bindings)
	case "repositories":
		// GitHub facts, served by the fake client. Nothing reaches the
		// database, so there is nothing to insert.
		return nil
	case "alice_go_primary", "scored_population":
		return seedScored(ctx, conn, set, bindings)
	}
	return fmt.Errorf("unknown seed set — scripts/validate_fixtures.py should have caught this")
}

func readSeed(name string, into any) error {
	raw, err := os.ReadFile(filepath.Join(seedRoot, name+".json"))
	if err != nil {
		return err
	}
	clean, err := stripComments(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(clean, into)
}

func seedPrincipals(ctx context.Context, conn *pgx.Conn, bindings map[string]string) error {
	var file Principals
	if err := readSeed("principals", &file); err != nil {
		return err
	}

	// The same instant the API's clock is pinned to. Seeding relative dates
	// from the host's clock while the API reads a pinned one would put every
	// availability window in the wrong place.
	now := EpochTime()

	for _, c := range file.Contributors {
		if _, err := conn.Exec(ctx,
			`INSERT INTO users (id, display_name, email) VALUES ($1, $2, $3)`,
			c.ID, c.DisplayName, c.Email); err != nil {
			return fmt.Errorf("contributor %s: %w", c.Key, err)
		}
		if _, err := conn.Exec(ctx,
			`INSERT INTO user_github_identities (id, user_id, github_user_id, github_login)
			 VALUES (gen_random_uuid(), $1, $2, $3)`,
			c.ID, c.GitHubUserID, c.GitHubLogin); err != nil {
			return fmt.Errorf("contributor %s identity: %w", c.Key, err)
		}
		bindings[c.Key+".id"] = c.ID

		if c.Availability == nil {
			continue
		}
		// expires_in_days is relative so a fixture stays valid whenever it runs;
		// negative means already lapsed, which is how carol is set up.
		var expires *time.Time
		if c.Availability.ExpiresInDays != nil {
			t := now.AddDate(0, 0, *c.Availability.ExpiresInDays)
			expires = &t
		}
		if _, err := conn.Exec(ctx,
			`INSERT INTO user_availability (user_id, status, expires_at) VALUES ($1, $2, $3)`,
			c.ID, c.Availability.Status, expires); err != nil {
			return fmt.Errorf("contributor %s availability: %w", c.Key, err)
		}
	}

	orgIDs := map[string]string{}
	for _, o := range file.Organizations {
		orgIDs[o.Key] = o.ID
		bindings[o.Key+".id"] = o.ID
		var verifiedAt, paymentAt *time.Time
		if o.Verified {
			verifiedAt = &now
		}
		if o.PaymentVerified {
			paymentAt = &now
		}
		if _, err := conn.Exec(ctx,
			`INSERT INTO organizations (id, name, slug, linkedin_url, verified_at, payment_verified_at)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			o.ID, o.Name, o.Slug, o.LinkedInURL, verifiedAt, paymentAt); err != nil {
			return fmt.Errorf("organization %s: %w", o.Key, err)
		}
	}

	for _, h := range file.Hirers {
		var verifiedAt *time.Time
		if h.Verified {
			verifiedAt = &now
		}
		orgID := orgIDs[h.Organization]

		// ck_hirer_password_only_for_email: a password hash is valid only for
		// the email provider. An OAuth account with one would mean two ways in,
		// one of which nobody set.
		var passwordHash *string
		if h.AuthProvider == "email" {
			hash, err := passwordHashForSeed()
			if err != nil {
				return err
			}
			passwordHash = &hash
		}
		if _, err := conn.Exec(ctx,
			`INSERT INTO hirer_accounts
			   (id, organization_id, email, auth_provider, display_name, verified_at, password_hash)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			h.ID, orgID, h.Email, h.AuthProvider, h.DisplayName, verifiedAt, passwordHash); err != nil {
			return fmt.Errorf("hirer %s: %w", h.Key, err)
		}

		role := h.OrgRole
		if role == "" {
			role = "member"
		}
		if _, err := conn.Exec(ctx,
			`INSERT INTO organization_members (organization_id, hirer_account_id, role)
			 VALUES ($1, $2, $3)`,
			orgID, h.ID, role); err != nil {
			return fmt.Errorf("hirer %s membership: %w", h.Key, err)
		}

		// dave_hiring shares a GitHub identity with contributor dave. That link
		// is what AssertNotSelf keys on, so it has to exist in the database
		// rather than only in the fixture's prose.
		if h.GitHubUserID != nil {
			if _, err := conn.Exec(ctx,
				`INSERT INTO user_github_identities (id, hirer_account_id, github_user_id, github_login)
				 VALUES (gen_random_uuid(), $1, $2, $3)`,
				h.ID, *h.GitHubUserID, h.Email); err != nil {
				return fmt.Errorf("hirer %s identity: %w", h.Key, err)
			}
		}

		bindings[h.Key+".id"] = h.ID
	}

	// Verification is per ORGANIZATION (ADR-0002, and ADR-0008 §3a depends on
	// it: approving one lifts every seat). ck_verification_single_subject
	// enforces that a request names a hirer or an org, never both — so one row
	// per organization, not one per hirer.
	for _, o := range file.Organizations {
		status := "pending"
		var reviewedAt *time.Time
		if o.Verified {
			status, reviewedAt = "approved", &now
		}
		var requestID string
		if err := conn.QueryRow(ctx,
			`INSERT INTO verification_requests (id, organization_id, status, reviewed_at)
			 VALUES (gen_random_uuid(), $1, $2, $3) RETURNING id`,
			o.ID, status, reviewedAt).Scan(&requestID); err != nil {
			return fmt.Errorf("organization %s verification: %w", o.Key, err)
		}
		// A fixture reaches this through whichever hirer it is acting as, so
		// bind it under every member of the organization too.
		bindings[o.Key+".verification_id"] = requestID
		for _, h := range file.Hirers {
			if h.Organization == o.Key {
				bindings[h.Key+".verification_id"] = requestID
			}
		}
	}

	adminHash, err := passwordHashForSeed()
	if err != nil {
		return err
	}
	for _, a := range file.Admins {
		if _, err := conn.Exec(ctx,
			`INSERT INTO admin_accounts (id, email, password_hash, display_name)
			 VALUES ($1, $2, $3, $4)`,
			a.ID, a.Email, adminHash, a.DisplayName); err != nil {
			return fmt.Errorf("admin %s: %w", a.Key, err)
		}
		bindings[a.Key+".id"] = a.ID
	}
	return nil
}

// seedSkill is one entry in the skill catalogue.
type seedSkill struct {
	Key         string   `json:"key"`
	ID          string   `json:"id"`
	Slug        string   `json:"slug"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Category    string   `json:"category"`
	ScoringMode string   `json:"scoring_mode"`
	Aliases     []string `json:"aliases"`
}

// seedGlobalNorm fixes the maximum one metric is normalised against.
//
// Generation is a version counter ON the row, not part of its key —
// global_norms is `metric PRIMARY KEY` (RFC-0005 schema), so a metric has
// exactly one row and bumping the generation overwrites it rather than adding
// a second.
type seedGlobalNorm struct {
	Metric     string             `json:"metric"`
	Generation int                `json:"generation"`
	MaxValue   float64            `json:"max_value"`
	Quantiles  map[string]float64 `json:"quantiles"`
}

type catalogueFile struct {
	Skills []seedSkill `json:"skills"`

	// An array rather than a map keyed on metric, so the seed file reads in a
	// fixed order and a diff stays legible.
	GlobalNorms []seedGlobalNorm `json:"global_norms"`
}

func seedCatalogue(ctx context.Context, conn *pgx.Conn, bindings map[string]string) error {
	var file catalogueFile
	if err := readSeed("catalogue", &file); err != nil {
		return err
	}

	for _, s := range file.Skills {
		// The seed carries fixed ids so a fixture can reference a skill
		// directly and a failure reads the same on every run.
		if _, err := conn.Exec(ctx,
			`INSERT INTO skills (id, slug, name, description, category, scoring_mode)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			s.ID, s.Slug, s.Name, s.Description, s.Category, s.ScoringMode); err != nil {
			return fmt.Errorf("skill %s: %w", s.Slug, err)
		}
		// A fixture that needs a catalogue skill's id — to prove that
		// accepting a DECLARED skill through the suggestion path is refused,
		// say — has no response to capture it from. The seed is the only place
		// that knows it.
		bindings[s.Key+".skill_id"] = s.ID

		for _, alias := range s.Aliases {
			if _, err := conn.Exec(ctx,
				`INSERT INTO skill_aliases (skill_id, alias) VALUES ($1, $2)`, s.ID, alias); err != nil {
				return fmt.Errorf("skill %s alias %s: %w", s.Slug, alias, err)
			}
		}
	}

	// Fixed maxima, so a score is reproducible. Norms that moved with the
	// seeded population would make every pinned score a moving target.
	for _, norm := range file.GlobalNorms {
		quantiles, err := json.Marshal(norm.Quantiles)
		if err != nil {
			return fmt.Errorf("global norm %s quantiles: %w", norm.Metric, err)
		}
		generation := norm.Generation
		if generation == 0 {
			generation = 1
		}
		if _, err := conn.Exec(ctx,
			`INSERT INTO global_norms (metric, generation, max_value, max_source, quantiles)
			 VALUES ($1, $2, $3, 'e2e_fixture', $4)`,
			norm.Metric, generation, norm.MaxValue, string(quantiles)); err != nil {
			return fmt.Errorf("global norm %s: %w", norm.Metric, err)
		}
	}
	return nil
}

// seedScored writes pre-scored rows directly, bypassing evaluation, so a search
// fixture fails when SEARCH is wrong rather than when scoring is. The scoring
// path has its own cases under controllers/evaluation/.
//
// It writes the whole chain a real evaluation would leave behind — claim,
// evidence, evaluation, per-pair scores, links and standing — because a search
// query joins all of them. Writing only user_skills would make the fixtures
// pass against a query that forgot half its joins.
func seedScored(ctx context.Context, conn *pgx.Conn, set string, bindings map[string]string) error {
	var file scoredFile
	if err := readSeed("scored_population", &file); err != nil {
		return err
	}

	sets := []scoredSet{file.AliceGoPrimary}
	if set == "scored_population" {
		// scored_population extends alice_go_primary; the seed file says so
		// with _extends, and the order is not commutative.
		sets = append(sets, file.ScoredPopulation)
	}

	for _, s := range sets {
		if err := seedScoredSet(ctx, conn, s, bindings); err != nil {
			return err
		}
	}
	return nil
}

type scoredFile struct {
	AliceGoPrimary   scoredSet `json:"alice_go_primary"`
	ScoredPopulation scoredSet `json:"scored_population"`
}

// seedPREvidence is one PR attached to a seeded claim.
type seedPREvidence struct {
	Position int    `json:"position"`
	Repo     string `json:"repo"`
	PRNumber int    `json:"pr_number"`
	Role     string `json:"role"`
}

// mergedAtFor resolves a seeded PR's merge date.
//
// Absent is an error rather than a default: a claim whose evidence has no
// known merge date would silently pass every age filter, which is the bug this
// lookup exists to prevent.
func mergedAtFor(merges map[string]time.Time, repo string, prNumber int) (time.Time, error) {
	at, ok := merges[fmt.Sprintf("%s#%d", repo, prNumber)]
	if !ok {
		return time.Time{}, fmt.Errorf(
			"%s#%d has no merged_at in the repositories seed", repo, prNumber)
	}
	return at, nil
}

// mergeDates reads every seeded pull request's merge date.
func mergeDates() (map[string]time.Time, error) {
	var file repositoriesSeed
	if err := readSeed("repositories", &file); err != nil {
		return nil, err
	}

	out := make(map[string]time.Time, len(file.PullRequests))
	for key, pr := range file.PullRequests {
		if pr.MergedAt == nil {
			continue
		}
		at, err := time.Parse(time.RFC3339, *pr.MergedAt)
		if err != nil {
			return nil, fmt.Errorf("%s merged_at: %w", key, err)
		}
		out[key] = at
	}
	return out, nil
}

// seedClaim is a pre-scored claim. Fixed ids let a fixture reference one
// directly and make a failure read the same on every run.
type seedClaim struct {
	Key              string           `json:"key"`
	ID               string           `json:"id"`
	User             string           `json:"user"`
	Status           string           `json:"status"`
	Version          int              `json:"version"`
	EvaluatedAt      string           `json:"evaluated_at"`
	LockedUntil      string           `json:"locked_until"`
	NominatedPrimary string           `json:"nominated_primary"`
	PREvidence       []seedPREvidence `json:"pr_evidence"`
}

// seedPRSkillScore pins one (PR, skill) score, so ranking is reproducible
// without running the evaluator.
type seedPRSkillScore struct {
	Claim    string  `json:"claim"`
	Position int     `json:"position"`
	Skill    string  `json:"skill"`
	Score    float64 `json:"score"`
	QualityQ float64 `json:"quality_q"`
	ReachR   float64 `json:"reach_r"`
	Engage   float64 `json:"engagement_e"`
}

// seedUserSkill is a contributor's standing in one skill.
type seedUserSkill struct {
	User            string  `json:"user"`
	Skill           string  `json:"skill"`
	Standing        string  `json:"standing"`
	DistinctPRCount int     `json:"distinct_pr_count"`
	Score           float64 `json:"score"`
	PRComponent     float64 `json:"pr_component"`
	ProjectComp     float64 `json:"project_component"`
}

// seedUserScore is the pair of user-level numbers. Both are pointers because
// null is not zero: a contributor with no primary skill has not been measured
// (ADR-0007).
type seedUserScore struct {
	User       string   `json:"user"`
	Overall    *float64 `json:"overall_score"`
	Generalist *float64 `json:"generalist_score"`
}

// seedAvailability overrides a contributor's window, so a fixture can seed a
// lapsed one without moving the clock (ADR-0010 §5).
type seedAvailability struct {
	User          string `json:"user"`
	Status        string `json:"status"`
	ExpiresInDays *int   `json:"expires_in_days"`
}

type scoredSet struct {
	Claims                []seedClaim        `json:"claims"`
	PRSkillScores         []seedPRSkillScore `json:"pr_skill_scores"`
	UserSkills            []seedUserSkill    `json:"user_skills"`
	Users                 []seedUserScore    `json:"users"`
	AvailabilityOverrides []seedAvailability `json:"availability_overrides"`
}

func seedScoredSet(ctx context.Context, conn *pgx.Conn, s scoredSet, bindings map[string]string) error {
	merges, err := mergeDates()
	if err != nil {
		return err
	}

	principals, err := LoadPrincipals()
	if err != nil {
		return err
	}
	userID := func(key string) (string, error) {
		c, ok := principals.Contributor(key)
		if !ok {
			return "", fmt.Errorf("contributor %q is not seeded", key)
		}
		return c.ID, nil
	}

	skillIDs, err := skillIDsBySlug(ctx, conn)
	if err != nil {
		return err
	}

	// claim key -> (claim id, user id, evaluation id)
	type claimRef struct{ claimID, userID, evaluationID string }
	claims := map[string]claimRef{}

	for _, c := range s.Claims {
		uid, err := userID(c.User)
		if err != nil {
			return err
		}
		if _, err := conn.Exec(ctx,
			`INSERT INTO claims (id, user_id, status, version, submitted_at, evaluated_at, locked_until)
			 VALUES ($1, $2, $3, $4, $5, $5, $6)`,
			c.ID, uid, c.Status, c.Version, nullableTime(c.EvaluatedAt), nullableTime(c.LockedUntil)); err != nil {
			return fmt.Errorf("claim %s: %w", c.Key, err)
		}

		// The declared skills, derived from what the claim was scored on.
		//
		// A real claim names its skills before submission and the evaluator
		// scores those; seeding the scores without the declaration produces a
		// claim that was judged on skills nobody claimed, which no run could
		// have created. It is also what claim reads count.
		for _, slug := range claimSkillSlugs(s.PRSkillScores, c.Key) {
			skillID, ok := skillIDs[slug]
			if !ok {
				return fmt.Errorf("claim %s references unknown skill %q", c.Key, slug)
			}
			if _, err := conn.Exec(ctx,
				`INSERT INTO claim_skills (id, claim_id, skill_id, origin, is_nominated_primary)
				 VALUES (gen_random_uuid(), $1, $2, 'user_declared', $3)
				 ON CONFLICT DO NOTHING`,
				c.ID, skillID, slug == c.NominatedPrimary); err != nil {
				return fmt.Errorf("claim %s skill %s: %w", c.Key, slug, err)
			}
		}

		for _, e := range c.PREvidence {
			// The merge date comes from the repositories seed, not from
			// now(). It is what evidence_within_months filters on, and
			// stamping every seeded PR as merged this instant makes an age
			// filter incapable of excluding anything.
			mergedAt, err := mergedAtFor(merges, e.Repo, e.PRNumber)
			if err != nil {
				return fmt.Errorf("claim %s evidence %d: %w", c.Key, e.Position, err)
			}

			owner, repo, err := splitRepo(e.Repo)
			if err != nil {
				return fmt.Errorf("claim %s position %d: %w", c.Key, e.Position, err)
			}
			url := fmt.Sprintf("https://github.com/%s/%s/pull/%d", owner, repo, e.PRNumber)
			if _, err := conn.Exec(ctx,
				`INSERT INTO claim_pr_evidence
				   (id, claim_id, pr_url, repo_owner, repo_name, pr_number, position, role, verified_at, enriched_at, merged_at)
				 VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6, $7, now(), now(), $8)`,
				c.ID, url, owner, repo, e.PRNumber, e.Position, e.Role, mergedAt); err != nil {
				return fmt.Errorf("claim %s evidence %d: %w", c.Key, e.Position, err)
			}
		}

		// A scored PR needs an evaluation to hang off. Writing the scores
		// without one would leave a shape no real run could produce.
		//
		// The fingerprint comes from the repository's own function rather than
		// a stand-in, so a seeded claim resubmitted unchanged is recognised as
		// unchanged — which a placeholder hash never could be.
		evidence, err := postgres.Fingerprint(ctx, conn, c.ID)
		if err != nil {
			return fmt.Errorf("claim %s fingerprint: %w", c.Key, err)
		}

		var evaluationID string
		if err := conn.QueryRow(ctx,
			`INSERT INTO evaluations
			   (id, claim_id, claim_version, rubric_version, model, prompt_version,
			    evidence_fingerprint, trigger, status, completed_at)
			 VALUES (gen_random_uuid(), $1, $2, 'v1', 'e2e-fake', 'v1',
			         $3, 'submission', 'succeeded', now())
			 RETURNING id`, c.ID, c.Version, []byte(evidence)).Scan(&evaluationID); err != nil {
			return fmt.Errorf("claim %s evaluation: %w", c.Key, err)
		}
		claims[c.Key] = claimRef{claimID: c.ID, userID: uid, evaluationID: evaluationID}
		// A fixture reaches a seeded claim as {{alice_go_claim.id}}; nothing in
		// the step sequence could bind it, because it existed before step 0.
		bindings[c.Key+".id"] = c.ID
	}

	for _, sc := range s.PRSkillScores {
		ref, ok := claims[sc.Claim]
		if !ok {
			return fmt.Errorf("pr_skill_scores references unknown claim %q", sc.Claim)
		}
		skillID, ok := skillIDs[sc.Skill]
		if !ok {
			return fmt.Errorf("pr_skill_scores references unknown skill %q", sc.Skill)
		}

		var owner, repo string
		var prNumber int
		if err := conn.QueryRow(ctx,
			`SELECT repo_owner, repo_name, pr_number FROM claim_pr_evidence
			 WHERE claim_id = $1 AND position = $2`,
			ref.claimID, sc.Position).Scan(&owner, &repo, &prNumber); err != nil {
			return fmt.Errorf("score for claim %s position %d: %w", sc.Claim, sc.Position, err)
		}

		if _, err := conn.Exec(ctx,
			`INSERT INTO pr_skill_scores
			   (id, evaluation_id, skill_id, repo_owner, repo_name, pr_number, position,
			    dimension_scores, score, quality_q, reach_r, engagement_e, norms_generation)
			 VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6, '{}'::jsonb, $7, $8, $9, $10, 1)`,
			ref.evaluationID, skillID, owner, repo, prNumber, sc.Position,
			sc.Score, sc.QualityQ, sc.ReachR, sc.Engage); err != nil {
			return fmt.Errorf("pr_skill_score %s/%d: %w", sc.Claim, sc.Position, err)
		}

		// A scored link is what standing counts. Seeding user_skills without
		// these would let a recompute silently disagree with the seed.
		if _, err := conn.Exec(ctx,
			`INSERT INTO user_skill_pr_links
			   (user_id, skill_id, repo_owner, repo_name, pr_number, claim_id, status)
			 VALUES ($1, $2, $3, $4, $5, $6, 'scored')
			 ON CONFLICT DO NOTHING`,
			ref.userID, skillID, owner, repo, prNumber, ref.claimID); err != nil {
			return fmt.Errorf("link %s/%d: %w", sc.Claim, sc.Position, err)
		}
	}

	for _, us := range s.UserSkills {
		uid, err := userID(us.User)
		if err != nil {
			return err
		}
		skillID, ok := skillIDs[us.Skill]
		if !ok {
			return fmt.Errorf("user_skills references unknown skill %q", us.Skill)
		}
		if _, err := conn.Exec(ctx,
			// promoted_at is deliberately NULL. It records that a promotion
			// was OBSERVED, and a standing that existed before step 0 was
			// never observed being promoted — stamping it would claim an
			// event that never happened.
			`INSERT INTO user_skills
			   (user_id, skill_id, distinct_pr_count, standing, score, pr_component, project_component,
			    last_evaluated_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, now())
			 ON CONFLICT (user_id, skill_id) DO UPDATE
			   SET distinct_pr_count = EXCLUDED.distinct_pr_count,
			       standing          = EXCLUDED.standing,
			       score             = EXCLUDED.score`,
			uid, skillID, us.DistinctPRCount, us.Standing, us.Score, us.PRComponent, us.ProjectComp); err != nil {
			return fmt.Errorf("user_skill %s/%s: %w", us.User, us.Skill, err)
		}
	}

	for _, u := range s.Users {
		uid, err := userID(u.User)
		if err != nil {
			return err
		}
		if _, err := conn.Exec(ctx,
			`UPDATE users SET overall_score = $2, generalist_score = $3 WHERE id = $1`,
			uid, u.Overall, u.Generalist); err != nil {
			return fmt.Errorf("user scores %s: %w", u.User, err)
		}
	}

	for _, a := range s.AvailabilityOverrides {
		uid, err := userID(a.User)
		if err != nil {
			return err
		}
		var expires *time.Time
		if a.ExpiresInDays != nil {
			t := time.Now().UTC().AddDate(0, 0, *a.ExpiresInDays)
			expires = &t
		}
		if _, err := conn.Exec(ctx,
			`INSERT INTO user_availability (user_id, status, expires_at)
			 VALUES ($1, $2, $3)
			 ON CONFLICT (user_id) DO UPDATE
			   SET status = EXCLUDED.status, expires_at = EXCLUDED.expires_at`,
			uid, a.Status, expires); err != nil {
			return fmt.Errorf("availability override %s: %w", a.User, err)
		}
	}
	return nil
}

func skillIDsBySlug(ctx context.Context, conn *pgx.Conn) (map[string]string, error) {
	rows, err := conn.Query(ctx, `SELECT slug, id FROM skills`)
	if err != nil {
		return nil, fmt.Errorf("reading skills: %w", err)
	}
	defer rows.Close()

	out := map[string]string{}
	for rows.Next() {
		var slug, id string
		if err := rows.Scan(&slug, &id); err != nil {
			return nil, err
		}
		out[slug] = id
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no skills seeded — the 'catalogue' set must be loaded first")
	}
	return out, nil
}

func splitRepo(s string) (owner, repo string, err error) {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("%q is not owner/repo", s)
	}
	return parts[0], parts[1], nil
}

func nullableTime(s string) *time.Time {
	if s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil
	}
	return &t
}

// claimSkillSlugs lists the distinct skills one claim was scored against, in
// first-seen order so the seed is deterministic.
func claimSkillSlugs(scores []seedPRSkillScore, claimKey string) []string {
	var (
		out  []string
		seen = map[string]struct{}{}
	)
	for _, sc := range scores {
		if sc.Claim != claimKey {
			continue
		}
		if _, ok := seen[sc.Skill]; ok {
			continue
		}
		seen[sc.Skill] = struct{}{}
		out = append(out, sc.Skill)
	}
	return out
}
