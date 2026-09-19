package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// SearchRepository owns the ranked query.
//
// One method for search, because ADR-0008 §1 specifies one query: rank over
// the UNFILTERED population in a CTE, then filter, then page. Splitting it
// would invite a second code path where the gates are applied as post-filters,
// and a post-filtered gate is a gate that leaks into a result count.
//
// The order is the whole design. Ranking after filtering would renumber, and
// the leaderboard — which shows everyone — would then disagree with search
// about who is third. Ranking first and filtering second is what produces the
// GAPS in the sequence that ADR-0008 §1a makes normal.
type SearchRepository struct{ db *DB }

// Search returns the discovery repository.
func (db *DB) Search() *SearchRepository { return &SearchRepository{db: db} }

var _ port.SearchRepository = (*SearchRepository)(nil)

// Paging bounds from ADR-0008 §1.
const (
	DefaultPerPage = 20
	MaxPerPage     = 50
)

// rankedPopulation ranks every scored contributor, before any filter.
//
// The only exclusion here is not_looking, and that is deliberate: an explicit
// opt-out leaves the RANKED POPULATION entirely, so no board carries a
// permanent gap that no parameter could ever fill. A merely lapsed contributor
// stays ranked and is hidden later, which is what makes their gap explainable.
// The clock's placeholder differs per query — this CTE is shared by three with
// different parameter lists — so it is built rather than pasted.
func rankedPopulation(nowParam, activeParam, baselineParam int) string {
	return fmt.Sprintf(`
	ranked AS (
	    SELECT u.id AS user_id,
	           u.overall_score,
	           u.generalist_score,
	           rank() OVER (ORDER BY u.overall_score DESC NULLS LAST)    AS overall_rank,
	           rank() OVER (ORDER BY u.generalist_score DESC NULLS LAST) AS generalist_rank,
	           a.status     AS availability_status,
	           a.expires_at AS availability_expires_at,
	           a.updated_at AS availability_updated_at,
	           (a.expires_at IS NOT NULL AND a.expires_at > $%[1]d) AS active
	    FROM users u
	    JOIN user_availability a ON a.user_id = u.id
	    WHERE u.deactivated_at IS NULL
	      AND a.status <> 'not_looking'
	      AND %[2]s = $%[3]d::text
	)`, nowParam, scoreRubricVersion("u.id", baselineParam), activeParam)
}

// scoreRubricVersion is the rubric a contributor's scores were produced by.
//
// ADR-0008 §1 gates discovery on `rubric_version = $active`: bumping the
// active version takes the whole scored population OUT of search, and it
// drains back in as re-evaluation completes. A leaderboard mixing two versions
// would rank people by which one happened to judge them.
//
// A contributor with no completed evaluation has no version of their own and
// falls back to the baseline — the first rubric, which is what a score
// predating any sweep was produced under.
func scoreRubricVersion(userColumn string, baselineParam int) string {
	return fmt.Sprintf(`coalesce((
	    SELECT e.rubric_version
	    FROM evaluations e
	    JOIN claims c ON c.id = e.claim_id
	    WHERE c.user_id = %s AND e.status = 'succeeded'
	    ORDER BY e.completed_at DESC NULLS LAST
	    LIMIT 1
	), $%d::text)`, userColumn, baselineParam)
}

// alreadyShortlisted is everybody on a round for one role (ADR-0008
// amendment 3).
//
// A CTE over the ROUNDS rather than a correlated subquery per candidate:
// measured at 1,000 contributors and 500 shortlists it resolves in 0.84ms
// against idx_shortlists_role, and it does not grow with the population —
// only with how heavily that one job has been worked.
//
// DISTINCT because one person can be on several rounds for one role, and the
// anti-join wants people rather than entries.
const alreadyShortlisted = `
	already AS (
	    SELECT DISTINCT e.user_id
	      FROM shortlist_entries e
	      JOIN shortlists s ON s.id = e.shortlist_id
	     WHERE $%[1]d::uuid IS NOT NULL AND s.role_id = $%[1]d::uuid
	)`

