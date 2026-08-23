package postgres

import (
	"context"
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
const rankedPopulation = `
	ranked AS (
	    SELECT u.id AS user_id,
	           u.overall_score,
	           u.generalist_score,
	           rank() OVER (ORDER BY u.overall_score DESC NULLS LAST)    AS overall_rank,
	           rank() OVER (ORDER BY u.generalist_score DESC NULLS LAST) AS generalist_rank,
	           a.status     AS availability_status,
	           a.expires_at AS availability_expires_at,
	           a.updated_at AS availability_updated_at,
	           (a.expires_at IS NOT NULL AND a.expires_at > now()) AS active
	    FROM users u
	    JOIN user_availability a ON a.user_id = u.id
	    WHERE u.deactivated_at IS NULL
	      AND a.status <> 'not_looking'
	)`

// eligibleCTE applies every gate except the inactive one, which is counted
// separately so a hidden contributor can be REPORTED rather than silently
// vanishing from a result count.
const eligibleCTE = `	eligible AS (
	    SELECT r.*
	    FROM ranked r
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
	                AND (u.display_name ILIKE '%' || $9 || '%' OR gi.github_login ILIKE '%' || $9 || '%')
	          ))
	      AND ($10::int IS NULL OR EXISTS (
	              SELECT 1
	              FROM user_skill_pr_links l
	              JOIN claim_pr_evidence e
	                ON e.repo_owner = l.repo_owner AND e.repo_name = l.repo_name
	               AND e.pr_number = l.pr_number
	              WHERE l.user_id = r.user_id AND l.status = 'scored'
	                AND e.merged_at > now() - ($10::int || ' months')::interval
	          ))
	)`

// visibleCTE applies the one gate that is a parameter.
const visibleCTE = `visible AS (SELECT * FROM eligible WHERE active OR $8::boolean)`

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
		q.EvidenceWithinMonths,             // $10
		perPage,                            // $11
		(page - 1) * perPage,               // $12
	}

	query := `
	WITH` + rankedPopulation + `,
	` + eligibleCTE + `,
	` + visibleCTE + `
	SELECT (SELECT count(*) FROM visible)                    AS total,
	       (SELECT count(*) FROM eligible) - (SELECT count(*) FROM visible) AS inactive_hidden,
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
			days := int(nowFrom(ctx).Sub(*expiresAt).Hours() / 24)
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
		total, hidden, err := r.counts(ctx, args)
		if err != nil {
			return nil, err
		}
		results.Total, results.InactiveHidden = total, hidden
	}
	return &results, nil
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
// counts takes args[:10] — the filter parameters. The paging pair ($11, $12)
// does not appear in this query, and passing them would be an arity error.
func (r *SearchRepository) counts(ctx context.Context, args []any) (total, hidden int, err error) {
	err = r.db.pool.QueryRow(ctx, `
	WITH`+rankedPopulation+`,
	`+eligibleCTE+`,
	visible AS (SELECT * FROM eligible WHERE active OR $8::boolean)
	SELECT (SELECT count(*) FROM visible),
	       (SELECT count(*) FROM eligible) - (SELECT count(*) FROM visible)`, args[:10]...).Scan(&total, &hidden)
	if err != nil {
		return 0, 0, translate(err, "counting search results")
	}
	return total, hidden, nil
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

	var query string
	args := []any{limit}

	switch kind {
	case domain.BoardSkill:
		if skill == nil {
			return nil, fmt.Errorf("kind=skill requires a skill: %w", port.ErrConflict)
		}
		args = append(args, string(*skill))
		query = `
			SELECT rank() OVER (ORDER BY us.score DESC NULLS LAST, u.display_name),
			       u.id, u.display_name, coalesce(gi.github_login, ''), us.score,
			       (a.expires_at IS NOT NULL AND a.expires_at > now())
			FROM user_skills us
			JOIN users u ON u.id = us.user_id
			JOIN user_availability a ON a.user_id = u.id
			LEFT JOIN user_github_identities gi ON gi.user_id = u.id
			WHERE us.skill_id = $2 AND us.standing = 'primary'
			  AND u.deactivated_at IS NULL AND a.status <> 'not_looking'
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
			       (a.expires_at IS NOT NULL AND a.expires_at > now())
			FROM users u
			JOIN user_availability a ON a.user_id = u.id
			LEFT JOIN user_github_identities gi ON gi.user_id = u.id
			WHERE u.deactivated_at IS NULL AND a.status <> 'not_looking'
			  AND ` + column + ` IS NOT NULL
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
		       (a.expires_at IS NOT NULL AND a.expires_at > now()),
		       a.status, a.expires_at, a.updated_at,
		       u.overall_score, u.generalist_score
		FROM users u
		JOIN user_availability a ON a.user_id = u.id
		LEFT JOIN user_github_identities gi ON gi.user_id = u.id
		WHERE u.id = $1
		  AND u.deactivated_at IS NULL
		  AND a.status <> 'not_looking'
		  AND NOT EXISTS (
		          SELECT 1
		          FROM user_github_identities hi
		          JOIN user_github_identities ui ON ui.github_user_id = hi.github_user_id
		          WHERE hi.hirer_account_id = $2 AND ui.user_id = u.id
		      )`, string(target), string(caller),
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
			days := int(nowFrom(ctx).Sub(*expiresAt).Hours() / 24)
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
	return &card, translate(rows.Err(), "reading scorecard")
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
	rank := domain.Rank{RubricVersion: "v1", Ranked: true}

	var status string
	var expiresAt *time.Time
	// The SCORES come from users, the RANKS from the ranked population.
	//
	// Reading both through `ranked` would lose an opted-out contributor's
	// score along with their position, and the score is theirs: opting out
	// hides you, it does not unmake what your evidence was worth.
	err := r.db.pool.QueryRow(ctx, `
		WITH`+rankedPopulation+`
		SELECT u.overall_score, r.overall_rank, (SELECT count(*) FROM ranked),
		       u.generalist_score, r.generalist_rank,
		       a.status, a.expires_at
		FROM users u
		JOIN user_availability a ON a.user_id = u.id
		LEFT JOIN ranked r ON r.user_id = u.id
		WHERE u.id = $1`, string(id),
	).Scan(&rank.Overall.Score, &rank.Overall.Rank, &rank.Overall.OutOf,
		&rank.Generalist.Score, &rank.Generalist.Rank, &status, &expiresAt)
	if err != nil {
		return nil, translate(err, fmt.Sprintf("rank for %s", id))
	}
	rank.Generalist.OutOf = rank.Overall.OutOf
	rank.Active = expiresAt != nil && expiresAt.After(nowFrom(ctx))

	if domain.AvailabilityStatus(status) == domain.NotLooking {
		rank.Ranked = false
		rank.UnrankedReason = "opted_out"
		rank.Overall.Rank, rank.Generalist.Rank = nil, nil
	} else if rank.Overall.Rank == nil {
		// No primary skill, so no user-level score and no position. Null is
		// not zero: zero would claim we measured something (ADR-0007).
		rank.Ranked = false
		rank.UnrankedReason = "no_primary_skill"
	}

	rows, err := r.db.pool.Query(ctx, `
		SELECT s.slug, s.name, us.standing, coalesce(us.score, 0),
		       CASE WHEN us.standing = 'primary' THEN
		           (SELECT count(*) + 1 FROM user_skills peer
		            WHERE peer.skill_id = us.skill_id AND peer.standing = 'primary'
		              AND peer.score > us.score)
		       END,
		       CASE WHEN us.standing = 'primary' THEN
		           (SELECT count(*) FROM user_skills peer
		            WHERE peer.skill_id = us.skill_id AND peer.standing = 'primary')
		       END
		FROM user_skills us
		JOIN skills s ON s.id = us.skill_id
		WHERE us.user_id = $1
		ORDER BY us.score DESC NULLS LAST, s.slug`, string(id))
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
		// contradict the standing rule.
		if sr.Standing != domain.Primary {
			sr.Rank, sr.OutOf = nil, nil
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
