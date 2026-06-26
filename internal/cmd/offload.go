package cmd

import (
	"errors"
	"fmt"
	"os/exec"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/offload"
)

var (
	offloadNode     string
	offloadPush     string
	offloadBedrock  bool
	offloadFresh    bool
	offloadTimeout  int
	offloadProbeCap int
)

var offloadCmd = &cobra.Command{
	Use:     "offload <repo-url> <branch> <command>",
	GroupID: GroupServices,
	Short:   "Run a self-contained command on the cluster, off the local box",
	Long: `Dispatch a self-contained command to a science-account cluster node via SSM,
keeping heavy work off the local host. The command clones <repo-url>, checks out
<branch>, runs uv, then runs <command>; stdout/stderr return inline.

This wraps offload_eng's proven offload script suite (embedded in the binary, so
it runs host-independently on any node). Secrets travel as short-TTL presigned S3
URLs — never plaintext in the SSM log. Fail-closed: a failed/timed-out/auth-lapsed
job exits non-zero, never a false green.

Prerequisite: run 'gt offload setup' once per session to stage the gh PAT (and,
for --bedrock jobs, the Bedrock bearer) into the offload bucket.

Examples:
  gt offload setup
  gt offload https://github.com/org/repo main "make test"
  gt offload --node i-0abc --push results https://github.com/org/repo feat "make bench"
  gt offload --bedrock --fresh https://github.com/org/repo main "uv run verify.py"

Environment: AWS_PROFILE_SCIENCE, AWS_REGION, OFFLOAD_BUCKET, OFFLOAD_TIMEOUT,
PROBE_CAP (overridden by the flags below when set).`,
	Args:         cobra.MinimumNArgs(3),
	SilenceUsage: true,
	RunE:         runOffload,
}

var offloadSetupCmd = &cobra.Command{
	Use:          "setup",
	Short:        "Stage secrets (gh PAT + Bedrock bearer) to the offload bucket (run once per session)",
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return wrapOffloadExit(offload.Setup(offloadEnv()))
	},
}

func init() {
	offloadCmd.Flags().StringVarP(&offloadNode, "node", "n", "", "Pin a specific instance ID (default: auto-pick least-loaded)")
	offloadCmd.Flags().StringVarP(&offloadPush, "push", "p", "", "Also push a results branch 'offload-<suffix>'")
	offloadCmd.Flags().BoolVarP(&offloadBedrock, "bedrock", "b", false, "Stage the Bedrock bearer (for claude/Converse jobs)")
	offloadCmd.Flags().BoolVarP(&offloadFresh, "fresh", "f", false, "Bypass the uv cache (re-fetch git SDK deps)")
	offloadCmd.Flags().IntVar(&offloadTimeout, "timeout", 0, "Job timeout in seconds (default 1800; long jobs need more)")
	offloadCmd.Flags().IntVar(&offloadProbeCap, "probe-cap", 0, "Cap load-probing at N nodes during auto-pick (default 12)")
	offloadCmd.AddCommand(offloadSetupCmd)
	rootCmd.AddCommand(offloadCmd)
}

// offloadEnv maps flags that the embedded scripts read from the environment.
func offloadEnv() []string {
	var env []string
	if offloadTimeout > 0 {
		env = append(env, fmt.Sprintf("OFFLOAD_TIMEOUT=%d", offloadTimeout))
	}
	if offloadProbeCap > 0 {
		env = append(env, fmt.Sprintf("PROBE_CAP=%d", offloadProbeCap))
	}
	return env
}

func runOffload(cmd *cobra.Command, args []string) error {
	// Map flags to offload.sh's frozen flag contract (-n/-p/-b/-f), then the
	// positional <repo-url> <branch> <command>. getopts requires flags first.
	var scriptArgs []string
	if offloadNode != "" {
		scriptArgs = append(scriptArgs, "-n", offloadNode)
	}
	if offloadPush != "" {
		scriptArgs = append(scriptArgs, "-p", offloadPush)
	}
	if offloadBedrock {
		scriptArgs = append(scriptArgs, "-b")
	}
	if offloadFresh {
		scriptArgs = append(scriptArgs, "-f")
	}
	scriptArgs = append(scriptArgs, args...) // repo-url, branch, command

	return wrapOffloadExit(offload.Dispatch(scriptArgs, offloadEnv()))
}

// wrapOffloadExit converts the script's non-zero exit into a SilentExitError so
// the fail-closed code propagates to the shell without a spurious cobra message.
// The script already prints its own diagnostics to stderr.
func wrapOffloadExit(err error) error {
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return NewSilentExit(exitErr.ExitCode())
	}
	return err
}
