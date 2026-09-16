package postgres

import (
	"fmt"

	"github.com/Manik2708/gitcherrypick/backend/internal/domain"
)

// Attribution (ADR-0016 §9).
//
// A row that records who authored it stores a hirer id. Emitting that id alone
// sends a reader to look up a person who may have been revoked, so every read
// resolves the author here instead — joined, not fetched per row, because a
// list of twenty rounds would otherwise be twenty-one queries.
//
// `active` comes from disabled_at rather than from the row's absence: a revoked
// seat is still the author, and hiding them would falsify the record rather
// than tidy it.

// hirerRefColumns names an author's four attribution columns under an alias.
//
// A function rather than a constant because the alias differs per query —
// shortlists join `author`, entries joining two authors would need two.
func hirerRefColumns(alias string) string {
	return fmt.Sprintf(
		`%[1]s.id, %[1]s.username, %[1]s.display_name, (%[1]s.disabled_at IS NULL)`,
		alias)
}

// hirerRefTargets is the matching scan destination, in the same order.
//
// Paired with hirerRefColumns deliberately: the column list and the scan list
// are the one place a mismatch is silent, so they live side by side.
func hirerRefTargets(r *domain.HirerRef) []any {
	return []any{&r.ID, &r.Username, &r.DisplayName, &r.Active}
}

// scanTargets flattens fixed destinations around an author's four.
func scanTargets(before []any, ref *domain.HirerRef, after []any) []any {
	out := make([]any, 0, len(before)+4+len(after))
	out = append(out, before...)
	out = append(out, hirerRefTargets(ref)...)
	return append(out, after...)
}
