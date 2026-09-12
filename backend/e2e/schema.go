package e2e

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The approved schema, read from the RFC corpus.
//
// There is no migration tool and no checked-in full schema: rfc/*.schema holds
// the deltas, in RFC order, and applying them in that order IS the schema
// (CLAUDE.md). Exported because three callers need it — the fixture suite, the
// repository suite, and cmd/devdata, which builds a database a developer can
// point a frontend at.

// DefaultSchemaDir is where the deltas live, relative to backend/e2e.
const DefaultSchemaDir = "../../rfc"

// LoadDDL reads the approved schema deltas in RFC order. They are not
// commutative, and each statement is applied separately because
// ALTER TYPE ... ADD VALUE cannot share a transaction with its first use.
func LoadDDL(rfcDir string) ([]string, error) {
	paths, err := filepath.Glob(filepath.Join(rfcDir, "RFC-*.schema"))
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no RFC-*.schema files under %s", rfcDir)
	}
	var statements []string
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		statements = append(statements, SplitStatements(string(raw))...)
	}
	return statements, nil
}

// SplitStatements breaks a .schema file into individual statements.
//
// Comments are removed BEFORE splitting, not after: a line comment may contain
// a semicolon ("This file is a DELTA; the full schema is..."), and splitting
// first tears it in half, leaving the tail as prose the parser then chokes on.
//
// Statements are applied one at a time rather than as one batch because
// ALTER TYPE ... ADD VALUE cannot share a transaction with its first use —
// verified in RFC-0008's schema against PostgreSQL 16.
func SplitStatements(sql string) []string {
	var (
		out       []string
		current   strings.Builder
		runes     = []rune(sql)
		inLine    bool // -- to end of line
		inBlock   bool // /* ... */
		inString  bool // '...'
		dollarTag string
	)

	// A statement ends at a semicolon ONLY when the scanner is at top level.
	// Splitting the comment-stripped text afterwards is not equivalent: the
	// body of a $$...$$ function contains semicolons that are not terminators.
	flush := func() {
		if statement := strings.TrimSpace(current.String()); statement != "" {
			out = append(out, statement)
		}
		current.Reset()
	}

	for i := 0; i < len(runes); i++ {
		rest := string(runes[i:])

		switch {
		case inLine:
			if runes[i] == '\n' {
				inLine = false
				current.WriteRune('\n')
			}
			continue

		case inBlock:
			if strings.HasPrefix(rest, "*/") {
				inBlock = false
				i++
			}
			continue

		case dollarTag != "":
			if strings.HasPrefix(rest, dollarTag) {
				current.WriteString(dollarTag)
				i += len(dollarTag) - 1
				dollarTag = ""
				continue
			}
			current.WriteRune(runes[i])
			continue

		case inString:
			current.WriteRune(runes[i])
			if runes[i] == '\'' {
				// '' is an escaped quote, not the end of the literal.
				if i+1 < len(runes) && runes[i+1] == '\'' {
					current.WriteRune('\'')
					i++
					continue
				}
				inString = false
			}
			continue
		}

		switch {
		case strings.HasPrefix(rest, "--"):
			inLine = true
		case strings.HasPrefix(rest, "/*"):
			inBlock = true
			i++
		case runes[i] == '\'':
			inString = true
			current.WriteRune(runes[i])
		case runes[i] == '$':
			if tag := dollarQuote(rest); tag != "" {
				dollarTag = tag
				current.WriteString(tag)
				i += len(tag) - 1
				continue
			}
			current.WriteRune(runes[i])
		case runes[i] == ';':
			flush()
		default:
			current.WriteRune(runes[i])
		}
	}
	flush()

	return out
}

// dollarQuote returns the opening tag of a $$ or $tag$ literal, or "".
func dollarQuote(s string) string {
	if !strings.HasPrefix(s, "$") {
		return ""
	}
	end := strings.Index(s[1:], "$")
	if end < 0 {
		return ""
	}
	tag := s[:end+2]
	for _, r := range tag[1 : len(tag)-1] {
		if r != '_' && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return ""
		}
	}
	return tag
}

// ApplyDDL runs the deltas against one schema, one statement at a time.
//
// The search_path is set on the connection rather than baked into the DDL, so
// the same statements build a throwaway fixture schema and a developer's
// database without a textual difference between them.
func ApplyDDL(ctx context.Context, pool *pgxpool.Pool, schema string, statements []string) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, fmt.Sprintf("SET search_path TO %q, ext", schema)); err != nil {
		return fmt.Errorf("set search_path: %w", err)
	}
	for _, statement := range statements {
		if _, err := conn.Exec(ctx, statement); err != nil {
			return fmt.Errorf("%w\n\nstatement: %s", err, truncate(statement))
		}
	}
	return nil
}