// eligibleCTE applies every gate except the inactive one, which is counted
// separately so a hidden contributor can be REPORTED rather than silently
// vanishing from a result count.
// The three profile parameters are passed as a BASE rather than fixed, because
// the page and the count renumber everything: the count drops the paging pair,
// so what is $16 in one query is $14 in the other. Naming the base once is what
// keeps the two from drifting — and a drift here would make a total the page
// could never produce.
func eligibleCTE(nowParam, profileParam int) string {
	ossParam, countryParam, shapeParam := profileParam, profileParam+1, profileParam+2
	return fmt.Sprintf(`	eligible AS (
	    -- `+"`shortlisted`"+` is carried as a COLUMN rather than filtered away,
	    -- so the count of who was dropped and the page itself come off one
	    -- pass. Filtering here would need a second CTE layer and a second
	    -- materialisation of the whole eligible set to count what it removed.
	    SELECT r.*, (sl.user_id IS NOT NULL) AS shortlisted
	    FROM ranked r
	    -- LEFT, not inner: a contributor who has never opened the profile form
	    -- must still be findable. They simply fail every filter below that is
	    -- actually set.
	    LEFT JOIN user_work_preferences w ON w.user_id = r.user_id
	    LEFT JOIN already sl ON sl.user_id = r.user_id
	    WHERE NOT EXISTS (
	              -- AssertNotSelf, as a join condition rather than a
	              -- post-filter, so a self-match never reaches a result count.
	              SELECT 1
	              FROM user_github_identities hi
	              JOIN user_github_identities ui ON ui.github_user_id = hi.github_user_id
	              WHERE hi.hirer_account_id = $1 AND ui.user_id = r.user_id
	          )
	      AND ($2::text[] IS NULL OR cardinality($2::text[]) = 0 OR EXISTS (
	              SELECT 1
	              FROM user_skills us
	              JOIN skills s ON s.id = us.skill_id
	              WHERE us.user_id = r.user_id
	                AND s.slug = ANY($2::text[])
	                AND us.standing = 'primary'
	                AND ($4::numeric IS NULL OR us.score >= $4::numeric)
	              GROUP BY us.user_id
	              HAVING count(DISTINCT us.skill_id) = $3::int
	          ))
	      AND ($5::numeric IS NULL OR r.overall_score    >= $5::numeric)
	      AND ($6::numeric IS NULL OR r.generalist_score >= $6::numeric)
	      AND ($7::text[]  IS NULL OR r.availability_status::text = ANY($7::text[]))
	      AND ($9::text    IS NULL OR EXISTS (
	              SELECT 1 FROM users u
	              LEFT JOIN user_github_identities gi ON gi.user_id = u.id
	              WHERE u.id = r.user_id
	                AND (u.display_name ILIKE '%%' || $9 || '%%' OR gi.github_login ILIKE '%%' || $9 || '%%')
	          ))

	      -- What the contributor said about themselves (ADR-0018), matched in
	      -- SQL rather than after the fact so a hidden row never reaches a
	      -- result count.
	      --
	      -- Every one of these is OPT-IN, so a stated minimum is NOT cleared by
	      -- an unstated figure: the LEFT JOIN yields nulls and the comparison
	      -- fails, which is the behaviour a hirer asking for five years means.
	      -- A contributor missed this way is the one the profile prompt exists
	      -- to reach.
	      --
	      -- No pay filter is here and none may be added (ADR-0018 §5).
	      AND ($10::int IS NULL OR w.office_yoe >= $10::int)

	      -- Open-source years, VERIFIED and dated from AUTHORSHIP (ADR-0019
	      -- §7). first_pr_verified_at being null means a fetch we could not
	      -- complete, and unknown never clears a checked minimum — otherwise
	      -- the minimum is clearable with a link nobody could read.
	      AND ($%[2]d::int IS NULL OR (
	              w.first_pr_verified_at IS NOT NULL
	              AND w.first_pr_authored_at IS NOT NULL
	              AND w.first_pr_authored_at
	                  <= $%[1]d::timestamptz - ($%[2]d::int || ' years')::interval))

	      AND ($%[3]d::text[] IS NULL OR cardinality($%[3]d::text[]) = 0
	           OR w.current_country = ANY($%[3]d::text[]))

	      -- The shapes they ticked, ANY of them. Composed with availability
	      -- exactly as ADR-0018 §4 requires: somebody is available for the
	      -- shapes they enabled and for nothing else, and the availability
	      -- gate above has already run.
	      AND ($%[4]d::text[] IS NULL OR cardinality($%[4]d::text[]) = 0 OR (
	              ('remote'     = ANY($%[4]d::text[]) AND w.open_to_remote)
	           OR ('onsite'     = ANY($%[4]d::text[]) AND w.open_to_onsite)
	           OR ('contract'   = ANY($%[4]d::text[]) AND w.open_to_contract)
	           OR ('internship' = ANY($%[4]d::text[]) AND w.open_to_internships)))
	)`, nowParam, ossParam, countryParam, shapeParam)
}

// visibleCTE applies the two gates that are parameters: the inactive toggle,
// and the already-on-this-round exclusion.
const visibleCTE = `visible AS (
	SELECT * FROM eligible WHERE (active OR $8::boolean) AND NOT shortlisted)`

