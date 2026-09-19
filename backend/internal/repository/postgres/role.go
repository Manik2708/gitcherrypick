package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

// RoleRepository owns openings (ADR-0019).
//
// The property this file exists to hold is that an OPEN ROLE IS NEVER UPDATED.
// A change writes a new row pointing at the old one and closes it, so a
// contributor contacted last week keeps reading the salary they agreed to talk
// about. Guarding an UPDATE would have got that right only for as long as
// everybody remembered the guard; a row nobody updates gets it right
// permanently, which is why UpdateDraft refuses anything that is not a draft.
type RoleRepository struct{ db *DB }

// Roles returns the openings repository.
func (db *DB) Roles() *RoleRepository { return &RoleRepository{db: db} }

var _ port.RoleRepository = (*RoleRepository)(nil)

// roleColumns is every column, in the order scanRole reads them.
const roleColumns = `
	r.id, r.organization_id, r.title, coalesce(r.description, ''),
	r.engagement, r.status, r.location, r.address_id,
	coalesce(r.currency, ''), r.yearly_ctc, r.yearly_base,
	r.hourly_rate, r.expected_hours,
	r.min_office_yoe, r.min_oss_yoe,
	r.requires_online_test, r.max_interview_rounds, r.avg_days_to_offer,
	r.created_by, r.opened_by, r.opened_at,
	r.close_requested_by, r.close_requested_at,
	r.closed_at, r.closed_by, r.close_reason, coalesce(r.close_note, ''),
	r.supersedes_id, r.created_at, r.updated_at,
	EXISTS (SELECT 1 FROM role_openings o
	         WHERE o.role_id = r.id
	           AND o.published_at IS NOT NULL
	           AND o.withdrawn_at IS NULL)`

// scanRole reads one row. The country list and the questions are separate
// reads, because a join against two child tables would multiply the role row
// and make every scalar arrive several times.
func scanRole(row interface{ Scan(...any) error }) (*domain.Role, error) {
	var (
		out       domain.Role
		orgID     string
		id        string
		createdBy string
		addressID *string
		openedBy  *string
		closeReqBy,
		closedBy *string
		reason     *string
		supersedes *string
		engagement string
		status     string
		location   string
	)
	err := row.Scan(&id, &orgID, &out.Title, &out.Description,
		&engagement, &status, &location, &addressID,
		&out.Currency, &out.YearlyCTC, &out.YearlyBase,
		&out.HourlyRate, &out.ExpectedHours,
		&out.MinOfficeYOE, &out.MinOSSYOE,
		&out.RequiresOnlineTest, &out.MaxInterviewRounds, &out.AvgDaysToOffer,
		&createdBy, &openedBy, &out.OpenedAt,
		&closeReqBy, &out.CloseRequestedAt,
		&out.ClosedAt, &closedBy, &reason, &out.CloseNote,
		&supersedes, &out.CreatedAt, &out.UpdatedAt, &out.Advertised)
	if err != nil {
		return nil, err
	}

	out.ID = domain.RoleID(id)
	out.OrgID = domain.OrganizationID(orgID)
	out.Engagement = domain.Engagement(engagement)
	out.Status = domain.RoleStatus(status)
	out.Location = domain.RoleLocation(location)
	out.CreatedBy = domain.HirerID(createdBy)

	if addressID != nil {
		a := domain.AddressID(*addressID)
		out.AddressID = &a
	}
	if openedBy != nil {
		h := domain.HirerID(*openedBy)
		out.OpenedBy = &h
	}
	if closeReqBy != nil {
		h := domain.HirerID(*closeReqBy)
		out.CloseRequestedBy = &h
	}
	if closedBy != nil {
		h := domain.HirerID(*closedBy)
		out.ClosedBy = &h
	}
	if reason != nil {
		c := domain.CloseReason(*reason)
		out.CloseReason = &c
	}
	if supersedes != nil {
		s := domain.RoleID(*supersedes)
		out.Supersedes = &s
	}
	return &out, nil
}

// ByID reads a role with its countries, questions and hires.
func (r *RoleRepository) ByID(ctx context.Context, id domain.RoleID) (*domain.Role, error) {
	return r.byID(ctx, nil, id)
}

