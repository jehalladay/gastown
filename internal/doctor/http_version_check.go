package doctor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/steveyegge/gastown/internal/git"
)

// HTTPVersionCheck verifies all clones pin http.version to HTTP/1.1.
// HTTP/2 + chunked-transfer corrupts the push pack on some networks (HTTP 400 /
// sideband-disconnect / false "everything-up-to-date"), which stalled pushes
// town-wide. gt sets this on every clone; this check catches pre-existing clones
// (and any that drifted) so a recurring transport outage can't masquerade as a
// server problem. See feature F5.
type HTTPVersionCheck struct {
	FixableCheck
	misconfiguredClones []string
}

// NewHTTPVersionCheck creates a new http.version check.
func NewHTTPVersionCheck() *HTTPVersionCheck {
	return &HTTPVersionCheck{
		FixableCheck: FixableCheck{
			BaseCheck: BaseCheck{
				CheckName:        "http-version-pinned",
				CheckDescription: "Check http.version=HTTP/1.1 is set for all clones",
				CheckCategory:    CategoryRig,
			},
		},
	}
}

// clonePathsForRig enumerates the git clones gt provisions under a rig.
func clonePathsForRig(rigPath string) []string {
	paths := []string{
		filepath.Join(rigPath, ".repo.git"), // bare repo — also pinned at clone time
		filepath.Join(rigPath, "mayor", "rig"),
		filepath.Join(rigPath, "refinery", "rig"),
		filepath.Join(rigPath, "witness", "rig"),
	}
	for _, sub := range []string{"crew", "polecats"} {
		dir := filepath.Join(rigPath, sub)
		if entries, err := os.ReadDir(dir); err == nil {
			for _, entry := range entries {
				if entry.IsDir() {
					paths = append(paths, filepath.Join(dir, entry.Name()))
				}
			}
		}
	}
	return paths
}

// clonePathsForTown enumerates clones across every rig under townRoot. Used when
// no --rig is given, so a town-wide `gt doctor` validates the pin globally
// (matching clone-divergence). Skips dotdirs, mayor, and docs at the top level.
func clonePathsForTown(townRoot string) []string {
	var paths []string
	entries, err := os.ReadDir(townRoot)
	if err != nil {
		return paths
	}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") ||
			entry.Name() == "mayor" || entry.Name() == "docs" {
			continue
		}
		paths = append(paths, clonePathsForRig(filepath.Join(townRoot, entry.Name()))...)
	}
	return paths
}

// isGitRepo reports whether path is a working clone (.git) or bare repo (HEAD).
func isGitRepo(path string) bool {
	if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
		return true
	}
	// Bare repos have HEAD at the top level.
	_, err := os.Stat(filepath.Join(path, "HEAD"))
	return err == nil
}

// Run checks if all clones have http.version pinned to HTTP/1.1.
func (c *HTTPVersionCheck) Run(ctx *CheckContext) *CheckResult {
	// Town-wide when no --rig (matches clone-divergence); per-rig when specified.
	var clonePaths []string
	if rigPath := ctx.RigPath(); rigPath != "" {
		clonePaths = clonePathsForRig(rigPath)
	} else {
		clonePaths = clonePathsForTown(ctx.TownRoot)
	}

	c.misconfiguredClones = nil

	for _, clonePath := range clonePaths {
		if !isGitRepo(clonePath) {
			continue
		}
		// --local, NOT --get: --get inherits the global ~/.gitconfig, so a host
		// with a global http.version would falsely report an unpinned clone as
		// green (and --fix would no-op). The whole point of the pin is to survive
		// global drift/absence, so we must assert the REPO-LOCAL value. (Found in
		// dogfood: false-green on hosts with a global http.version.)
		out, err := exec.Command("git", "-C", clonePath, "config", "--local", "--get", "http.version").Output()
		if err != nil || strings.TrimSpace(string(out)) != git.HTTPVersionConfig {
			c.misconfiguredClones = append(c.misconfiguredClones, clonePath)
		}
	}

	if len(c.misconfiguredClones) == 0 {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusOK,
			Message: "All clones pin http.version=HTTP/1.1",
		}
	}

	var details []string
	for _, clonePath := range c.misconfiguredClones {
		relPath, err := filepath.Rel(ctx.TownRoot, clonePath)
		if err != nil || relPath == "" {
			relPath = clonePath
		}
		details = append(details, relPath)
	}

	return &CheckResult{
		Name:    c.Name(),
		Status:  StatusWarning,
		Message: fmt.Sprintf("%d clone(s) not pinning http.version=HTTP/1.1 (HTTP/2 corrupts push packs)", len(c.misconfiguredClones)),
		Details: details,
		FixHint: "Run 'gt doctor --fix' to pin http.version=HTTP/1.1",
	}
}

// Fix pins http.version=HTTP/1.1 for all misconfigured clones.
func (c *HTTPVersionCheck) Fix(ctx *CheckContext) error {
	for _, clonePath := range c.misconfiguredClones {
		// --local explicit: write the repo-local pin (matches the --local read in
		// Run and the clone-time pin), never the global config.
		cmd := exec.Command("git", "-C", clonePath, "config", "--local", "http.version", git.HTTPVersionConfig)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to pin http.version for %s: %w", clonePath, err)
		}
	}
	return nil
}
