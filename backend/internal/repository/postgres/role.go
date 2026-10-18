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
	r.engagement, r.status,
	coalesce(r.currency, ''), r.yearly_ctc, r.yearly_base,
	r.hourly_rate, r.expected_hours,
	r.min_office_yoe, r.min_oss_yoe,
	r.requires_online_test, r.max_interview_rounds, r.avg_days_to_offer,
	r.created_by, r.opened_by, r.opened_at,
	r.close_requested_by, r.close_requested_at,
	r.closed_at, r.closed_by, r.close_reason, coalesce(r.close_note, ''),
	r.supersedes_id, r.created_at, r.updated_at, r.first_contacted_at,
	EXISTS (SELECT 1 FROM role_openings o
	         WHERE o.role_id = r.id
	           AND o.published_at IS NOT NULL
	           AND o.withdrawn_at IS NULL)`

// scanRole reads one row. The locations and the questions are separate
// reads, because a join against two child tables would multiply the role row
// and make every scalar arrive several times.
func scanRole(row interface{ Scan(...any) error }) (*domain.Role, error) {
	var (
		out       domain.Role
		orgID     string
		id        string
		createdBy string
		openedBy  *string
		closeReqBy,
		closedBy *string
		reason     *string
		supersedes *string
		engagement string
		status     string
	)
	err := row.Scan(&id, &orgID, &out.Title, &out.Description,
		&engagement, &status,
		&out.Currency, &out.YearlyCTC, &out.YearlyBase,
		&out.HourlyRate, &out.ExpectedHours,
		&out.MinOfficeYOE, &out.MinOSSYOE,
		&out.RequiresOnlineTest, &out.MaxInterviewRounds, &out.AvgDaysToOffer,
		&createdBy, &openedBy, &out.OpenedAt,
		&closeReqBy, &out.CloseRequestedAt,
		&out.ClosedAt, &closedBy, &reason, &out.CloseNote,
		&supersedes, &out.CreatedAt, &out.UpdatedAt, &out.FirstContactedAt,
		&out.Advertised)
	if err != nil {
		return nil, err
	}

	out.ID = domain.RoleID(id)
	out.OrgID = domain.OrganizationID(orgID)
	out.Engagement = domain.Engagement(engagement)
	out.Status = domain.RoleStatus(status)
	out.CreatedBy = domain.HirerID(createdBy)

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

// ByID reads a role with its locations, questions and hires.
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

// insert writes the role row and its child lists.
func (r *RoleRepository) insert(ctx context.Context, t port.Tx, role *domain.Role, supersedes *domain.RoleID) error {
	q := r.db.q(t)

	var prior *string
	if supersedes != nil {
		p := string(*supersedes)
		prior = &p
	}

	_, err := q.Exec(ctx, `
		INSERT INTO roles
		    (id, organization_id, title, description, engagement, status,
		     currency, yearly_ctc, yearly_base,
		     hourly_rate, expected_hours, min_office_yoe, min_oss_yoe,
		     requires_online_test, max_interview_rounds, avg_days_to_offer,
		     created_by, supersedes_id)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5::role_engagement, 'draft',
		        NULLIF($6, ''), $7, $8,
		        $9, $10, $11, $12, $13, $14, $15, $16, $17)`,
		string(role.ID), string(role.OrgID), role.Title, role.Description,
		string(role.Engagement),
		role.Currency, role.YearlyCTC, role.YearlyBase,
		role.HourlyRate, role.ExpectedHours, role.MinOfficeYOE, role.MinOSSYOE,
		role.RequiresOnlineTest, role.MaxInterviewRounds, role.AvgDaysToOffer,
		string(role.CreatedBy), prior)
	if err != nil {
		return translate(err, "creating role")
	}
	return r.writeChildren(ctx, t, role)
}

// writeChildren replaces the location entries and questions of a role.
//
// Entries are written here and nowhere else, so they share the role's
// immutability: create, a draft's update and a revision's successor are the
// only callers (ADR-0024 decision 4).
func (r *RoleRepository) writeChildren(ctx context.Context, t port.Tx, role *domain.Role) error {
	q := r.db.q(t)

	if _, err := q.Exec(ctx,
		`DELETE FROM role_locations WHERE role_id = $1`, string(role.ID)); err != nil {
		return translate(err, "clearing role locations")
	}
	for _, l := range role.Locations {
		var country, address *string
		if l.Country != "" {
			c := l.Country
			country = &c
		}
		if l.AddressID != nil {
			a := string(*l.AddressID)
			address = &a
		}
		// ON CONFLICT: a repeated entry is collapsed, not refused (ADR-0024
		// decision 3). The service collapses first; this is the backstop.
		if _, err := q.Exec(ctx, `
			INSERT INTO role_locations (role_id, mode, country, address_id)
			VALUES ($1, $2::role_location_mode, $3, $4)
			ON CONFLICT DO NOTHING`,
			string(role.ID), string(l.Mode), country, address); err != nil {
			return translate(err, "writing a role location")
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
	tag, err := r.db.q(t).Exec(ctx, `
		UPDATE roles
		   SET title = $2, description = NULLIF($3, ''),
		       engagement = $4::role_engagement, currency = NULLIF($5, ''),
		       yearly_ctc = $6, yearly_base = $7,
		       hourly_rate = $8, expected_hours = $9,
		       min_office_yoe = $10, min_oss_yoe = $11,
		       requires_online_test = $12, max_interview_rounds = $13,
		       avg_days_to_offer = $14
		 WHERE id = $1 AND status = 'draft'`,
		string(role.ID), role.Title, role.Description,
		string(role.Engagement), role.Currency,
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

// hydrate fills in the locations, questions and hires for a set of roles.
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
		role.Locations = []domain.RoleLocation{}
		role.Questions = []domain.RoleQuestion{}
		role.Hires = []domain.RoleHire{}
	}

	if err := r.hydrateLocations(ctx, t, byID, ids); err != nil {
		return err
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

// Candidates lists everybody on this role.
//
// One row per person by construction now (ADR-0023): the primary key is
// (role_id, user_id), so the duplicate a round-per-role model could produce —
// the same contributor staged on two rounds for one job — cannot exist.
func (r *RoleRepository) Candidates(ctx context.Context, id domain.RoleID) ([]domain.RoleCandidate, error) {
	rows, err := r.db.pool.Query(ctx, `
		SELECT c.user_id, u.display_name, coalesce(gi.github_login, ''),
		       coalesce(c.note, ''), `+hirerRefColumns("author")+`,
		       c.added_at,
		       coalesce(cr.status::text, ''), c.notified_at,
		       -- The address comes off email_released_at, which is stamped
		       -- when the contributor accepts and never by a decline or an
		       -- expiry.
		       CASE WHEN cr.email_released_at IS NOT NULL THEN coalesce(u.email, '') ELSE '' END
		  FROM role_candidates c
		  JOIN users u ON u.id = c.user_id
		  JOIN hirer_accounts author ON author.id = c.added_by
		  LEFT JOIN user_github_identities gi ON gi.user_id = c.user_id
		  -- Their answer, if they have been asked. A left join because a
		  -- staged candidate has no request.
		  LEFT JOIN contact_requests cr
		         ON cr.role_id = c.role_id AND cr.user_id = c.user_id
		 WHERE c.role_id = $1
		 ORDER BY c.added_at`, string(id))
	if err != nil {
		return nil, translate(err, fmt.Sprintf("listing candidates for role %s", id))
	}
	defer rows.Close()

	out := []domain.RoleCandidate{}
	for rows.Next() {
		var (
			c      domain.RoleCandidate
			user   string
			status string
		)
		if err := rows.Scan(scanTargets(
			[]any{&user, &c.DisplayName, &c.GitHubLogin, &c.Note},
			&c.AddedBy,
			[]any{&c.AddedAt, &status, &c.NotifiedAt, &c.Email})...); err != nil {
			return nil, translate(err, "scanning a candidate")
		}
		c.UserID = domain.UserID(user)
		c.ContactStatus = domain.ContactRequestStatus(status)
		c.Accepted = c.ContactStatus == domain.ContactAccepted
		out = append(out, c)
	}
	return out, translate(rows.Err(), "listing candidates")
}

// hydrateLocations reads every entry of a set of roles, with the address each
// onsite entry points at embedded — removed or not, because a closed role
// still says where it was (ADR-0024 §Bodies).
func (r *RoleRepository) hydrateLocations(ctx context.Context, t port.Tx, byID map[domain.RoleID]*domain.Role, ids []string) error {
	rows, err := r.db.q(t).Query(ctx, `
		SELECT l.role_id, l.mode, coalesce(l.country, ''), a.id IS NOT NULL,
		       `+addressColumnsNullable("a")+`
		  FROM role_locations l
		  LEFT JOIN organization_addresses a ON a.id = l.address_id
		 WHERE l.role_id = ANY($1::uuid[])`, ids)
	if err != nil {
		return translate(err, "reading role locations")
	}
	defer rows.Close()

	for rows.Next() {
		var (
			roleID, mode, country string
			hasAddress            bool
			addr                  addressRow
		)
		targets := append([]any{&roleID, &mode, &country, &hasAddress}, addr.targets()...)
		if err := rows.Scan(targets...); err != nil {
			return translate(err, "scanning a role location")
		}
		loc := domain.RoleLocation{Mode: domain.LocationMode(mode), Country: country}
		if hasAddress {
			a := addr.domain()
			loc.AddressID = &a.ID
			loc.Address = &a
		}
		if role := byID[domain.RoleID(roleID)]; role != nil {
			role.Locations = append(role.Locations, loc)
		}
	}
	if err := rows.Err(); err != nil {
		return translate(err, "reading role locations")
	}
	for _, role := range byID {
		domain.SortLocations(role.Locations)
	}
	return nil
}

// ActiveOnsiteRoles lists the draft and open roles with an onsite entry, at
// one address or at any, oldest first.
func (r *RoleRepository) ActiveOnsiteRoles(ctx context.Context, t port.Tx, org domain.OrganizationID, at *domain.AddressID) ([]domain.RoleSummary, error) {
	var address *string
	if at != nil {
		a := string(*at)
		address = &a
	}
	rows, err := r.db.q(t).Query(ctx, `
		SELECT r.id, r.title, r.status
		  FROM roles r
		 WHERE r.organization_id = $1
		   AND r.status IN ('draft', 'open')
		   AND EXISTS (SELECT 1 FROM role_locations l
		                WHERE l.role_id = r.id AND l.mode = 'onsite'
		                  AND ($2::uuid IS NULL OR l.address_id = $2::uuid))
		 ORDER BY r.created_at, r.id`, string(org), address)
	if err != nil {
		return nil, translate(err, fmt.Sprintf("listing onsite roles of organization %s", org))
	}
	defer rows.Close()

	out := []domain.RoleSummary{}
	for rows.Next() {
		var id, title, status string
		if err := rows.Scan(&id, &title, &status); err != nil {
			return nil, translate(err, "scanning an onsite role")
		}
		out = append(out, domain.RoleSummary{
			ID: domain.RoleID(id), Title: title, Status: domain.RoleStatus(status),
		})
	}
	return out, translate(rows.Err(), "listing onsite roles")
}