// byID reads a role INSIDE the caller's transaction when there is one.
//
// Every write here reads back what it wrote, and a read-back on the pool would
// not see it: the row is uncommitted, so the write would report "not found" for
// something it had just created. The public ByID passes nil and reads
// committed state, which is what an ordinary caller wants.
func (r *RoleRepository) byID(ctx context.Context, t port.Tx, id domain.RoleID) (*domain.Role, error) {
	role, err := scanRole(r.db.q(t).QueryRow(ctx,
		`SELECT`+roleColumns+` FROM roles r WHERE r.id = $1`, string(id)))
	if err != nil {
		return nil, translate(err, fmt.Sprintf("reading role %s", id))
	}
	if err := r.hydrate(ctx, t, []*domain.Role{role}); err != nil {
		return nil, err
	}
	return role, nil
}

// ListByOrganization returns every role, newest first.
//
// Superseded rows are included deliberately. A caller wanting the live list
// filters on status: it is the NEWEST row that is live, so filtering on
// `supersedes_id IS NULL` — the filter that looks right — would return exactly
// the dead ones.
func (r *RoleRepository) ListByOrganization(ctx context.Context, id domain.OrganizationID, status *domain.RoleStatus) ([]domain.Role, error) {
	rows, err := r.db.pool.Query(ctx, `
		SELECT`+roleColumns+`
		  FROM roles r
		 WHERE r.organization_id = $1
		   AND ($2::role_status IS NULL OR r.status = $2::role_status)
		 ORDER BY r.created_at DESC`,
		string(id), statusArg(status))
	if err != nil {
		return nil, translate(err, fmt.Sprintf("listing roles for organization %s", id))
	}
	defer rows.Close()
	return r.collect(ctx, nil, rows, "listing roles")
}

// statusArg passes a nullable enum without a driver-level nil interface.
func statusArg(s *domain.RoleStatus) *string {
	if s == nil {
		return nil
	}
	v := string(*s)
	return &v
}

// Create writes a DRAFT.
//
// A role is never born open. Opening is a separate act performed by whoever the
// organisation's authority setting says may perform it, and collapsing the two
// would make the setting unenforceable for anybody who creates and opens in one
// request.
func (r *RoleRepository) Create(ctx context.Context, t port.Tx, role *domain.Role) (*domain.Role, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generating role id: %w", err)
	}
	role.ID = domain.RoleID(id.String())

	if err := r.insert(ctx, t, role, nil); err != nil {
		return nil, err
	}
	return r.byID(ctx, t, role.ID)
}

// insert writes the role row and its two child lists.
func (r *RoleRepository) insert(ctx context.Context, t port.Tx, role *domain.Role, supersedes *domain.RoleID) error {
	q := r.db.q(t)

	var address *string
	if role.AddressID != nil {
		a := string(*role.AddressID)
		address = &a
	}
	var prior *string
	if supersedes != nil {
		p := string(*supersedes)
		prior = &p
	}

	_, err := q.Exec(ctx, `
		INSERT INTO roles
		    (id, organization_id, title, description, engagement, status,
		     location, address_id, currency, yearly_ctc, yearly_base,
		     hourly_rate, expected_hours, min_office_yoe, min_oss_yoe,
		     requires_online_test, max_interview_rounds, avg_days_to_offer,
		     created_by, supersedes_id)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5::role_engagement, 'draft',
		        $6::role_location, $7, NULLIF($8, ''), $9, $10,
		        $11, $12, $13, $14, $15, $16, $17, $18, $19)`,
		string(role.ID), string(role.OrgID), role.Title, role.Description,
		string(role.Engagement), string(role.Location), address,
		role.Currency, role.YearlyCTC, role.YearlyBase,
		role.HourlyRate, role.ExpectedHours, role.MinOfficeYOE, role.MinOSSYOE,
		role.RequiresOnlineTest, role.MaxInterviewRounds, role.AvgDaysToOffer,
		string(role.CreatedBy), prior)
	if err != nil {
		return translate(err, "creating role")
	}
	return r.writeChildren(ctx, t, role)
}

