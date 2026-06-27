#!/usr/bin/env bash
# Self-check for the gt_timeout portable-timeout helper (dolt-backup/dolt-archive
# run.sh). Asserts the perl fallback: fast->0, timeout->124, nonzero preserved.
# Run: bash plugins/_test/gt_timeout_test.sh
set -euo pipefail
gt_timeout() {
  local secs="$1"; shift
  perl -e '
    my $s = shift;
    my $pid = fork();
    if ($pid == 0) { exec @ARGV or exit 127; }
    local $SIG{ALRM} = sub { kill "TERM", $pid; sleep 2; kill "KILL", $pid; exit 124; };
    alarm $s; waitpid($pid, 0); exit($? >> 8);
  ' "$secs" "$@"
}
fail=0
gt_timeout 5 sleep 1 || { echo "FAIL: fast cmd"; fail=1; }
rc=0; gt_timeout 1 sleep 10 || rc=$?; [ "$rc" -eq 124 ] || { echo "FAIL: timeout!=124 ($rc)"; fail=1; }
rc=0; gt_timeout 5 bash -c 'exit 3' || rc=$?; [ "$rc" -eq 3 ] || { echo "FAIL: exit!=3 ($rc)"; fail=1; }
[ "$fail" -eq 0 ] && echo "gt_timeout: all checks PASS" || exit 1
