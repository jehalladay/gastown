package cmd

import "testing"

// TestEnvIdentityPresent locks the gate for the off-town prime path (F2, hq-wwxq):
// prime only enters primeFromEnv when GT_ROLE + GT_RIG + (GT_CREW|GT_POLECAT) are all
// set. A bare cwd with no town and no env identity must still no-op (original behavior),
// so a partial identity must NOT trip the path.
func TestEnvIdentityPresent(t *testing.T) {
	cases := []struct {
		name                     string
		role, rig, crew, polecat string
		want                     bool
	}{
		{"all set (crew)", "reactivecli/crew/tester", "reactivecli", "tester", "", true},
		{"all set (polecat)", "reactivecli/polecats/p1", "reactivecli", "", "p1", true},
		{"no role", "", "reactivecli", "tester", "", false},
		{"no rig", "reactivecli/crew/tester", "", "tester", "", false},
		{"no crew or polecat", "reactivecli/crew/tester", "reactivecli", "", "", false},
		{"all empty", "", "", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GT_ROLE", tc.role)
			t.Setenv("GT_RIG", tc.rig)
			t.Setenv("GT_CREW", tc.crew)
			t.Setenv("GT_POLECAT", tc.polecat)
			if got := envIdentityPresent(); got != tc.want {
				t.Errorf("envIdentityPresent() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestGetRoleHomeOffTown guards the off-town home: with townRoot=="" getRoleHome must
// return "" (callers already handle empty Home), NOT a misleading relative path like
// "rig/crew/x" that filepath.Join("", ...) would otherwise produce and that would
// resolve against an arbitrary cwd on the node.
func TestGetRoleHomeOffTown(t *testing.T) {
	roles := []struct {
		role           Role
		rig, polecat   string
	}{
		{RoleCrew, "reactivecli", "tester"},
		{RolePolecat, "reactivecli", "p1"},
		{RoleWitness, "reactivecli", ""},
		{RoleRefinery, "reactivecli", ""},
		{RoleMayor, "", ""},
		{RoleDeacon, "", ""},
	}
	for _, r := range roles {
		if got := getRoleHome(r.role, r.rig, r.polecat, ""); got != "" {
			t.Errorf("getRoleHome(%v, %q, %q, \"\") = %q, want \"\" (off-town)", r.role, r.rig, r.polecat, got)
		}
	}
	// On-town still builds the real path (regression guard for the added branch).
	if got := getRoleHome(RoleCrew, "reactivecli", "tester", "/town"); got != "/town/reactivecli/crew/tester" {
		t.Errorf("on-town getRoleHome regressed: %q", got)
	}
}