// writeChildren replaces the countries and questions attached to a role.
func (r *RoleRepository) writeChildren(ctx context.Context, t port.Tx, role *domain.Role) error {
	q := r.db.q(t)

	if _, err := q.Exec(ctx,
		`DELETE FROM role_eligible_countries WHERE role_id = $1`, string(role.ID)); err != nil {
		return translate(err, "clearing eligible countries")
	}
	for _, c := range role.EligibleCountries {
		if _, err := q.Exec(ctx,
			`INSERT INTO role_eligible_countries (role_id, country) VALUES ($1, $2)
			 ON CONFLICT DO NOTHING`, string(role.ID), c); err != nil {
			return translate(err, "writing an eligible country")
		}
	}

	if _, err := q.Exec(ctx,
		`DELETE FROM role_questions WHERE role_id = $1`, string(role.ID)); err != nil {
		return translate(err, "clearing role questions")
	}
	for i := range role.Questions {
		qid, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("generating question id: %w", err)
		}
		role.Questions[i].ID = qid.String()
		role.Questions[i].Position = i
		if _, err := q.Exec(ctx, `
			INSERT INTO role_questions (id, role_id, question, position, expected)
			VALUES ($1, $2, $3, $4, $5)`,
			qid.String(), string(role.ID), role.Questions[i].Question, i,
			role.Questions[i].Expected); err != nil {
			return translate(err, "writing a role question")
		}
	}
	return nil
}

// UpdateDraft replaces a draft's contents.
//
// DRAFTS ONLY. The WHERE clause carries the rule rather than a check above it:
// an open role that reached here would be edited under everybody who had
// already been shown it, and the one place that cannot be forgotten is the
// statement itself.
func (r *RoleRepository) UpdateDraft(ctx context.Context, t port.Tx, role *domain.Role) (*domain.Role, error) {
	var address *string
	if role.AddressID != nil {
		a := string(*role.AddressID)
		address = &a
	}

	tag, err := r.db.q(t).Exec(ctx, `
		UPDATE roles
		   SET title = $2, description = NULLIF($3, ''),
		       engagement = $4::role_engagement, location = $5::role_location,
		       address_id = $6, currency = NULLIF($7, ''),
		       yearly_ctc = $8, yearly_base = $9,
		       hourly_rate = $10, expected_hours = $11,
		       min_office_yoe = $12, min_oss_yoe = $13,
		       requires_online_test = $14, max_interview_rounds = $15,
		       avg_days_to_offer = $16
		 WHERE id = $1 AND status = 'draft'`,
		string(role.ID), role.Title, role.Description,
		string(role.Engagement), string(role.Location), address, role.Currency,
		role.YearlyCTC, role.YearlyBase, role.HourlyRate, role.ExpectedHours,
		role.MinOfficeYOE, role.MinOSSYOE,
		role.RequiresOnlineTest, role.MaxInterviewRounds, role.AvgDaysToOffer)
	if err != nil {
		return nil, translate(err, fmt.Sprintf("updating role %s", role.ID))
	}
	if tag.RowsAffected() == 0 {
		// Either it is gone or it is no longer a draft. Conflict rather than
		// not-found: the caller is holding a role they just read, and telling
		// them it does not exist would send them looking for the wrong bug.
		return nil, fmt.Errorf("role %s is not a draft: %w", role.ID, port.ErrConflict)
	}
	if err := r.writeChildren(ctx, t, role); err != nil {
		return nil, err
	}
	return r.byID(ctx, t, role.ID)
}

// Open signs the numbers and starts matching.
func (r *RoleRepository) Open(ctx context.Context, t port.Tx, id domain.RoleID, by domain.HirerID, at time.Time) (*domain.Role, error) {
	tag, err := r.db.q(t).Exec(ctx, `
		UPDATE roles
		   SET status = 'open', opened_by = $2, opened_at = $3,
		       closed_at = NULL, closed_by = NULL,
		       close_reason = NULL, close_note = NULL,
		       close_requested_by = NULL, close_requested_at = NULL
		 WHERE id = $1 AND status <> 'open'`,
		string(id), string(by), at)
	if err != nil {
		return nil, translate(err, fmt.Sprintf("opening role %s", id))
	}
	if tag.RowsAffected() == 0 {
		return nil, fmt.Errorf("role %s is already open: %w", id, port.ErrConflict)
	}
	return r.byID(ctx, t, id)
}

