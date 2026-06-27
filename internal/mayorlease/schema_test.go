package mayorlease

import (
	"strings"
	"testing"
)

// TestSplitDDL verifies the schema splitter yields exactly the two CREATE statements,
// trimmed and without trailing semicolons — the DDL contract EnsureSchema relies on
// (the mysql driver executes one statement per Exec).
func TestSplitDDL(t *testing.T) {
	stmts := splitDDL(Schema)
	if len(stmts) != 2 {
		t.Fatalf("splitDDL(Schema) = %d statements, want 2:\n%v", len(stmts), stmts)
	}
	if !strings.HasPrefix(stmts[0], "CREATE TABLE IF NOT EXISTS mayor_lease") {
		t.Errorf("stmt[0] should create mayor_lease, got: %.40q", stmts[0])
	}
	if !strings.HasPrefix(stmts[1], "CREATE TABLE IF NOT EXISTS mayor_clients") {
		t.Errorf("stmt[1] should create mayor_clients, got: %.40q", stmts[1])
	}
	for i, s := range stmts {
		if strings.HasSuffix(s, ";") {
			t.Errorf("stmt[%d] retains trailing semicolon: %.40q", i, s)
		}
		if s != strings.TrimSpace(s) {
			t.Errorf("stmt[%d] not trimmed: %.40q", i, s)
		}
	}
}

// TestSplitDDLEdgeCases guards the trimmer against empty fragments and trailing
// whitespace-only tails (a naive split on ';' would emit a spurious empty statement
// that Exec would reject).
func TestSplitDDLEdgeCases(t *testing.T) {
	cases := map[string]int{
		"":                      0,
		"  ;  ":                 0,
		"SELECT 1;":             1,
		"SELECT 1; SELECT 2;":   2,
		"SELECT 1; SELECT 2":    2, // no trailing semicolon
		"\n SELECT 1 ;\n\n ;\n": 1, // trailing empty fragment dropped
	}
	for in, want := range cases {
		if got := len(splitDDL(in)); got != want {
			t.Errorf("splitDDL(%q) = %d, want %d", in, got, want)
		}
	}
}
