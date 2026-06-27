package daemon

import (
	"strconv"
	"strings"

	"github.com/steveyegge/gastown/internal/tmux"
)

// F10 Phase-2: proactive auto-shed under critical swap pressure.
//
// Phase-1 (pressure.go) DEFERS new spawns when memory/CPU/session pressure is
// high. Phase-2 is the inverse ACTION: when swap usage crosses a CRITICAL
// threshold (kernel jetsam range), the daemon proactively PARKS idle crew —
// killing their tmux sessions — to free memory BEFORE the kernel OOM-kills a
// victim of its own choosing (historically Dolt, the data plane).
//
// The careful part is victim selection. The invariant: NEVER shed an active
// session and NEVER shed the data-plane/infra layer. Only idle, zero-active-bead
// crew/polecats are eligible. Selection is a pure function (selectShedVictims)
// so it is exhaustively testable without tmux or process kills.

// shedKeepRoles are roles that are NEVER shed regardless of idle state: the
// monitoring/recovery layer and anything adjacent to the data plane. Mirrors
// pressure.go's "infrastructure agents are not gated" list, extended for shed.
var shedKeepRoles = map[string]bool{
	"mayor":    true,
	"witness":  true,
	"refinery": true,
	"deacon":   true,
	"boot":     true,
	"dog":      true,
}

// ShedCandidate is the per-session input to victim selection.
type ShedCandidate struct {
	// Session is the tmux session name (the kill target).
	Session string
	// Role is the agent role (mayor/witness/refinery/crew/polecat/...).
	Role string
	// Idle is true if the session is idle at the prompt (NOT busy processing).
	Idle bool
	// ActiveBeads is the count of work beads currently hooked/assigned.
	ActiveBeads int
}

// shedEligible reports whether a single candidate may be parked. An eligible
// victim is: not an infra/data-plane role, idle at prompt, and holding zero
// active work beads. This is fail-safe — any uncertainty (busy, has work, or a
// role we don't recognize as crew/polecat) keeps the session alive.
func (c ShedCandidate) shedEligible() bool {
	if shedKeepRoles[c.Role] {
		return false
	}
	// Only crew and polecats are ever sheddable. An unrecognized role is kept
	// (fail-safe): we never kill something we can't classify.
	if c.Role != "crew" && c.Role != "polecat" {
		return false
	}
	if !c.Idle {
		return false
	}
	if c.ActiveBeads > 0 {
		return false
	}
	return true
}

// selectShedVictims returns the session names to park, capped at maxToShed.
// Pure: no side effects, no I/O. Order of `in` is preserved so the caller can
// pre-sort by value (lowest-value-idle first) if desired.
func selectShedVictims(in []ShedCandidate, maxToShed int) []string {
	if maxToShed <= 0 {
		return nil
	}
	var victims []string
	for _, c := range in {
		if len(victims) >= maxToShed {
			break
		}
		if c.shedEligible() {
			victims = append(victims, c.Session)
		}
	}
	return victims
}

// parseSwapUsagePercent extracts used/total from macOS `sysctl -n vm.swapusage`
// output and returns used as a percent of total. Returns ok=false when total is
// zero or unparseable — a no-swap or unreadable host must NOT report pressure.
// Format: "total = 7168.00M  used = 5694.81M  free = 1473.19M  (encrypted)".
//
// ponytail: the "M" suffix is the only unit macOS emits here; if a future macOS
// emits G/K, divide by the same scale for used+total — the RATIO is unit-free.
func parseSwapUsagePercent(line string) (float64, bool) {
	field := func(key string) (float64, bool) {
		i := strings.Index(line, key)
		if i < 0 {
			return 0, false
		}
		rest := strings.TrimSpace(line[i+len(key):])
		// rest looks like "7168.00M  used = ..."; take the first token.
		tok := strings.Fields(rest)
		if len(tok) == 0 {
			return 0, false
		}
		num := strings.TrimRight(tok[0], "MGKmgk")
		v, err := strconv.ParseFloat(num, 64)
		if err != nil {
			return 0, false
		}
		return v, true
	}
	total, okT := field("total =")
	used, okU := field("used =")
	if !okT || !okU || total <= 0 {
		return 0, false
	}
	return used / total * 100, true
}

