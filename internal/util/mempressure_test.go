package util

import "testing"

// TestCheckMemoryPressureLevels locks the threshold logic via the test-injection
// override (no real memory exhausted). Covers OK / WARNING / CRITICAL boundaries
// on both axes (swap% and the free-MB floor).
func TestCheckMemoryPressureLevels(t *testing.T) {
	cases := []struct {
		name    string
		swapPct string // GT_MEMPRESSURE_TEST_SWAP_PCT
		freeMB  string // GT_MEMPRESSURE_TEST_FREE_MB ("" = benign default)
		want    MemoryPressureLevel
	}{
		{"low swap = OK", "10", "", MemoryOK},
		{"just below warn = OK", "84", "", MemoryOK},
		{"at warn threshold = WARNING", "85", "", MemoryWarning},
		{"mid warn = WARNING", "90", "", MemoryWarning},
		{"just below critical = WARNING", "94", "", MemoryWarning},
		{"at critical threshold = CRITICAL", "95", "", MemoryCritical},
		{"high swap = CRITICAL", "99", "", MemoryCritical},
		{"low free overrides to CRITICAL even at low swap", "10", "256", MemoryCritical},
		{"free above floor stays OK", "10", "4096", MemoryOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GT_MEMPRESSURE_TEST_SWAP_PCT", tc.swapPct)
			if tc.freeMB != "" {
				t.Setenv("GT_MEMPRESSURE_TEST_FREE_MB", tc.freeMB)
			} else {
				t.Setenv("GT_MEMPRESSURE_TEST_FREE_MB", "")
			}
			level, msg, err := CheckMemoryPressure()
			if err != nil {
				t.Fatalf("CheckMemoryPressure: %v", err)
			}
			if level != tc.want {
				t.Errorf("level = %v (%q), want %v", level, msg, tc.want)
			}
			// Non-OK levels must carry a message; OK must not.
			if tc.want == MemoryOK && msg != "" {
				t.Errorf("OK should have empty message, got %q", msg)
			}
			if tc.want != MemoryOK && msg == "" {
				t.Errorf("%v should have a message", tc.want)
			}
		})
	}
}

// TestMemoryPressureOverrideAbsent confirms no override env -> live read path runs
// (returns a real reading without error on this platform; we don't assert a level
// since it depends on the host, only that it doesn't error).
func TestMemoryPressureOverrideAbsent(t *testing.T) {
	t.Setenv("GT_MEMPRESSURE_TEST_SWAP_PCT", "")
	info, err := GetMemoryPressure()
	if err != nil {
		t.Fatalf("live GetMemoryPressure: %v", err)
	}
	if info.Simulated {
		t.Error("no override set but Simulated=true")
	}
	if info.SwapUsedPercent < 0 || info.SwapUsedPercent > 100 {
		t.Errorf("swap%% out of range: %.1f", info.SwapUsedPercent)
	}
}
