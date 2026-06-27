package daemon

import (
	"fmt"
	"os"
	"strings"
)

// loadAverage1Sysctl is a no-op on Linux — /proc/loadavg is used directly.
func loadAverage1Sysctl() float64 {
	return 0
}

// availableMemoryGB returns available memory in GB on Linux.
// Reads MemAvailable from /proc/meminfo (kernel 3.14+).
func availableMemoryGB() float64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}

	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "MemAvailable:") {
			var kb uint64
			_, err := fmt.Sscanf(line, "MemAvailable: %d kB", &kb)
			if err != nil {
				return 0
			}
			return float64(kb) / (1024 * 1024)
		}
	}
	return 0
}

// swapUsedPercent returns swap used as a percent of total on Linux, from
// /proc/meminfo SwapTotal/SwapFree. Returns -1 when unavailable or no swap
// (SwapTotal=0) so a swapless host never trips the critical-shed threshold.
func swapUsedPercent() float64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return -1
	}
	var total, free uint64
	var haveTotal, haveFree bool
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "SwapTotal:") {
			if _, err := fmt.Sscanf(line, "SwapTotal: %d kB", &total); err == nil {
				haveTotal = true
			}
		} else if strings.HasPrefix(line, "SwapFree:") {
			if _, err := fmt.Sscanf(line, "SwapFree: %d kB", &free); err == nil {
				haveFree = true
			}
		}
	}
	if !haveTotal || !haveFree || total == 0 {
		return -1
	}
	used := total - free
	return float64(used) / float64(total) * 100
}