// Search runs the ranked query.
func (r *SearchRepository) Search(ctx context.Context, caller domain.HirerID, q domain.SearchQuery) (*domain.SearchResults, error) {
	perPage := q.PerPage
	switch {
	case perPage <= 0:
		perPage = DefaultPerPage
	case perPage > MaxPerPage:
		return nil, fmt.Errorf("per_page %d exceeds the maximum of %d: %w", perPage, MaxPerPage, port.ErrConflict)
	}
	page := q.Page
	if page <= 0 {
		page = 1
	}

	ordering, orderBy := resolveOrdering(q)

	// A name search surfaces inactive contributors regardless of the toggle —
	// ADR-0008 §1b. Browsing the market is a question about who is available;
	// typing a name is a question about a person you already know exists.
	includeInactive := q.IncludeInactive || q.Query != ""

	args := []any{
		string(caller),                     // $1
		q.Skills,                           // $2
		len(q.Skills),                      // $3
		q.MinSkillScore,                    // $4
		q.MinOverallScore,                  // $5
		q.MinGeneralistScore,               // $6
		availabilityFilter(q.Availability), // $7
		includeInactive,                    // $8
		nullIfEmpty(q.Query),               // $9
		q.MinOfficeYOE,                     // $10
		perPage,                            // $11
		(page - 1) * perPage,               // $12

		// The platform's clock, not the database's (ADR-0012). "Is this
		// availability live" and "was this merged recently" are business
		// questions, and answering them from a second clock nothing else reads
		// is what made this suite's result depend on the calendar date.
		r.db.now(), // $13
	}

	// The version discovery gates on, resolved once for the page and the
	// count so the two cannot disagree mid-request (ADR-0008 §1).
	active, err := r.db.activeRubricVersion(ctx)
	if err != nil {
		return nil, err
	}
	unversioned, err := r.db.unversionedRubricVersion(ctx, active)
	if err != nil {
		return nil, err
	}
	args = append(args, active, unversioned) // $14, $15

	// The contributor's own account of themselves (ADR-0018). Appended LAST
	// rather than slotted in beside the other filters: $14 and $15 are the
	// rubric versions, and renumbering them would move two parameters that
	// every comment in this file names by position.
	args = append(args,
		q.MinOSSYOE, // $16
		q.Countries, // $17
		q.OpenTo,    // $18

		// The role whose existing candidates to exclude. Last, and nullable:
		// most searches are not for a particular job.
		roleArg(q.ForRole), // $19
	)

	query := `
	WITH` + fmt.Sprintf(alreadyShortlisted, 19) + `,
	` + rankedPopulation(13, 14, 15) + `,
	` + eligibleCTE(13, 16) + `,
	` + visibleCTE + `
	SELECT (SELECT count(*) FROM visible)                    AS total,
	       -- Each count means EXACTLY ONE thing. Inactive counts only those
	       -- hidden for being quiet, so it excludes the already-shortlisted
	       -- rather than absorbing them — two reasons behind one number is a
	       -- number a hirer cannot act on.
	       (SELECT count(*) FROM eligible WHERE NOT shortlisted)
	           - (SELECT count(*) FROM visible)               AS inactive_hidden,
	       (SELECT count(*) FROM eligible WHERE shortlisted)  AS already_shortlisted,
	       v.user_id, u.display_name, coalesce(gi.github_login, ''),
	       v.active, v.availability_status, v.availability_expires_at, v.availability_updated_at,
	       v.overall_score, v.generalist_score,
	       ` + orderBy + ` AS rank
	FROM visible v
	JOIN users u ON u.id = v.user_id
	LEFT JOIN user_github_identities gi ON gi.user_id = v.user_id
	ORDER BY rank, u.display_name
	LIMIT $11 OFFSET $12`

	rows, err := r.db.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, translate(err, "searching")
	}
	defer rows.Close()

	results := domain.SearchResults{RankedBy: ordering, Page: page, PerPage: perPage}
	for rows.Next() {
		var (
			res       domain.SearchResult
			status    string
			expiresAt *time.Time
			updatedAt *time.Time
		)
		if err := rows.Scan(&results.Total, &results.InactiveHidden,
			&results.AlreadyShortlisted,
			&res.UserID, &res.DisplayName, &res.GitHubLogin,
			&res.Active, &status, &expiresAt, &updatedAt,
			&res.OverallScore, &res.GeneralistScore, &res.Rank); err != nil {
			return nil, translate(err, "scanning search result")
		}
		res.Availability = &domain.Availability{
			Status:    domain.AvailabilityStatus(status),
			ExpiresAt: expiresAt,
		}
		if updatedAt != nil {
			res.Availability.LastSetAt = *updatedAt
			res.LastConfirmedAt = updatedAt
		}
		// Both numbers, because they answer different questions and differ by
		// the 15-day window: how stale the signal is, and how long they have
		// been gone (ADR-0008 §1a). Exact, never bucketed.
		if !res.Active && expiresAt != nil {
			days := int(r.db.now().Sub(*expiresAt).Hours() / 24)
			res.InactiveForDays = &days
		}
		results.Results = append(results.Results, res)
	}
	if err := rows.Err(); err != nil {
		return nil, translate(err, "searching")
	}

	// A page past the end returns no rows, so the aggregate columns never
	// arrive. Recount rather than reporting zero, which would read as "nobody
	// matched" when the truth is "nobody on THIS page".
	if len(results.Results) == 0 {
		total, hidden, shortlisted, err := r.counts(ctx, args, active, unversioned)
		if err != nil {
			return nil, err
		}
		results.Total, results.InactiveHidden = total, hidden
		results.AlreadyShortlisted = shortlisted
	}

	if err := r.attachResultSkills(ctx, results.Results, q.Skills); err != nil {
		return nil, err
	}
	return &results, nil
}

