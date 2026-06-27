package doctor

import (
	"fmt"

	"github.com/steveyegge/gastown/internal/util"
)

// MemoryPressureCheck surfaces memory/swap pressure — the signal that precedes the
// macOS jetsam kills that twice nearly took the control plane (and Dolt) down this
// session. Disk space has a guard (DiskSpaceCheck); memory did not — this closes
// that gap (F10). At CRITICAL it points at shedding/parking idle crew BEFORE the
// kernel SIGKILLs a victim (which has repeatedly been Dolt).
type MemoryPressureCheck struct {
	BaseCheck
}

// NewMemoryPressureCheck creates a new memory/swap-pressure check.
func NewMemoryPressureCheck() *MemoryPressureCheck {
	return &MemoryPressureCheck{
		BaseCheck: BaseCheck{
			CheckName:        "memory-pressure",
			CheckDescription: "Check memory/swap pressure (warn/shed before jetsam bricks Dolt)",
			CheckCategory:    CategoryInfrastructure,
		},
	}
}

// Run evaluates memory/swap pressure.
func (c *MemoryPressureCheck) Run(_ *CheckContext) *CheckResult {
	info, err := util.GetMemoryPressure()
	if err != nil {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusWarning,
			Message: fmt.Sprintf("Could not check memory pressure: %v", err),
		}
	}

	level, msg, _ := util.CheckMemoryPressure()

	switch level {
	case util.MemoryCritical:
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusError,
			Message: msg,
			Details: []string{
				fmt.Sprintf("Swap %.1f%% used, %s reclaimable free",
					info.SwapUsedPercent, util.FormatBytesHuman(info.FreeMB*1024*1024)),
				"Jetsam (macOS) kills processes non-deterministically under this pressure — repeatedly Dolt",
				"SHED LOAD NOW: park idle crew ('gt crew stop <idle>') before the kernel picks the victim",
			},
			FixHint: "Park idle crew to shed memory, then re-check",
		}

	case util.MemoryWarning:
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusWarning,
			Message: msg,
			Details: []string{
				fmt.Sprintf("Swap %.1f%% used, %s reclaimable free",
					info.SwapUsedPercent, util.FormatBytesHuman(info.FreeMB*1024*1024)),
				"Reduce active polecats/crew before pressure reaches the jetsam range",
			},
		}

	default:
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusOK,
			Message: fmt.Sprintf("swap %.1f%% used, %s free", info.SwapUsedPercent, util.FormatBytesHuman(info.FreeMB*1024*1024)),
		}
	}
}
