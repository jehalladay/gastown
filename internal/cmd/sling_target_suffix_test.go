package cmd

import "testing"

// TestMatchSessionBySuffix locks the prefix-agnostic session fallback that fixes
// re-hooking remote crew (hq-wwxq follow-up): PrefixFor() falls back to "gt" when
// rigs.json is absent (remote node), so "reactivecli/crew/eng_tools" mis-derives to
// "gt-crew-eng_tools" while the live session is "rc-crew-eng_tools". The fallback must
// match the shared "-crew-eng_tools" suffix under the real prefix — but only when the
// match is unambiguous.
func TestMatchSessionBySuffix(t *testing.T) {
	cases := []struct {
		name      string
		candidate string
		sessions  []string
		want      string
		wantOK    bool
	}{
		{
			name:      "rig-prefixed crew session (the bug)",
			candidate: "gt-crew-eng_tools",
			sessions:  []string{"rc-crew-eng_tools", "rc-crew-other", "hq-deacon"},
			want:      "rc-crew-eng_tools",
			wantOK:    true,
		},
		{
			name:      "witness suffix",
			candidate: "gt-witness",
			sessions:  []string{"rc-witness", "rc-crew-x"},
			want:      "rc-witness",
			wantOK:    true,
		},
		{
			name:      "no match",
			candidate: "gt-crew-ghost",
			sessions:  []string{"rc-crew-eng_tools", "hq-deacon"},
			want:      "",
			wantOK:    false,
		},
		{
			name:      "ambiguous: same suffix under two prefixes -> reject",
			candidate: "gt-crew-eng_tools",
			sessions:  []string{"rc-crew-eng_tools", "bd-crew-eng_tools"},
			want:      "",
			wantOK:    false,
		},
		{
			name:      "exact match still works (already-correct prefix)",
			candidate: "rc-crew-eng_tools",
			sessions:  []string{"rc-crew-eng_tools"},
			want:      "rc-crew-eng_tools",
			wantOK:    true,
		},
		{
			name:      "candidate without a dash -> no match",
			candidate: "weird",
			sessions:  []string{"rc-crew-eng_tools"},
			want:      "",
			wantOK:    false,
		},
		{
			name:      "must not match a different crew with overlapping name segment",
			candidate: "gt-crew-eng",
			sessions:  []string{"rc-crew-eng_tools"}, // suffix "crew-eng" != "crew-eng_tools"
			want:      "",
			wantOK:    false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := matchSessionBySuffix(tc.candidate, tc.sessions)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("matchSessionBySuffix(%q, %v) = (%q, %v), want (%q, %v)",
					tc.candidate, tc.sessions, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}