// Revise writes a successor and closes the original, in one transaction.
//
// One call because the two halves cannot come apart. A successor without the
// close leaves two open roles for one job; a close without the successor
// withdraws a live opening and tells nobody why.
//
// The successor is a DRAFT. Opening it is the commitment, governed by the
// organisation's update authority, and a revision that opened itself would
// route around the owner the setting exists to involve.
func (r *RoleRepository) Revise(ctx context.Context, t port.Tx, of domain.RoleID, next *domain.Role, by domain.HirerID, at time.Time) (*domain.Role, error) {
	// SELECT FOR UPDATE first: two hirers revising the same role would
	// otherwise both pass the status check and leave two successors, which the
	// unique index on supersedes_id would reject as a bare constraint error
	// rather than as the conflict it is.
	var status string
	if err := r.db.q(t).QueryRow(ctx,
		`SELECT status FROM roles WHERE id = $1 FOR UPDATE`, string(of)).Scan(&status); err != nil {
		return nil, translate(err, fmt.Sprintf("locking role %s", of))
	}
	if domain.RoleStatus(status) != domain.RoleOpen {
		return nil, fmt.Errorf("role %s is not open, so there is nothing to revise: %w",
			of, port.ErrConflict)
	}

	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generating role id: %w", err)
	}
	next.ID = domain.RoleID(id.String())
	if err := r.insert(ctx, t, next, &of); err != nil {
		return nil, err
	}

	// Closing the superseded role does NOT consult role_close_authority: it is
	// one act, authorised once, under the update setting. Requiring a second
	// approval to retire a row that has just been replaced would leave two open
	// roles for one job while somebody went looking for an owner.
	if _, err := r.db.q(t).Exec(ctx, `
		UPDATE roles
		   SET status = 'closed', closed_at = $2, closed_by = $3,
		       close_reason = 'superseded'
		 WHERE id = $1`, string(of), at, string(by)); err != nil {
		return nil, translate(err, fmt.Sprintf("closing superseded role %s", of))
	}
	return r.byID(ctx, t, next.ID)
}

// RequestClose records that somebody asked, and changes nothing else.
//
// The role STAYS OPEN. It matches, it can be shortlisted against, and the
// promise it makes stands until an owner actually withdraws it — a request is
// not an outcome, and a status would make it look like one.
func (r *RoleRepository) RequestClose(ctx context.Context, t port.Tx, id domain.RoleID, by domain.HirerID, at time.Time) (*domain.Role, error) {
	tag, err := r.db.q(t).Exec(ctx, `
		UPDATE roles
		   SET close_requested_by = $2, close_requested_at = $3
		 WHERE id = $1 AND status = 'open'`,
		string(id), string(by), at)
	if err != nil {
		return nil, translate(err, fmt.Sprintf("requesting closure of role %s", id))
	}
	if tag.RowsAffected() == 0 {
		return nil, fmt.Errorf("role %s is not open: %w", id, port.ErrConflict)
	}
	return r.byID(ctx, t, id)
}

// Close ends a role, recording why and — for hired_via_platform — who.
//
// The hires are written HERE rather than by a second call. The invariant that
// reason has at least one hire and no other reason has any spans two tables, so
// it cannot be a CHECK; one call makes it one transaction, which is the only
// place it can be held at all.
func (r *RoleRepository) Close(ctx context.Context, t port.Tx, id domain.RoleID, c port.RoleClosure) (*domain.Role, error) {
	tag, err := r.db.q(t).Exec(ctx, `
		UPDATE roles
		   SET status = 'closed', closed_at = $2, closed_by = $3,
		       close_reason = $4::role_close_reason, close_note = NULLIF($5, '')
		 WHERE id = $1 AND status = 'open'`,
		string(id), c.At, string(c.By), string(c.Reason), c.Note)
	if err != nil {
		return nil, translate(err, fmt.Sprintf("closing role %s", id))
	}
	if tag.RowsAffected() == 0 {
		return nil, fmt.Errorf("role %s is not open: %w", id, port.ErrConflict)
	}

	for _, user := range c.Hires {
		if _, err := r.db.q(t).Exec(ctx, `
			INSERT INTO role_hires (role_id, user_id, recorded_by, recorded_at)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (role_id, user_id) DO NOTHING`,
			string(id), string(user), string(c.By), c.At); err != nil {
			return nil, translate(err, "recording a hire")
		}
	}
	return r.byID(ctx, t, id)
}

