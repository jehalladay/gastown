package util

import (
	"fmt"
	"os"
	"strconv"
)

// MemoryPressureInfo captures memory/swap pressure — the signal that precedes the
// macOS jetsam kills that twice nearly took the control plane (and Dolt) down.
type MemoryPressureInfo struct {
	// SwapUsedPercent is the percentage of swap in use (0-100). The primary signal:
	// sustained high swap means the working set exceeds RAM and jetsam is imminent.
	SwapUsedPercent float64

	// FreeMB is free + inactive (reclaimable) physical memory in MB.
	FreeMB uint64

	// Simulated is true when the values came from a test-injection env var rather
	// than the live system (so callers/tests can tell).
	Simulated bool
}

// Default thresholds for memory-pressure checks. Tuned so the guard WARNs (and at
// critical, sheds idle crew) BEFORE jetsam fires — jetsam on macOS starts killing
// around very high swap + low free, and it picks the victim non-deterministically
// (which has repeatedly been Dolt). We want to act first.
const (
	// MemSwapWarningPercent: swap this full = working set exceeds RAM, shed soon.
	MemSwapWarningPercent float64 = 85.0
	// MemSwapCriticalPercent: jetsam-risk range — shed/park idle crew NOW, before
	// the kernel SIGKILLs Dolt.
	MemSwapCriticalPercent float64 = 95.0
	// MemFreeCriticalMB: absolute floor of reclaimable memory; below this, critical
	// regardless of swap% (covers the no-swap-configured case).
	MemFreeCriticalMB uint64 = 512
)

// MemoryPressureLevel mirrors DiskSpaceLevel for the memory axis.
type MemoryPressureLevel int

const (
	// MemoryOK — memory pressure is fine.
	MemoryOK MemoryPressureLevel = iota
	// MemoryWarning — pressure rising; reduce/shed soon.
	MemoryWarning
	// MemoryCritical — jetsam-imminent; shed/park idle crew before Dolt is killed.
	MemoryCritical
)

func (l MemoryPressureLevel) String() string {
	switch l {
	case MemoryOK:
		return "ok"
	case MemoryWarning:
		return "warning"
	case MemoryCritical:
		return "critical"
	default:
		return "unknown"
	}
}

// memPressureTestOverride returns an injected MemoryPressureInfo when the test env
// vars are set, so the guard can be exercised (warn/critical paths) without actually
// exhausting the host. GT_MEMPRESSURE_TEST_SWAP_PCT sets swap%; the optional
// GT_MEMPRESSURE_TEST_FREE_MB sets free memory. Returns (nil, false) when unset.
func memPressureTestOverride() (*MemoryPressureInfo, bool) {
	pctStr := os.Getenv("GT_MEMPRESSURE_TEST_SWAP_PCT")
	if pctStr == "" {
		return nil, false
	}
	pct, err := strconv.ParseFloat(pctStr, 64)
	if err != nil {
		return nil, false
	}
	freeMB := uint64(8192) // a benign default unless overridden
	if f := os.Getenv("GT_MEMPRESSURE_TEST_FREE_MB"); f != "" {
		if v, err := strconv.ParseUint(f, 10, 64); err == nil {
			freeMB = v
		}
	}
	return &MemoryPressureInfo{SwapUsedPercent: pct, FreeMB: freeMB, Simulated: true}, true
}

// CheckMemoryPressure evaluates memory/swap pressure and returns the level + a
// human-readable message (empty when OK). Honors the test-injection override.
func CheckMemoryPressure() (MemoryPressureLevel, string, error) {
	info, err := GetMemoryPressure()
	if err != nil {
		return MemoryOK, "", err
	}

	simNote := ""
	if info.Simulated {
		simNote = " [simulated]"
	}

	if info.SwapUsedPercent >= MemSwapCriticalPercent || info.FreeMB < MemFreeCriticalMB {
		return MemoryCritical,
			fmt.Sprintf("CRITICAL: swap %.1f%% used, %s free%s — jetsam-imminent, SHED/park idle crew before Dolt is killed",
				info.SwapUsedPercent, FormatBytesHuman(info.FreeMB*1024*1024), simNote),
			nil
	}
	if info.SwapUsedPercent >= MemSwapWarningPercent {
		return MemoryWarning,
			fmt.Sprintf("WARNING: swap %.1f%% used, %s free%s — memory pressure rising, reduce/shed workload",
				info.SwapUsedPercent, FormatBytesHuman(info.FreeMB*1024*1024), simNote),
			nil
	}
	return MemoryOK, "", nil
}
