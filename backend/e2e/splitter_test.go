package e2e

import "testing"

// The splitter is the one piece of the harness with no fixture behind it, and
// it broke twice while being written — once by stripping comments after
// splitting on ';' (a comment containing a semicolon was torn in half), and
// once by splitting the comment-stripped text at the end (the semicolons inside
// a $$ function body were treated as terminators).
//
// Both are regressions worth keeping a test for: either one produces a
// "syntax error at or near" that points at prose and explains nothing.

func TestSplitStatements(t *testing.T) {
	tests := []struct {
		name string
		sql  string
		want []string
	}{
		{
			name: "a comment containing a semicolon is not a statement boundary",
			sql:  "-- This file is a DELTA; the full schema is the concatenation\nCREATE TABLE t (id int);",
			want: []string{"CREATE TABLE t (id int)"},
		},
		{
			name: "semicolons inside a dollar-quoted body are not boundaries",
			sql:  "CREATE FUNCTION f() RETURNS trigger AS $$\nBEGIN\n  NEW.x = now();\n  RETURN NEW;\nEND;\n$$ LANGUAGE plpgsql;",
			want: []string{"CREATE FUNCTION f() RETURNS trigger AS $$\nBEGIN\n  NEW.x = now();\n  RETURN NEW;\nEND;\n$$ LANGUAGE plpgsql"},
		},
		{
			name: "semicolons inside a string literal are not boundaries",
			sql:  "COMMENT ON TABLE t IS 'attaches as trg_<table>; never set by hand';",
			want: []string{"COMMENT ON TABLE t IS 'attaches as trg_<table>; never set by hand'"},
		},
		{
			name: "an escaped quote does not end the literal",
			sql:  "INSERT INTO t VALUES ('it''s fine; really');",
			want: []string{"INSERT INTO t VALUES ('it''s fine; really')"},
		},
		{
			name: "block comments are removed",
			sql:  "/* a block;\n   spanning lines */\nCREATE TABLE t (id int);",
			want: []string{"CREATE TABLE t (id int)"},
		},
		{
			name: "a tagged dollar quote is respected",
			sql:  "SELECT $body$semi; colon$body$;",
			want: []string{"SELECT $body$semi; colon$body$"},
		},
		{
			name: "$1 is a placeholder, not a dollar quote",
			sql:  "SELECT * FROM t WHERE id = $1;SELECT 2;",
			want: []string{"SELECT * FROM t WHERE id = $1", "SELECT 2"},
		},
		{
			name: "a trailing statement without a semicolon still counts",
			sql:  "SELECT 1;\nSELECT 2",
			want: []string{"SELECT 1", "SELECT 2"},
		},
		{
			name: "comment-only input yields nothing",
			sql:  "-- nothing here;\n-- nor here\n",
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SplitStatements(tt.sql)
			if len(got) != len(tt.want) {
				t.Fatalf("expected %d statement(s), got %d: %q", len(tt.want), len(got), got)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("statement %d:\n  want %q\n  got  %q", i, tt.want[i], got[i])
				}
			}
		})
	}
}