// HiresOf reports who a role was filled with.
func (r *RoleRepository) HiresOf(ctx context.Context, id domain.RoleID) ([]domain.RoleHire, error) {
	rows, err := r.db.pool.Query(ctx, `
		SELECT role_id, user_id, recorded_by, recorded_at
		  FROM role_hires WHERE role_id = $1 ORDER BY recorded_at`, string(id))
	if err != nil {
		return nil, translate(err, fmt.Sprintf("reading hires for role %s", id))
	}
	defer rows.Close()

	out := []domain.RoleHire{}
	for rows.Next() {
		var h domain.RoleHire
		var role, user, by string
		if err := rows.Scan(&role, &user, &by, &h.RecordedAt); err != nil {
			return nil, translate(err, "scanning a hire")
		}
		h.RoleID, h.UserID, h.RecordedBy = domain.RoleID(role), domain.UserID(user), domain.HirerID(by)
		out = append(out, h)
	}
	return out, translate(rows.Err(), "reading hires")
}

// Matching returns open roles a contributor could be approached for.
//
// Every predicate FAILS OPEN except the two that must not. A role with no
// stated countries admits everybody; a contributor who has not said where they
// are is admitted by any role that hires anywhere; an unstated office-years
// minimum admits everybody. The exceptions are OSS years, which is verified and
// therefore excluded when unknown (ADR-0019 §When the fetch fails), and the
// shapes, which are what the contributor actually asked for.
func (r *RoleRepository) Matching(ctx context.Context, m port.RoleMatch) ([]domain.Role, error) {
	if len(m.Shapes) == 0 {
		// Nothing ticked matches nothing. Returning everything here would show
		// somebody roles they specifically did not ask for.
		return []domain.Role{}, nil
	}

	shapes := make([]string, 0, len(m.Shapes))
	for _, s := range m.Shapes {
		shapes = append(shapes, string(s))
	}

	limit := m.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	rows, err := r.db.pool.Query(ctx, `
		SELECT`+roleColumns+`
		  FROM roles r
		 WHERE r.status = 'open'
		   AND r.engagement::text = ANY($1::text[])

		   -- No stated countries means anywhere; an unstated country is not a
		   -- reason to hide a role that hires anywhere.
		   AND (NOT EXISTS (SELECT 1 FROM role_eligible_countries c WHERE c.role_id = r.id)
		        OR ($2 <> '' AND EXISTS (
		            SELECT 1 FROM role_eligible_countries c
		             WHERE c.role_id = r.id AND c.country = $2)))

		   -- Self-reported either way, so an unstated figure clears a minimum.
		   AND (r.min_office_yoe IS NULL OR $3::int IS NULL OR $3::int >= r.min_office_yoe)

		   -- VERIFIED, so an unknown figure does NOT clear a minimum.
		   AND (r.min_oss_yoe IS NULL OR ($4::int IS NOT NULL AND $4::int >= r.min_oss_yoe))

		   -- Pay filters the CONTRIBUTOR'S view and travels no further
		   -- (ADR-0018 §5). A role in another currency is not compared, because
		   -- comparing it would need a rate nobody here has.
		   AND ($5 = '' OR r.currency IS NULL OR r.currency <> $5
		        OR (($6::bigint IS NULL OR r.yearly_ctc IS NULL OR r.yearly_ctc >= $6::bigint)
		        AND ($7::bigint IS NULL OR r.hourly_rate IS NULL OR r.hourly_rate >= $7::bigint)))

		 ORDER BY r.opened_at DESC NULLS LAST, r.created_at DESC
		 LIMIT $8 OFFSET $9`,
		shapes, m.Country, m.OfficeYOE, m.OSSYears,
		m.Currency, m.MinYearly, m.MinHourly, limit, max(m.Offset, 0))
	if err != nil {
		return nil, translate(err, "matching roles")
	}
	defer rows.Close()
	return r.collect(ctx, nil, rows, "matching roles")
}