// attachResultSkills fills in the standing each result is being shown for.
//
// A second query rather than a join, because the first one is already a
// window over a ranked population and joining a second per-user set onto it
// would multiply the rows the LIMIT counts — the page would hold fewer people
// than asked for, and how many fewer would depend on their skills.
//
// PRIMARY only. A search result is a ranked position, and a secondary skill
// has none (ADR-0007); the full picture including secondaries is what the
// scorecard is for. When the query named skills, only those are shown — the
// recruiter asked about Go, not about everything else this person can do.
func (r *SearchRepository) attachResultSkills(ctx context.Context, results []domain.SearchResult, only []string) error {
	if len(results) == 0 {
		return nil
	}

	ids := make([]string, 0, len(results))
	for _, res := range results {
		ids = append(ids, string(res.UserID))
	}

	rows, err := r.db.pool.Query(ctx, `
		SELECT us.user_id, s.slug, s.name, us.standing, coalesce(us.score, 0),
		       (SELECT count(*) + 1 FROM user_skills peer
		        WHERE peer.skill_id = us.skill_id AND peer.standing = 'primary'
		          AND peer.score > us.score)
		FROM user_skills us
		JOIN skills s ON s.id = us.skill_id
		WHERE us.user_id = ANY($1)
		  AND us.standing = 'primary'
		  AND (coalesce(cardinality($2::text[]), 0) = 0 OR s.slug = ANY($2))
		ORDER BY us.score DESC NULLS LAST, s.slug`, ids, only)
	if err != nil {
		return translate(err, "reading result skills")
	}
	defer rows.Close()

	byUser := make(map[domain.UserID][]domain.ResultSkill, len(results))
	for rows.Next() {
		var (
			id domain.UserID
			rs domain.ResultSkill
		)
		if err := rows.Scan(&id, &rs.Slug, &rs.Name, &rs.Standing, &rs.Score, &rs.Rank); err != nil {
			return translate(err, "scanning result skill")
		}
		byUser[id] = append(byUser[id], rs)
	}
	if err := rows.Err(); err != nil {
		return translate(err, "reading result skills")
	}

	for i := range results {
		results[i].Skills = byUser[results[i].UserID]
	}
	return nil
}

// resolveOrdering picks the ranking a result set is expressed in.
//
// A rank is meaningless without saying rank in WHAT: a multi-skill query has
// no single skill score to order by, so the response names the order it used
// rather than leaving `rank` to mean different things in different responses
// (ADR-0008 §1).
func resolveOrdering(q domain.SearchQuery) (domain.RankedBy, string) {
	if len(q.Skills) == 1 {
		// Ranked within the skill, over the whole population holding it as
		// primary — not within the filtered page.
		// 1 + the number of primary holders scoring higher.
		//
		// NOT rank() OVER inside a correlated subquery: that window sees only
		// the one row it was correlated to and returns 1 for everybody, which
		// silently collapses the ordering to the tie-breaker.
		return domain.RankedBySkill(q.Skills[0]), `(
			SELECT count(*) + 1
			FROM user_skills peer
			JOIN skills ps ON ps.id = peer.skill_id
			WHERE ps.slug = $2[1]
			  AND peer.standing = 'primary'
			  AND peer.score > (
			      SELECT us2.score FROM user_skills us2
			      JOIN skills s2 ON s2.id = us2.skill_id
			      WHERE s2.slug = $2[1] AND us2.user_id = v.user_id
			  )
		)`
	}
	if q.MinGeneralistScore != nil {
		return domain.RankedByGeneralist, "v.generalist_rank"
	}
	return domain.RankedByOverall, "v.overall_rank"
}

