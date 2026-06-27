package cmd

import (
	"reflect"
	"testing"
)

// TestSplitLeadingRig covers the "gt crew stop <rig> <name...>" arg parsing that
// caused the spurious "No session found for reactivecli/reactivecli" warning:
// the leading rig name must be peeled off, not processed as a crew name.
func TestSplitLeadingRig(t *testing.T) {
	isRig := func(s string) bool { return s == "reactivecli" || s == "beads" }

	cases := []struct {
		name     string
		args     []string
		wantRig  string
		wantRest []string
		wantOK   bool
	}{
		{"rig + one crew", []string{"reactivecli", "emma"}, "reactivecli", []string{"emma"}, true},
		{"rig + many crew", []string{"beads", "ace", "joe"}, "beads", []string{"ace", "joe"}, true},
		{"single arg never splits", []string{"reactivecli"}, "", []string{"reactivecli"}, false},
		{"first arg not a rig", []string{"emma", "joe"}, "", []string{"emma", "joe"}, false},
		{"rig/name form is not split here", []string{"reactivecli/emma", "joe"}, "", []string{"reactivecli/emma", "joe"}, false},
		{"empty args", nil, "", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rig, rest, ok := splitLeadingRig(c.args, isRig)
			if rig != c.wantRig || ok != c.wantOK || !reflect.DeepEqual(rest, c.wantRest) {
				t.Errorf("splitLeadingRig(%v) = (%q, %v, %v), want (%q, %v, %v)",
					c.args, rig, rest, ok, c.wantRig, c.wantRest, c.wantOK)
			}
		})
	}
}
