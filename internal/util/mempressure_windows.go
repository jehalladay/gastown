//go:build windows

package util

// GetMemoryPressure on Windows: the test-injection override still works (for
// parity in tests); the live path reports OK (no swap-pressure source wired —
// Gas Town's control plane runs on macOS/Linux, this is a stub for build parity).
func GetMemoryPressure() (*MemoryPressureInfo, error) {
	if inj, ok := memPressureTestOverride(); ok {
		return inj, nil
	}
	return &MemoryPressureInfo{SwapUsedPercent: 0, FreeMB: 1 << 20}, nil
}