// counts runs the same CTEs without paging, so an empty page still reports
// how many matched and how many were hidden.
//
// It takes args[:10] — the filter parameters — then the clock, the two rubric
// versions, and the profile filters. The paging pair ($11, $12) does not
// appear in this query, and passing them would be an arity error.
//
// EVERY filter the page applies has to be applied here too. A count computed
// from a narrower filter set would report a total the page cannot produce, and
// a hirer would page towards results that do not exist — which is exactly what
// happened when the profile filters were added and this slice was left alone.
func (r *SearchRepository) counts(ctx context.Context, args []any, active, unversioned string) (total, hidden, shortlisted int, err error) {
	// args[15:] is MinOSSYOE, Countries, OpenTo — the three appended after the
	// rubric versions. They land at $14, $15, $16 here, which is what the
	// renumbered CTEs below expect.
	// args[15:18] is MinOSSYOE, Countries, OpenTo; args[18] is ForRole. They
	// land at $14..$17 here, which is what the renumbered CTEs expect.
	profile := args[15:]

	err = r.db.pool.QueryRow(ctx, `
	WITH`+fmt.Sprintf(alreadyShortlisted, 17)+`,
	`+rankedPopulation(11, 12, 13)+`,
	`+eligibleCTE(11, 14)+`,
	visible AS (
	    SELECT * FROM eligible WHERE (active OR $8::boolean) AND NOT shortlisted)
	SELECT (SELECT count(*) FROM visible),
	       (SELECT count(*) FROM eligible WHERE NOT shortlisted)
	           - (SELECT count(*) FROM visible),
	       (SELECT count(*) FROM eligible WHERE shortlisted)`,
		append(append(append([]any{}, args[:10]...), r.db.now(), active, unversioned),
			profile...)...).Scan(&total, &hidden, &shortlisted)
	if err != nil {
		return 0, 0, 0, translate(err, "counting search results")
	}
	return total, hidden, shortlisted, nil
}

func availabilityFilter(statuses []domain.AvailabilityStatus) []string {
	if len(statuses) == 0 {
		return nil
	}
	out := make([]string, len(statuses))
	for i, s := range statuses {
		out[i] = string(s)
	}
	return out
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Leaderboard is the definitive global ranking.
//
// It shows EVERYONE, inactive included, and takes no include_inactive — which
// is what makes a gap in a search explainable: the board is where a hirer
// finds who fills it (ADR-0008 §1).
//
// kind=skill returns only primary standings. Tie-breakers order the result but
// are never serialized; ties fall back to display name, which is the
// alphabetical ordering ADR-0005 specifies.
func (r *SearchRepository) Leaderboard(ctx context.Context, kind domain.LeaderboardKind, skill *domain.SkillID, limit int) (*domain.Leaderboard, error) {
	if limit <= 0 {
		limit = MaxPerPage
	}

	board := domain.Leaderboard{Kind: kind}

	// $1 limit, $2 the platform's clock. A board hides nobody for lapsing —
	// it shows everyone (ADR-0008 §1) — but it still reports who is active,
	// and that answer must come from the same clock as everything else.
	// A board reports the version it ranks BY, and shows only scores produced
	// under it. Mixing two versions would rank people by which one judged
	// them (ADR-0008 §1).
	active, activeErr := r.db.activeRubricVersion(ctx)
	if activeErr != nil {
		return nil, activeErr
	}
	unversioned, activeErr := r.db.unversionedRubricVersion(ctx, active)
	if activeErr != nil {
		return nil, activeErr
	}
	board.RubricVersion = active

	var query string
	args := []any{limit, r.db.now(), active, unversioned}

	switch kind {
	case domain.BoardSkill:
		if skill == nil {
			return nil, fmt.Errorf("kind=skill requires a skill: %w", port.ErrConflict)
		}
		args = append(args, string(*skill))
		query = `
			SELECT rank() OVER (ORDER BY us.score DESC NULLS LAST, u.display_name),
			       u.id, u.display_name, coalesce(gi.github_login, ''), us.score,
			       (a.expires_at IS NOT NULL AND a.expires_at > $2)
			FROM user_skills us
			JOIN users u ON u.id = us.user_id
			JOIN user_availability a ON a.user_id = u.id
			LEFT JOIN user_github_identities gi ON gi.user_id = u.id
			WHERE us.skill_id = $5 AND us.standing = 'primary'
			  AND u.deactivated_at IS NULL AND a.status <> 'not_looking'
			  AND ` + scoreRubricVersion("u.id", 4) + ` = $3::text
			ORDER BY 1
			LIMIT $1`

		s, err := r.skillByID(ctx, *skill)
		if err != nil {
			return nil, err
		}
		board.Skill = s

	case domain.BoardGeneralist, domain.BoardOverall:
		column := "u.overall_score"
		if kind == domain.BoardGeneralist {
			column = "u.generalist_score"
		}
		query = `
			SELECT rank() OVER (ORDER BY ` + column + ` DESC NULLS LAST, u.display_name),
			       u.id, u.display_name, coalesce(gi.github_login, ''), ` + column + `,
			       (a.expires_at IS NOT NULL AND a.expires_at > $2)
			FROM users u
			JOIN user_availability a ON a.user_id = u.id
			LEFT JOIN user_github_identities gi ON gi.user_id = u.id
			WHERE u.deactivated_at IS NULL AND a.status <> 'not_looking'
			  AND ` + column + ` IS NOT NULL
			  AND ` + scoreRubricVersion("u.id", 4) + ` = $3::text
			ORDER BY 1
			LIMIT $1`

	default:
		return nil, fmt.Errorf("unknown leaderboard kind %q: %w", kind, port.ErrConflict)
	}

	rows, err := r.db.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, translate(err, "reading leaderboard")
	}
	defer rows.Close()

	for rows.Next() {
		var e domain.LeaderboardEntry
		if err := rows.Scan(&e.Rank, &e.UserID, &e.DisplayName, &e.GitHubLogin,
			&e.Score, &e.Active); err != nil {
			return nil, translate(err, "scanning leaderboard entry")
		}
		board.Entries = append(board.Entries, e)
	}
	return &board, translate(rows.Err(), "reading leaderboard")
}