// shedMaxPerTick caps how many sessions a single shed pass parks, so the daemon
// frees memory in measured steps and re-reads swap on the next tick rather than
// nuking the whole crew at once.
// ponytail: fixed small cap; make it config if a host needs faster bleed-off.
const shedMaxPerTick = 3

// checkSwapShed is the F10 Phase-2 tick action: if swap usage is at/above the
// configured critical percent, park idle/zero-bead crew+polecats (capped per
// tick) to free memory before the kernel OOM-kills the data plane. Disabled
// (returns immediately) when the threshold is 0 — the opt-in default, matching
// Phase-1. Returns the sessions it parked (nil if none / disabled).
func (d *Daemon) checkSwapShed() []string {
	threshold := d.loadOperationalConfig().GetDaemonConfig().ShedSwapCriticalPercentV()
	if threshold <= 0 {
		return nil
	}
	pct := swapUsedPercent()
	if pct < 0 || pct < threshold {
		return nil // unavailable, or below critical — never shed
	}

	candidates := d.gatherShedCandidates()
	victims := selectShedVictims(candidates, shedMaxPerTick)
	if len(victims) == 0 {
		d.logger.Printf("shed: swap %.1f%% >= critical %.1f%% but no idle/0-bead crew to park (data plane protected)", pct, threshold)
		return nil
	}

	t := tmux.NewTmux()
	parked := make([]string, 0, len(victims))
	for _, v := range victims {
		if err := t.KillSession(v); err != nil {
			d.logger.Printf("shed: failed to park %s: %v", v, err)
			continue
		}
		parked = append(parked, v)
		d.logger.Printf("shed: parked idle session %s (swap %.1f%% >= critical %.1f%%)", v, pct, threshold)
	}
	return parked
}

// gatherShedCandidates builds the shed roster from live tmux sessions. A session
// is a candidate input with: its parsed role, idle state (tmux.IsIdle = idle at
// prompt, not busy), and active-bead count. Fail-safe: any session we can't
// parse or whose bead state is uncertain is recorded as NOT idle / has-work so
// selectShedVictims keeps it alive.
func (d *Daemon) gatherShedCandidates() []ShedCandidate {
	t := tmux.NewTmux()
	sessions, err := t.ListSessions()
	if err != nil {
		return nil
	}
	out := make([]ShedCandidate, 0, len(sessions))
	for _, name := range sessions {
		if !isAgentSession(name) {
			continue
		}
		parsed, err := parseIdentity(name)
		if err != nil || parsed == nil {
			// Unparseable agent session — keep it alive (record as non-idle).
			out = append(out, ShedCandidate{Session: name, Role: "", Idle: false, ActiveBeads: 1})
			continue
		}
		out = append(out, ShedCandidate{
			Session:     name,
			Role:        parsed.RoleType,
			Idle:        t.IsIdle(name),
			ActiveBeads: d.sessionActiveBeadCount(name),
		})
	}
	return out
}

// sessionActiveBeadCount returns the number of active (open, hooked) work beads
// for a session. Conservative: on any lookup error it returns 1 (treat as
// having work) so the session is never shed on incomplete info.
func (d *Daemon) sessionActiveBeadCount(sessionName string) int {
	agentBeadID := d.identityToAgentBeadID(sessionName)
	if agentBeadID == "" {
		return 1 // can't resolve identity → assume working, don't shed
	}
	info, err := d.getAgentBeadInfo(agentBeadID)
	if err != nil {
		return 1 // lookup failed → assume working, don't shed
	}
	if info.HookBead == "" {
		return 0
	}
	// A hooked bead that's already closed doesn't count as active work.
	if d.isBeadClosed(info.HookBead) {
		return 0
	}
	return 1
}