// collect scans a role result set and hydrates the child lists.
func (r *RoleRepository) collect(ctx context.Context, t port.Tx, rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}, what string) ([]domain.Role, error) {
	out := []domain.Role{}
	refs := []*domain.Role{}
	for rows.Next() {
		role, err := scanRole(rows)
		if err != nil {
			return nil, translate(err, "scanning a role")
		}
		out = append(out, *role)
	}
	if err := rows.Err(); err != nil {
		return nil, translate(err, what)
	}
	for i := range out {
		refs = append(refs, &out[i])
	}
	if err := r.hydrate(ctx, t, refs); err != nil {
		return nil, err
	}
	return out, nil
}

// hydrate fills in the countries, questions and hires for a set of roles.
//
// One query per child table for the whole set, not one per role: a list of
// fifty roles would otherwise be a hundred and fifty round trips.
func (r *RoleRepository) hydrate(ctx context.Context, t port.Tx, roles []*domain.Role) error {
	if len(roles) == 0 {
		return nil
	}

	byID := make(map[domain.RoleID]*domain.Role, len(roles))
	ids := make([]string, 0, len(roles))
	for _, role := range roles {
		byID[role.ID] = role
		ids = append(ids, string(role.ID))
		role.EligibleCountries = []string{}
		role.Questions = []domain.RoleQuestion{}
		role.Hires = []domain.RoleHire{}
	}

	countries, err := r.db.q(t).Query(ctx, `
		SELECT role_id, country FROM role_eligible_countries
		 WHERE role_id = ANY($1::uuid[]) ORDER BY country`, ids)
	if err != nil {
		return translate(err, "reading eligible countries")
	}
	for countries.Next() {
		var id, c string
		if err := countries.Scan(&id, &c); err != nil {
			countries.Close()
			return translate(err, "scanning an eligible country")
		}
		if role := byID[domain.RoleID(id)]; role != nil {
			role.EligibleCountries = append(role.EligibleCountries, c)
		}
	}
	countries.Close()
	if err := countries.Err(); err != nil {
		return translate(err, "reading eligible countries")
	}

	questions, err := r.db.q(t).Query(ctx, `
		SELECT id, role_id, question, position, expected FROM role_questions
		 WHERE role_id = ANY($1::uuid[]) ORDER BY role_id, position`, ids)
	if err != nil {
		return translate(err, "reading role questions")
	}
	for questions.Next() {
		var q domain.RoleQuestion
		var id string
		if err := questions.Scan(&q.ID, &id, &q.Question, &q.Position, &q.Expected); err != nil {
			questions.Close()
			return translate(err, "scanning a role question")
		}
		q.RoleID = domain.RoleID(id)
		if role := byID[q.RoleID]; role != nil {
			role.Questions = append(role.Questions, q)
		}
	}
	questions.Close()
	if err := questions.Err(); err != nil {
		return translate(err, "reading role questions")
	}

	hires, err := r.db.q(t).Query(ctx, `
		SELECT role_id, user_id, recorded_by, recorded_at FROM role_hires
		 WHERE role_id = ANY($1::uuid[]) ORDER BY recorded_at`, ids)
	if err != nil {
		return translate(err, "reading role hires")
	}
	for hires.Next() {
		var h domain.RoleHire
		var id, user, by string
		if err := hires.Scan(&id, &user, &by, &h.RecordedAt); err != nil {
			hires.Close()
			return translate(err, "scanning a role hire")
		}
		h.RoleID, h.UserID, h.RecordedBy = domain.RoleID(id), domain.UserID(user), domain.HirerID(by)
		if role := byID[h.RoleID]; role != nil {
			role.Hires = append(role.Hires, h)
		}
	}
	hires.Close()
	return translate(hires.Err(), "reading role hires")
}

// OrgSettingsRepository owns an organisation's policy (ADR-0019 §11).
type OrgSettingsRepository struct{ db *DB }

// OrgSettings returns the policy repository.
func (db *DB) OrgSettings() *OrgSettingsRepository { return &OrgSettingsRepository{db: db} }

var _ port.OrgSettingsRepository = (*OrgSettingsRepository)(nil)