// Scorecard is the full evidence dossier.
//
// SECONDARY SKILLS APPEAR HERE, unranked. Standing gates SEARCHABILITY, not
// disclosure: a hirer already looking at someone should see everything that
// person evidenced (ADR-0008 §2).
//
// The availability gate does not apply — a lapsed contributor's scorecard is
// readable, because a hirer who found them through include_inactive must be
// able to read the evidence. Only not_looking is hidden.
func (r *SearchRepository) Scorecard(ctx context.Context, caller domain.HirerID, target domain.UserID) (*domain.Scorecard, error) {
	var (
		card      domain.Scorecard
		status    string
		expiresAt *time.Time
		updatedAt *time.Time
	)
	err := r.db.pool.QueryRow(ctx, `
		SELECT u.id, u.display_name, coalesce(gi.github_login, ''),
		       (a.expires_at IS NOT NULL AND a.expires_at > $3),
		       a.status, a.expires_at, a.updated_at,
		       u.overall_score, u.generalist_score
		FROM users u
		JOIN user_availability a ON a.user_id = u.id
		LEFT JOIN user_github_identities gi ON gi.user_id = u.id
		WHERE u.id = $1
		  AND u.deactivated_at IS NULL
		  AND a.status <> 'not_looking'
		  -- An unauthenticated read through a share link has no caller, so
		  -- there is nobody for AssertNotSelf to compare against. Guarded
		  -- rather than passed an empty uuid, which fails the cast.
		  AND ($2 = '' OR NOT EXISTS (
		          SELECT 1
		          FROM user_github_identities hi
		          JOIN user_github_identities ui ON ui.github_user_id = hi.github_user_id
		          WHERE hi.hirer_account_id = $2::uuid AND ui.user_id = u.id
		      ))`, string(target), string(caller), r.db.now(),
	).Scan(&card.User.UserID, &card.User.DisplayName, &card.User.GitHubLogin,
		&card.User.Active, &status, &expiresAt, &updatedAt,
		&card.User.OverallScore, &card.User.GeneralistScore)
	if err != nil {
		// AssertNotSelf and not_looking both land here as not-found. A
		// distinguishable response would confirm the identity link.
		return nil, translate(err, fmt.Sprintf("scorecard for %s", target))
	}

	card.User.Availability = &domain.Availability{
		Status: domain.AvailabilityStatus(status), ExpiresAt: expiresAt,
	}
	if updatedAt != nil {
		card.User.LastConfirmedAt = updatedAt
		if !card.User.Active && expiresAt != nil {
			days := int(r.db.now().Sub(*expiresAt).Hours() / 24)
			card.User.InactiveForDays = &days
		}
	}

	rows, err := r.db.pool.Query(ctx, `
		SELECT s.slug, s.name, us.standing, coalesce(us.score, 0), us.distinct_pr_count,
		       CASE WHEN us.standing = 'primary' THEN
		           (SELECT count(*) + 1 FROM user_skills peer
		            WHERE peer.skill_id = us.skill_id AND peer.standing = 'primary'
		              AND peer.score > us.score)
		       END AS rank
		FROM user_skills us
		JOIN skills s ON s.id = us.skill_id
		WHERE us.user_id = $1
		ORDER BY us.score DESC NULLS LAST, s.slug`, string(target))
	if err != nil {
		return nil, translate(err, "reading scorecard skills")
	}
	defer rows.Close()

	for rows.Next() {
		var sk domain.ScorecardSkill
		if err := rows.Scan(&sk.Slug, &sk.Name, &sk.Standing, &sk.Score,
			&sk.DistinctPRCount, &sk.Rank); err != nil {
			return nil, translate(err, "scanning scorecard skill")
		}
		card.Skills = append(card.Skills, sk)
	}
	if err := rows.Err(); err != nil {
		return nil, translate(err, "reading scorecard")
	}

	if err := r.attachScorecardEvidence(ctx, target, card.Skills); err != nil {
		return nil, err
	}
	return &card, nil
}

