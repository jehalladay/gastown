//go:build darwin

package util

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"time"
)

// GetMemoryPressure reads swap usage (sysctl vm.swapusage) and free memory
// (vm_stat) on macOS. The test-injection override short-circuits the syscalls so
// the guard's warn/critical paths are exercisable without exhausting the host.
func GetMemoryPressure() (*MemoryPressureInfo, error) {
	if inj, ok := memPressureTestOverride(); ok {
		return inj, nil
	}

	swapPct, err := darwinSwapUsedPercent()
	if err != nil {
		return nil, err
	}
	freeMB := darwinFreeReclaimableMB() // best-effort; 0 if unreadable

	return &MemoryPressureInfo{SwapUsedPercent: swapPct, FreeMB: freeMB}, nil
}

// swapUsageRe parses `vm.swapusage: total = 2048.00M  used = 1900.00M  free = 148.00M`.
var swapUsageRe = regexp.MustCompile(`total = ([0-9.]+)M\s+used = ([0-9.]+)M`)

func darwinSwapUsedPercent() (float64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "sysctl", "-n", "vm.swapusage").Output()
	if err != nil {
		return 0, fmt.Errorf("sysctl vm.swapusage: %w", err)
	}
	m := swapUsageRe.FindSubmatch(out)
	if m == nil {
		return 0, fmt.Errorf("unexpected vm.swapusage format: %q", string(out))
	}
	total, _ := strconv.ParseFloat(string(m[1]), 64)
	used, _ := strconv.ParseFloat(string(m[2]), 64)
	if total <= 0 {
		return 0, nil // no swap configured -> 0% (FreeMB floor still guards)
	}
	return used / total * 100, nil
}

// vmStatPageRe parses lines like `Pages free: 12345.` from vm_stat.
var vmStatPageRe = regexp.MustCompile(`Pages (free|inactive|speculative|purgeable):\s+([0-9]+)\.`)

// darwinFreeReclaimableMB sums free + inactive + speculative + purgeable pages
// (the memory the kernel can reclaim before swapping harder) in MB. Best-effort:
// returns 0 if vm_stat is unreadable (CheckMemoryPressure then relies on swap%).
func darwinFreeReclaimableMB() uint64 {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "vm_stat").Output()
	if err != nil {
		return 0
	}
	const pageSize = 4096 // macOS vm_stat reports 4 KiB pages
	var pages uint64
	for _, m := range vmStatPageRe.FindAllSubmatch(out, -1) {
		if v, err := strconv.ParseUint(string(m[2]), 10, 64); err == nil {
			pages += v
		}
	}
	return pages * pageSize / (1024 * 1024)
}
