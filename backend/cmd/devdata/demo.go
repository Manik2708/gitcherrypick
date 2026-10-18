package main

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// One role with somebody at EVERY stage.
//
// The fixture seeds are written for assertions, so they stop where the
// assertion stops: every candidate in them is staged. That is the one shape in
// which a stage filter shows nothing to filter — and it is the shape a
// developer opening the roles screen always saw, which reads as the feature
// missing rather than as the data being thin.
//
// So this is a DEMO layer, on top of the seeds and separate from them: nothing
// here is asserted by any fixture, and adding a person to it cannot change
// what the suite proves.
//
// Ids are fixed and every statement is ON CONFLICT DO NOTHING, so running it
// against a database that already has it is a no-op rather than a duplicate.
const (
	demoRole  = "01a0c000-0000-7000-8000-000000000001"
	demoPriya = "01a0c000-0000-7000-8000-00000000000a"

	// From fixtures/seed/principals.json.
	demoOrg   = "01920000-0000-7000-8000-00000000b002" // Tiny Studio
	demoHirer = "01920000-0000-7000-8000-00000000c003" // Sam Ortega
	alice     = "01920000-0000-7000-8000-00000000a001"
	bob       = "01920000-0000-7000-8000-00000000a002"
	carol     = "01920000-0000-7000-8000-00000000a003"
	dave      = "01920000-0000-7000-8000-00000000a004"
)

// seedDemo writes the every-stage role.
//
// Run on EVERY start, not only after --reset: a developer who already has a
// database is exactly the person who has been looking at a one-stage list.
func seedDemo(ctx context.Context, pool *pgxpool.Pool, schema string) error {
	statements := []string{
		// A fifth contributor, so each of the five stages can have its own
		// person — one row per person per job is the primary key.
		`INSERT INTO ` + schema + `.users (id, display_name, email, bio, location,
		            overall_score, generalist_score)
		 VALUES ('` + demoPriya + `', 'Priya Raman', 'priya@example.com',
		         'Payments, idempotency, and the boring parts of money.', 'Bengaluru, IN',
		         71.40, 96.20)
		 ON CONFLICT (id) DO NOTHING`,

		// Contacted a fortnight ago, so the four answers below have had time
		// to be four different answers.
		`INSERT INTO ` + schema + `.roles (id, organization_id, title, description,
		            engagement, status, currency, yearly_ctc, yearly_base,
		            min_oss_yoe, max_interview_rounds, avg_days_to_offer,
		            created_by, opened_by, opened_at, first_contacted_at)
		 VALUES ('` + demoRole + `', '` + demoOrg + `', 'Staff engineer, payments',
		         'Ledgers, reconciliation and the parts of a payment that must not be approximately right.',
		         'full_time', 'open', 'GBP', 14500000, 11500000,
		         3, 3, 12,
		         '` + demoHirer + `', '` + demoHirer + `', now() - interval '21 days',
		         now() - interval '14 days')
		 ON CONFLICT (id) DO NOTHING`,

		// Where the work is (ADR-0024): remote from anywhere, the one kind of
		// entry Tiny Studio — a fully remote organisation — may post.
		`INSERT INTO ` + schema + `.role_locations (role_id, mode, country, address_id)
		 VALUES ('` + demoRole + `', 'remote', NULL, NULL)
		 ON CONFLICT DO NOTHING`,

		// NOT CONTACTED — notified_at is null, so this row is still removable.
		candidate(schema, alice, "Best Go evidence in the pool.", ""),

		// The four that were told, at fourteen days.
		candidate(schema, bob, "Kubernetes depth, and writes it down.", "14 days"),
		candidate(schema, carol, "Postgres internals.", "14 days"),
		candidate(schema, dave, "Reviewed half the pipeline.", "14 days"),
		candidate(schema, demoPriya, "Payments, at the ledger level.", "14 days"),

		// ACCEPTED — the address is released, which is the whole point of the
		// stamp: it is what the candidate list and the scorecard both read.
		request(schema, "01a0c000-0000-7000-8000-000000000101", bob, "accepted", "2 days", "released"),

		// DECLINED — answered, and nothing released. A decline is an answer.
		request(schema, "01a0c000-0000-7000-8000-000000000102", carol, "declined", "3 days", ""),

		// EXPIRED — never answered, and the window is behind us.
		request(schema, "01a0c000-0000-7000-8000-000000000103", dave, "expired", "", ""),

		// WAITING — told, still inside the window, no answer yet.
		request(schema, "01a0c000-0000-7000-8000-000000000104", demoPriya, "pending", "", ""),
	}

	for i, statement := range statements {
		if _, err := pool.Exec(ctx, statement); err != nil {
			return fmt.Errorf("seeding the demo role (statement %d): %w", i+1, err)
		}
	}
	return nil
}

// candidate is one person on the job. told is an interval like "14 days", or
// "" for somebody nobody has been told about.
func candidate(schema, user, note, told string) string {
	notified := "NULL"
	if told != "" {
		notified = "now() - interval '" + told + "'"
	}
	return `INSERT INTO ` + schema + `.role_candidates
	            (role_id, user_id, note, added_by, added_at, notified_at)
	     VALUES ('` + demoRole + `', '` + user + `', '` + note + `', '` + demoHirer + `',
	             now() - interval '15 days', ` + notified + `)
	     ON CONFLICT (role_id, user_id) DO NOTHING`
}

// request is the contact request that carries somebody's answer.
//
// An EXPIRED one is dated in the past; everything else still has a window, so
// the expiry sweep cannot quietly turn a pending request into an expired one
// while somebody is looking at the screen.
func request(schema, id, user, status, answered, released string) string {
	expires := "now() + interval '7 days'"
	if status == "expired" {
		expires = "now() - interval '2 days'"
	}

	respondedAt, releasedAt := "NULL", "NULL"
	if answered != "" {
		respondedAt = "now() - interval '" + answered + "'"
	}
	if released != "" {
		releasedAt = respondedAt
	}

	return `INSERT INTO ` + schema + `.contact_requests
	            (id, role_id, user_id, organization_id, requested_by, status,
	             tentative_result_date, responded_at, email_released_at, expires_at, created_at)
	     VALUES ('` + id + `', '` + demoRole + `', '` + user + `', '` + demoOrg + `',
	             '` + demoHirer + `', '` + status + `', current_date + 10,
	             ` + respondedAt + `, ` + releasedAt + `, ` + expires + `,
	             now() - interval '14 days')
	     ON CONFLICT (id) DO NOTHING`
}