// attachScorecardEvidence fills in the PRs behind each skill's score.
//
// A scorecard is a dossier: the number is an assertion and these are what back
// it, which is the whole difference between this platform and a CV. Best first,
// because a hirer reading five reads the top one or two.
func (r *SearchRepository) attachScorecardEvidence(ctx context.Context, target domain.UserID, skills []domain.ScorecardSkill) error {
	if len(skills) == 0 {
		return nil
	}

	rows, err := r.db.pool.Query(ctx, `
		SELECT s.slug, pss.repo_owner || '/' || pss.repo_name, pss.pr_number,
		       e.title, e.merged_at, pss.score, pss.dimension_scores, e.pr_url
		FROM pr_skill_scores pss
		JOIN skills s ON s.id = pss.skill_id
		JOIN evaluations ev ON ev.id = pss.evaluation_id AND ev.status = 'succeeded'
		JOIN claims c ON c.id = ev.claim_id AND c.user_id = $1
		LEFT JOIN claim_pr_evidence e
		       ON e.claim_id = c.id AND e.position = pss.position
		ORDER BY s.slug, pss.score DESC`, string(target))
	if err != nil {
		return translate(err, "reading scorecard evidence")
	}
	defer rows.Close()

	bySlug := map[string][]domain.ScorecardEvidence{}
	for rows.Next() {
		var (
			slug     string
			ev       domain.ScorecardEvidence
			title    *string
			mergedAt *time.Time
		)
		var (
			dims []byte
			url  *string
		)
		if err := rows.Scan(&slug, &ev.Repo, &ev.PRNumber, &title, &mergedAt, &ev.Score, &dims, &url); err != nil {
			return translate(err, "scanning scorecard evidence")
		}
		if len(dims) > 0 {
			if err := json.Unmarshal(dims, &ev.Dimensions); err != nil {
				return fmt.Errorf("decoding dimension scores for %s#%d: %w", ev.Repo, ev.PRNumber, err)
			}
		}
		if title != nil {
			ev.Title = *title
		}
		if mergedAt != nil {
			ev.MergedAt = *mergedAt
		}
		if url != nil {
			ev.URL = *url
		}
		bySlug[slug] = append(bySlug[slug], ev)
	}
	if err := rows.Err(); err != nil {
		return translate(err, "reading scorecard evidence")
	}

	for i := range skills {
		skills[i].Evidence = bySlug[skills[i].Slug]
	}
	return nil
}

