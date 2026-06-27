#!/bin/bash
# L1 shell test for F10 — the Dolt memory/swap-pressure guard (agentic-TDD: written
# FIRST, before the guard, and it must FAIL for the right reason until the guard
# exists). Verifies the OUTSIDE contract: `gt doctor` surfaces memory pressure, and
# a simulated high-swap state makes the guard WARN (and at critical, shed/park idle
# crew) BEFORE the jetsam threshold — not after.
#
# Hermetic via injection: the memory-pressure reader honors test-override env vars
# (GT_MEMPRESSURE_TEST_SWAP_PCT, GT_MEMPRESSURE_TEST_FREE_MB) so we can simulate
# pressure without actually exhausting the host. No real OOM induced.
#
# Usage: scripts/test-mempressure-guard.sh
set -uo pipefail

FAILS=0
pass() { echo "  PASS: $1"; }
fail() { echo "  FAIL: $1"; FAILS=$((FAILS+1)); }

TEST_DIR="$(mktemp -d /tmp/gt-mempressure-test-XXXX)"
cleanup() { rm -rf "$TEST_DIR"; }
trap cleanup EXIT

echo "=== Building gt..."
# Build with BuiltProperly=1 — macOS SIGKILLs unsigned plain `go build` gt binaries
# (the darwin build-provenance guard), so the binary must carry the ldflag to RUN.
CGO_ENABLED=0 go build -tags gms_pure_go \
  -ldflags "-X github.com/steveyegge/gastown/internal/cmd.BuiltProperly=1" \
  -o "$TEST_DIR/gt" ./cmd/gt || { echo "BUILD FAILED"; exit 1; }
GT="$TEST_DIR/gt"
echo "OK gt built"

echo ""
echo "=== F10: memory/swap-pressure guard contract ==="

# 1. gt doctor includes a memory-pressure check (registered).
if "$GT" doctor --help 2>&1 | grep -qi "memory-pressure\|mem-pressure\|swap"; then
  pass "gt doctor lists a memory-pressure check"
else
  fail "gt doctor has no memory-pressure check (F10 not implemented)"
fi

# 2. SIMULATED HIGH SWAP -> WARN. Inject 92% swap; the guard must warn (below the
#    ~95%+ jetsam-risk critical line but above the warn threshold).
out="$(GT_MEMPRESSURE_TEST_SWAP_PCT=92 "$GT" doctor --rig "" 2>&1 || true)"
if echo "$out" | grep -qi "memory\|swap" && echo "$out" | grep -qi "warn"; then
  pass "92% swap -> guard WARNs"
else
  fail "92% swap did not produce a memory/swap warning; out: $(echo "$out" | grep -i 'memory\|swap' | head -3)"
fi

# 3. SIMULATED CRITICAL SWAP -> CRITICAL (the shed/park trigger point). Inject 97%.
out="$(GT_MEMPRESSURE_TEST_SWAP_PCT=97 "$GT" doctor --rig "" 2>&1 || true)"
if echo "$out" | grep -qi "memory\|swap" && echo "$out" | grep -qiE "critical|shed|park"; then
  pass "97% swap -> guard CRITICAL (shed/park trigger)"
else
  fail "97% swap did not produce a critical memory state; out: $(echo "$out" | grep -i 'memory\|swap' | head -3)"
fi

# 4. NORMAL -> OK (no false positive). Inject 10% swap.
out="$(GT_MEMPRESSURE_TEST_SWAP_PCT=10 "$GT" doctor --rig "" 2>&1 || true)"
if echo "$out" | grep -qi "memory-pressure\|mem-pressure" && echo "$out" | grep -qi "ok\|✓\|passed"; then
  pass "10% swap -> guard OK (no false positive)"
else
  # Acceptable if the check simply doesn't warn at 10% (no memory/swap warning line).
  if echo "$out" | grep -i "memory\|swap" | grep -qi "warn\|critical"; then
    fail "10% swap wrongly warned/critical; out: $(echo "$out" | grep -i 'memory\|swap' | head -3)"
  else
    pass "10% swap -> no warning (guard OK)"
  fi
fi

echo ""
if [ $FAILS -eq 0 ]; then
  echo "=== ALL F10 guard checks passed ==="
  exit 0
fi
echo "=== $FAILS check(s) failed (expected RED until the guard is implemented) ==="
exit 1
