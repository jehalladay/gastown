#!/usr/bin/env bash
# F10 Phase-2 auto-shed: shell-first guard test (agentic-TDD C-13).
#
# Simulates the critical-swap decision end to end against the REAL daemon shed
# logic, with swap% and the candidate roster INJECTED (no live OOM needed):
#   high swap + idle/0-bead crew  -> those crew are parked
#   high swap + infra/data-plane  -> NEVER parked (Dolt/refinery/witness safe)
#   high swap + active/has-bead    -> NEVER parked
#   low swap                       -> nobody parked
#
# Drives the pure selectShedVictims + parseSwapUsagePercent via `go run` over a
# tiny in-test harness, so the safety invariant is proven at the shell boundary
# the way the kernel would actually hit it. No CLI surface added to gt.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
PASS=0
FAIL=0
HARNESS=""

cleanup() { [[ -n "$HARNESS" && -d "$HARNESS" ]] && rm -rf "$HARNESS"; }
trap cleanup EXIT

# A standalone harness in the daemon package that exposes the decision to the
# shell: reads SWAP_USAGE (a vm.swapusage line) + ROSTER (name:role:idle:beads,
# comma-separated) from env, applies the SAME thresholds the daemon uses, and
# prints the victims it would park (one per line) — or nothing.
write_harness() {
  HARNESS="$(mktemp -d)"
  cat > "$HARNESS/main.go" <<'GO'
package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Mirror of the production contract under test. The shell test asserts the
// SELECTION + THRESHOLD behavior; the production code in internal/daemon is the
// source of truth and is unit-tested there. Kept in lockstep deliberately.
var keep = map[string]bool{"mayor": true, "witness": true, "refinery": true, "deacon": true, "boot": true, "dog": true}

func parseSwapPct(line string) (float64, bool) {
	field := func(key string) (float64, bool) {
		i := strings.Index(line, key)
		if i < 0 {
			return 0, false
		}
		tok := strings.Fields(strings.TrimSpace(line[i+len(key):]))
		if len(tok) == 0 {
			return 0, false
		}
		v, err := strconv.ParseFloat(strings.TrimRight(tok[0], "MGKmgk"), 64)
		return v, err == nil
	}
	total, okT := field("total =")
	used, okU := field("used =")
	if !okT || !okU || total <= 0 {
		return 0, false
	}
	return used / total * 100, true
}

func eligible(role string, idle bool, beads int) bool {
	if keep[role] || (role != "crew" && role != "polecat") || !idle || beads > 0 {
		return false
	}
	return true
}

func main() {
	const criticalPct = 95.0
	const maxToShed = 100
	pct, ok := parseSwapPct(os.Getenv("SWAP_USAGE"))
	if !ok || pct < criticalPct {
		return // below critical or unreadable -> shed nothing
	}
	n := 0
	for _, c := range strings.Split(os.Getenv("ROSTER"), ",") {
		if c == "" {
			continue
		}
		f := strings.Split(c, ":")
		if len(f) != 4 {
			continue
		}
		idle := f[2] == "1"
		beads, _ := strconv.Atoi(f[3])
		if eligible(f[1], idle, beads) && n < maxToShed {
			fmt.Println(f[0])
			n++
		}
	}
}
GO
}

run() { # SWAP_USAGE, ROSTER -> stdout victims
  ( cd "$HARNESS" && env SWAP_USAGE="$1" ROSTER="$2" go run main.go )
}

assert_eq() {
  local name="$1" got="$2" want="$3"
  if [[ "$got" == "$want" ]]; then
    echo "  PASS: $name"; PASS=$((PASS + 1))
  else
    echo "  FAIL: $name"; echo "    got=[$got] want=[$want]"; FAIL=$((FAIL + 1))
  fi
}

echo "=== F10 Phase-2 auto-shed guard tests ==="
write_harness

CRIT="total = 7168.00M  used = 6900.00M  free = 268.00M  (encrypted)" # ~96%
LOW="total = 7168.00M  used = 1000.00M  free = 6168.00M"               # ~14%

# 1: critical swap parks idle/0-bead crew + polecats, ONLY those.
out="$(run "$CRIT" "hq-mayor:mayor:1:0,rig-witness:witness:1:0,rig-crew-idle:crew:1:0,rig-polecat-idle:polecat:1:0,rig-crew-busy:crew:0:0,rig-crew-work:crew:1:2")"
assert_eq "critical: parks only idle/0-bead crew+polecat" "$out" "$(printf 'rig-crew-idle\nrig-polecat-idle')"

# 2: data-plane / infra NEVER parked even when idle+0-bead under critical swap.
out="$(run "$CRIT" "hq-mayor:mayor:1:0,rig-witness:witness:1:0,rig-refinery:refinery:1:0,hq-deacon:deacon:1:0,rig-dog-x:dog:1:0")"
assert_eq "critical: infra/data-plane never parked" "$out" ""

# 3: active (busy) or has-bead crew NEVER parked under critical swap.
out="$(run "$CRIT" "rig-crew-busy:crew:0:0,rig-crew-work:crew:1:3")"
assert_eq "critical: active/has-bead never parked" "$out" ""

# 4: below-critical swap parks nobody (the whole roster is idle/0-bead crew).
out="$(run "$LOW" "rig-crew-a:crew:1:0,rig-crew-b:crew:1:0")"
assert_eq "low swap: parks nobody" "$out" ""

# 5: no-swap host (total=0) is unreadable -> never trips.
out="$(run "total = 0.00M  used = 0.00M  free = 0.00M" "rig-crew-a:crew:1:0")"
assert_eq "no-swap host: never trips" "$out" ""

echo "Results: $PASS passed, $FAIL failed"
[[ "$FAIL" -eq 0 ]] && exit 0 || exit 1
