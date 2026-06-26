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
		filepath.Join(rigPath, "mayor", "rig"),
		filepath.Join(rigPath, "refinery", "rig"),
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
	rigPath := ctx.RigPath()
	if rigPath == "" {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusError,
			Message: "No rig specified",
		}
	}

	c.misconfiguredClones = nil

	for _, clonePath := range clonePathsForRig(rigPath) {
		if !isGitRepo(clonePath) {
			continue
		}
		out, err := exec.Command("git", "-C", clonePath, "config", "--get", "http.version").Output()
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
		relPath, _ := filepath.Rel(rigPath, clonePath)
		if relPath == "" {
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
		cmd := exec.Command("git", "-C", clonePath, "config", "http.version", git.HTTPVersionConfig)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to pin http.version for %s: %w", clonePath, err)
		}
	}
	return nil
}
