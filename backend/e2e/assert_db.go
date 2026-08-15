package e2e

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

// DB assertions cover state the API does not return — link status, standing,
// cooldown tier, and above all the things whose absence is the point: that a
// failed validation enqueued nothing, that staging a shortlist entry wrote no
// contact request.
//
// A snapshot of a response body cannot express "and nothing else happened",
// which is why these exist alongside it.

// AssertDB evaluates every assertion in a step against one schema. It reports
// all failures rather than the first, so a single run says everything.
func AssertDB(ctx context.Context, pool poolLike, schema string, assertions map[string]DBAssertion, bindings map[string]string) error {
	if len(assertions) == 0 {
		return nil
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, fmt.Sprintf("SET search_path TO %q, ext", schema)); err != nil {
		return fmt.Errorf("set search_path: %w", err)
	}

	tables := make([]string, 0, len(assertions))
	for table := range assertions {
		tables = append(tables, table)
	}
	sort.Strings(tables)

	var problems []string
	for _, table := range tables {
		if err := assertTable(ctx, conn.Conn(), table, assertions[table], bindings); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("database assertions failed:\n    %s", strings.Join(problems, "\n    "))
	}
	return nil
}

func assertTable(ctx context.Context, conn *pgx.Conn, table string, a DBAssertion, bindings map[string]string) error {
	if !isIdentifier(table) {
		return fmt.Errorf("%q is not a valid table name", table)
	}

	where, args, err := buildWhere(a.Where, bindings, 1)
	if err != nil {
		return fmt.Errorf("%s.where: %w", table, err)
	}
	clause := ""
	if where != "" {
		clause = " WHERE " + where
	}

	if a.Count != nil {
		var got int
		query := fmt.Sprintf("SELECT count(*) FROM %q%s", table, clause)
		if err := conn.QueryRow(ctx, query, args...).Scan(&got); err != nil {
			return fmt.Errorf("%s: %w", table, err)
		}
		if got != *a.Count {
			return fmt.Errorf("%s: expected %d row(s)%s, found %d",
				table, *a.Count, describe(a.Where), got)
		}
	}

	// all_match: every matched row must satisfy these, so a violation is a row
	// that does NOT.
	if len(a.AllMatch) > 0 {
		if err := assertRowPredicate(ctx, conn, table, clause, args, a.AllMatch, bindings, false); err != nil {
			return err
		}
	}
	// none_match: no matched row may satisfy these.
	if len(a.NoneMatch) > 0 {
		if err := assertRowPredicate(ctx, conn, table, clause, args, a.NoneMatch, bindings, true); err != nil {
			return err
		}
	}

	for _, column := range a.SameValue {
		if !isIdentifier(column) {
			return fmt.Errorf("%s.same_value: %q is not a valid column name", table, column)
		}
		var distinct int
		query := fmt.Sprintf("SELECT count(DISTINCT %q) FROM %q%s", column, table, clause)
		if err := conn.QueryRow(ctx, query, args...).Scan(&distinct); err != nil {
			return fmt.Errorf("%s.%s: %w", table, column, err)
		}
		if distinct > 1 {
			return fmt.Errorf("%s: expected every matched row to share one %s, found %d distinct values",
				table, column, distinct)
		}
	}
	return nil
}

// assertRowPredicate counts rows that satisfy (or violate) a predicate.
// negate=false means "every row must match"; negate=true means "no row may".
func assertRowPredicate(ctx context.Context, conn *pgx.Conn, table, clause string, args []any,
	predicate map[string]any, bindings map[string]string, negate bool) error {

	inner, innerArgs, err := buildWhere(predicate, bindings, len(args)+1)
	if err != nil {
		return fmt.Errorf("%s: %w", table, err)
	}

	combined := append(append([]any{}, args...), innerArgs...)
	var query, message string
	if negate {
		query = fmt.Sprintf("SELECT count(*) FROM %q%s AND (%s)", table, orWhere(clause), inner)
		message = "expected no matched row to satisfy"
	} else {
		query = fmt.Sprintf("SELECT count(*) FROM %q%s AND NOT (%s)", table, orWhere(clause), inner)
		message = "expected every matched row to satisfy"
	}

	var violations int
	if err := conn.QueryRow(ctx, query, combined...).Scan(&violations); err != nil {
		return fmt.Errorf("%s: %w", table, err)
	}
	if violations > 0 {
		return fmt.Errorf("%s: %s %s — %d row(s) do not",
			table, message, describeMap(predicate), violations)
	}
	return nil
}

// orWhere makes a WHERE clause safe to append " AND ..." to, even when the
// assertion had no `where` of its own.
func orWhere(clause string) string {
	if clause == "" {
		return " WHERE TRUE"
	}
	return clause
}

// buildWhere turns a map into a SQL fragment and its arguments, substituting
// {{binding}} in string values. NULL is compared with IS NULL rather than =,
// which would never match.
//
// Column names are validated against an identifier pattern rather than quoted
// blindly: a fixture is trusted input, but a typo producing invalid SQL fails
// with a parse error that names nothing useful.
func buildWhere(where map[string]any, bindings map[string]string, start int) (string, []any, error) {
	if len(where) == 0 {
		return "", nil, nil
	}
	columns := make([]string, 0, len(where))
	for column := range where {
		if !isIdentifier(column) {
			return "", nil, fmt.Errorf("%q is not a valid column name", column)
		}
		columns = append(columns, column)
	}
	sort.Strings(columns)

	var clauses []string
	var args []any
	for _, column := range columns {
		value := where[column]
		if s, ok := value.(string); ok {
			resolved, err := resolve(s, bindings)
			if err != nil {
				return "", nil, fmt.Errorf("%s: %w", column, err)
			}
			value = resolved
		}
		if value == nil {
			clauses = append(clauses, fmt.Sprintf("%q IS NULL", column))
			continue
		}
		args = append(args, value)
		clauses = append(clauses, fmt.Sprintf("%q = $%d", column, start+len(args)-1))
	}
	return strings.Join(clauses, " AND "), args, nil
}

func describe(where map[string]any) string {
	if len(where) == 0 {
		return ""
	}
	return " matching " + describeMap(where)
}

func describeMap(m map[string]any) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, m[k]))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func isIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r == '_':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}
