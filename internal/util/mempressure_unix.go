//go:build !windows && !darwin

package util

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// GetMemoryPressure reads swap + reclaimable memory from /proc/meminfo on Linux.
// The test-injection override short-circuits the read.
func GetMemoryPressure() (*MemoryPressureInfo, error) {
	if inj, ok := memPressureTestOverride(); ok {
		return inj, nil
	}

	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return nil, fmt.Errorf("read /proc/meminfo: %w", err)
	}
	mem := parseMeminfoKB(data) // values in KiB

	swapTotal := mem["SwapTotal"]
	swapFree := mem["SwapFree"]
	var swapPct float64
	if swapTotal > 0 {
		swapPct = float64(swapTotal-swapFree) / float64(swapTotal) * 100
	}

	// Reclaimable ~= MemAvailable (kernel's own estimate); fall back to MemFree.
	freeKB := mem["MemAvailable"]
	if freeKB == 0 {
		freeKB = mem["MemFree"]
	}

	return &MemoryPressureInfo{
		SwapUsedPercent: swapPct,
		FreeMB:          freeKB / 1024,
	}, nil
}

// parseMeminfoKB parses `Key:   12345 kB` lines into a map of KiB values.
func parseMeminfoKB(data []byte) map[string]uint64 {
	out := make(map[string]uint64)
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		key := strings.TrimSuffix(fields[0], ":")
		if v, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
			out[key] = v
		}
	}
	return out
}