// Rank is a contributor's own position.
//
// Positions and totals and nothing identifying anyone else — no neighbours, no
// names, no adjacent scores. A ladder showing who sits immediately above you
// turns the platform into a competition against named individuals, which is
// what ADR-0005 guards against.
//
// Rank SURVIVES lapsing (ADR-0008 §1a). Only not_looking yields nulls, and
// that is a state the contributor chose.
func (r *SearchRepository) Rank(ctx context.Context, id domain.UserID) (*domain.Rank, error) {
	// RubricVersion is left to the service: it is process configuration, not
	// a fact this query can read.
	rank := domain.Rank{Ranked: true}

	// LEFT JOIN on availability: a contributor who has never set one still has
	// a rank to report — unranked and inactive, which is the truthful answer
	// on the first sign-in rather than a 404 saying they do not exist.
	var status string
	var expiresAt *time.Time
	// The SCORES come from users, the RANKS from the ranked population.
	//
	// Reading both through `ranked` would lose an opted-out contributor's
	// score along with their position, and the score is theirs: opting out
	// hides you, it does not unmake what your evidence was worth.
	active, err := r.db.activeRubricVersion(ctx)
	if err != nil {
		return nil, err
	}
	unversioned, err := r.db.unversionedRubricVersion(ctx, active)
	if err != nil {
		return nil, err
	}

	err = r.db.pool.QueryRow(ctx, `
		WITH`+rankedPopulation(2, 3, 4)+`
		SELECT u.overall_score, r.overall_rank,
		       -- The pool you are ranked among. ADR-0008 §6: not_looking is
		       -- excluded from the RANKED POPULATION, not merely hidden from
		       -- search — so the board renumbers rather than leaving a gap
		       -- nothing could fill. A contributor who never set availability
		       -- has not joined the pool at all.
		       (SELECT count(*) FROM users u2
		        JOIN user_availability a2 ON a2.user_id = u2.id
		        WHERE u2.deactivated_at IS NULL
		          AND a2.status <> 'not_looking'),
		       u.generalist_score, r.generalist_rank,
		       coalesce(a.status::text, ''), a.expires_at
		FROM users u
		LEFT JOIN user_availability a ON a.user_id = u.id
		LEFT JOIN ranked r ON r.user_id = u.id
		WHERE u.id = $1`, string(id), r.db.now(), active, unversioned,
	).Scan(&rank.Overall.Score, &rank.Overall.Rank, &rank.Overall.OutOf,
		&rank.Generalist.Score, &rank.Generalist.Rank, &status, &expiresAt)
	if err != nil {
		return nil, translate(err, fmt.Sprintf("rank for %s", id))
	}
	rank.Generalist.OutOf = rank.Overall.OutOf

	// Active means IN THE MARKET, which needs both a live window and a
	// willingness. Opting out sets a fresh window like any other statement of
	// availability, so the expiry alone would report an opted-out contributor
	// as active (ADR-0008 §6).
	optedOut := domain.AvailabilityStatus(status) == domain.NotLooking
	rank.Active = !optedOut && expiresAt != nil && expiresAt.After(r.db.now())

	// Reported only when the window actually LAPSED. An opted-out contributor
	// whose window is still open has not been inactive for a negative number
	// of days — they made a choice, which unranked_reason already says.
	if expiresAt != nil && !expiresAt.After(r.db.now()) {
		days := int(r.db.now().Sub(*expiresAt).Hours() / 24)
		rank.InactiveForDays = &days
	}

	if optedOut {
		rank.Ranked = false
		rank.UnrankedReason = "opted_out"
		rank.Overall.Rank, rank.Generalist.Rank = nil, nil
	} else if rank.Overall.Rank == nil || rank.Overall.Score == nil {
		// No primary skill, so no user-level score and no position. A window
		// function still hands an unscored row a position — everyone ties at
		// NULL — so the SCORE is what decides whether they are ranked at all.
		// Null is not zero: zero would claim we measured something (ADR-0007).
		rank.Ranked = false
		rank.UnrankedReason = "no_primary_skill"
		rank.Overall.Rank, rank.Generalist.Rank = nil, nil
	}

	rows, err := r.db.pool.Query(ctx, `
		SELECT s.slug, s.name, us.standing, coalesce(us.score, 0),
		       CASE WHEN us.standing = 'primary' THEN
		           (SELECT count(*) + 1 FROM user_skills peer
		            JOIN user_availability pa ON pa.user_id = peer.user_id
		            WHERE peer.skill_id = us.skill_id AND peer.standing = 'primary'
		              AND pa.status <> 'not_looking'
		              AND peer.score > us.score)
		       END,
		       -- Same population as the rank above it. A position out of a
		       -- field that included people who left it would not be the
		       -- position anyone competes in (ADR-0008 §6).
		       CASE WHEN us.standing = 'primary' THEN
		           (SELECT count(*) FROM user_skills peer
		            JOIN user_availability pa ON pa.user_id = peer.user_id
		            WHERE peer.skill_id = us.skill_id AND peer.standing = 'primary'
		              AND pa.status <> 'not_looking')
		       END
		FROM user_skills us
		JOIN skills s ON s.id = us.skill_id
		WHERE us.user_id = $1
		-- Standing before score, unlike every other skill listing. This
		-- endpoint answers "where do I stand", and a skill with a position
		-- answers it; a higher-scoring secondary does not. Dave's Go is the
		-- best Go score on the platform and still carries no rank.
		ORDER BY us.standing, us.score DESC NULLS LAST, s.slug`, string(id))
	if err != nil {
		return nil, translate(err, "reading skill ranks")
	}
	defer rows.Close()

	for rows.Next() {
		var sr domain.SkillRank
		if err := rows.Scan(&sr.Slug, &sr.Name, &sr.Standing, &sr.Score, &sr.Rank, &sr.OutOf); err != nil {
			return nil, translate(err, "scanning skill rank")
		}
		// An unranked skill has no position, and reporting one would
		// contradict the standing rule. Opting out unranks every skill at
		// once: the contributor left the population all of them are measured
		// in (ADR-0008 §6), so the field size stands and the position does
		// not.
		if sr.Standing != domain.Primary {
			sr.Rank, sr.OutOf = nil, nil
		} else if optedOut {
			sr.Rank = nil
		}
		rank.Skills = append(rank.Skills, sr)
	}
	return &rank, translate(rows.Err(), "reading skill ranks")
}

func (r *SearchRepository) skillByID(ctx context.Context, id domain.SkillID) (*domain.Skill, error) {
	var s domain.Skill
	err := r.db.pool.QueryRow(ctx,
		`SELECT id, slug, name, description, category, scoring_mode, is_active
		 FROM skills WHERE id = $1`, string(id),
	).Scan(&s.ID, &s.Slug, &s.Name, &s.Description, &s.Category, &s.ScoringMode, &s.IsActive)
	if err != nil {
		return nil, translate(err, fmt.Sprintf("skill %s", id))
	}
	return &s, nil
}

// roleArg passes a nullable role id without a driver-level nil interface.
func roleArg(id *domain.RoleID) *string {
	if id == nil {
		return nil
	}
	v := string(*id)
	return &v
}
