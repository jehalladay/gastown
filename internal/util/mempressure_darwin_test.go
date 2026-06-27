//go:build darwin

package util

import "testing"

// Real vm_stat output from an Apple Silicon host: header declares 16384-byte pages.
const vmStatAppleSilicon = `Mach Virtual Memory Statistics: (page size of 16384 bytes)
Pages free:                              384989.
Pages active:                            500000.
Pages inactive:                          751098.
Pages speculative:                        12535.
Pages purgeable:                          12468.
`

// Intel host: 4096-byte pages.
const vmStatIntel = `Mach Virtual Memory Statistics: (page size of 4096 bytes)
Pages free:                              100000.
Pages inactive:                          100000.
Pages speculative:                            0.
Pages purgeable:                              0.
`

func TestParseVMStatFreeMB_UsesDeclaredPageSize(t *testing.T) {
	// free+inactive+speculative+purgeable = 384989+751098+12535+12468 = 1161090 pages.
	// At 16 KiB: 1161090 * 16384 / 1MiB = 18141 MB. A hardcoded 4096 would report
	// ~4535 MB (4x under-count) — the bug that trips spurious CRITICAL on healthy hosts.
	got := parseVMStatFreeMB([]byte(vmStatAppleSilicon))
	const want = uint64(1161090) * 16384 / (1024 * 1024)
	if got != want {
		t.Fatalf("Apple Silicon: got %d MB, want %d MB (page-size 16384 not honored?)", got, want)
	}
	// Guard against the regression directly: must NOT match the 4096 under-count.
	const under = uint64(1161090) * 4096 / (1024 * 1024)
	if got == under {
		t.Fatalf("regression: free mem computed with hardcoded 4096 (%d MB)", under)
	}
}

func TestParseVMStatFreeMB_IntelPageSize(t *testing.T) {
	// free+inactive = 200000 pages at 4 KiB = 781 MB.
	got := parseVMStatFreeMB([]byte(vmStatIntel))
	const want = uint64(200000) * 4096 / (1024 * 1024)
	if got != want {
		t.Fatalf("Intel: got %d MB, want %d MB", got, want)
	}
}

func TestParseVMStatFreeMB_FallsBackWhenHeaderMissing(t *testing.T) {
	// No page-size header -> fall back to 4096, still sum pages (don't return 0).
	const noHeader = "Pages free:                              100000.\n"
	got := parseVMStatFreeMB([]byte(noHeader))
	const want = uint64(100000) * 4096 / (1024 * 1024)
	if got != want {
		t.Fatalf("missing header: got %d MB, want %d MB (4096 fallback)", got, want)
	}
}
