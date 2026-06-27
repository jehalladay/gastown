package cmd

import (
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/config"
)

// captureStderr is defined in status_test.go (same package) — reused here.

// TestWarnIfOnConflictInert verifies the footgun-killer: auto_rebase (which the
// live formula ignores — F8-inert) warns loudly; assign_back (the default,
// matches live behavior) and empty are silent.
func TestWarnIfOnConflictInert(t *testing.T) {
	cases := []struct {
		name     string
		value    string
		wantWarn bool
	}{
		{"auto_rebase warns (inert no-op)", config.OnConflictAutoRebase, true},
		{"assign_back silent (matches live)", config.OnConflictAssignBack, false},
		{"empty silent", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := captureStderr(t, func() { warnIfOnConflictInert(tc.value) })
			gotWarn := strings.Contains(out, "on_conflict")
			if gotWarn != tc.wantWarn {
				t.Errorf("warnIfOnConflictInert(%q): warned=%v, want %v (stderr=%q)", tc.value, gotWarn, tc.wantWarn, out)
			}
		})
	}
}