// Settings returns the stored row, or the DEFAULTS.
//
// Absence is not an error. An organisation that never opens the settings screen
// behaves exactly like one that opened it and changed nothing, which is what
// makes the table need no backfill.
func (r *OrgSettingsRepository) Settings(ctx context.Context, id domain.OrganizationID) (*domain.OrgSettings, error) {
	out := domain.DefaultOrgSettings(id)
	var create, update, closing string

	err := r.db.pool.QueryRow(ctx, `
		SELECT role_create_authority, role_update_authority, role_close_authority
		  FROM organization_settings WHERE organization_id = $1`, string(id),
	).Scan(&create, &update, &closing)
	if err != nil {
		if isNotFound(err) {
			return &out, nil
		}
		return nil, translate(err, fmt.Sprintf("reading settings for organization %s", id))
	}

	out.RoleCreateAuthority = domain.RoleAuthority(create)
	out.RoleUpdateAuthority = domain.RoleAuthority(update)
	out.RoleCloseAuthority = domain.RoleAuthority(closing)
	return &out, nil
}

// Save replaces the policy.
func (r *OrgSettingsRepository) Save(ctx context.Context, t port.Tx, s *domain.OrgSettings) error {
	_, err := r.db.q(t).Exec(ctx, `
		INSERT INTO organization_settings
		    (organization_id, role_create_authority, role_update_authority, role_close_authority)
		VALUES ($1, $2::role_authority, $3::role_authority, $4::role_authority)
		ON CONFLICT (organization_id) DO UPDATE SET
		    role_create_authority = excluded.role_create_authority,
		    role_update_authority = excluded.role_update_authority,
		    role_close_authority  = excluded.role_close_authority`,
		string(s.OrgID), string(s.RoleCreateAuthority),
		string(s.RoleUpdateAuthority), string(s.RoleCloseAuthority))
	return translate(err, fmt.Sprintf("saving settings for organization %s", s.OrgID))
}

// Candidates lists everybody on a round for this role.
//
// DISTINCT ON the person, not the entry: the same contributor staged on two
// rounds for one job has been approached once as far as a hirer is concerned,
// and showing them twice would make a list of six people read as a list of
// nine approaches.
//
// The earliest entry wins the tie, because that is the one that reached them —
// and its contact request is the one that carries their answer.
func (r *RoleRepository) Candidates(ctx context.Context, id domain.RoleID) ([]domain.RoleCandidate, error) {
	rows, err := r.db.pool.Query(ctx, `
		SELECT DISTINCT ON (e.user_id)
		       e.user_id, u.display_name, coalesce(gi.github_login, ''),
		       s.id, s.name,
		       coalesce(cr.status::text, ''), e.notified_at
		  FROM shortlist_entries e
		  JOIN shortlists s ON s.id = e.shortlist_id
		  JOIN users u ON u.id = e.user_id
		  LEFT JOIN user_github_identities gi ON gi.user_id = e.user_id
		  -- The request raised for this person ON THIS ROUND, which is where
		  -- their answer lives. A left join because a staged entry has none.
		  LEFT JOIN contact_requests cr
		         ON cr.shortlist_id = e.shortlist_id AND cr.user_id = e.user_id
		 WHERE s.role_id = $1
		 ORDER BY e.user_id, e.added_at`, string(id))
	if err != nil {
		return nil, translate(err, fmt.Sprintf("listing candidates for role %s", id))
	}
	defer rows.Close()

	out := []domain.RoleCandidate{}
	for rows.Next() {
		var (
			c                 domain.RoleCandidate
			user, shortlistID string
			status            string
		)
		if err := rows.Scan(&user, &c.DisplayName, &c.GitHubLogin,
			&shortlistID, &c.ShortlistName, &status, &c.NotifiedAt); err != nil {
			return nil, translate(err, "scanning a candidate")
		}
		c.UserID = domain.UserID(user)
		c.ShortlistID = domain.ShortlistID(shortlistID)
		c.ContactStatus = domain.ContactRequestStatus(status)
		c.Accepted = c.ContactStatus == domain.ContactAccepted
		out = append(out, c)
	}
	return out, translate(rows.Err(), "listing candidates")
}
