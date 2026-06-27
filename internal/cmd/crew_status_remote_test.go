package cmd

import (
	"testing"

	"github.com/steveyegge/gastown/internal/crew"
	"github.com/steveyegge/gastown/internal/rig"
	"github.com/steveyegge/gastown/internal/tmux"
)

// TestCrewStatusSurfacesRemoteNode is the F2 OBSERVABILITY gate (agentic-TDD): a
// crew spawned with --remote must be inspectable host-side — `gt crew status` shows
// WHICH node it runs on, without sshing the node. Asserts buildCrewStatusItems
// propagates CrewWorker.RemoteNode -> CrewStatusItem.RemoteNode. Written test-first;
// fails to compile until the RemoteNode fields exist.
func TestCrewStatusSurfacesRemoteNode(t *testing.T) {
	r := &rig.Rig{Name: "reactivecli", Path: t.TempDir()}
	workers := []*crew.CrewWorker{
		{Name: "research_bench", Rig: "reactivecli", ClonePath: t.TempDir(), RemoteNode: "i-0dbe6c30e1b878312"},
		{Name: "local_one", Rig: "reactivecli", ClonePath: t.TempDir()}, // no RemoteNode = local
	}

	items := buildCrewStatusItems(r, workers, tmux.NewTmux())
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	byName := map[string]CrewStatusItem{}
	for _, it := range items {
		byName[it.Name] = it
	}
	if got := byName["research_bench"].RemoteNode; got != "i-0dbe6c30e1b878312" {
		t.Errorf("remote crew RemoteNode = %q, want i-0dbe6c30e1b878312 (must be inspectable host-side)", got)
	}
	if got := byName["local_one"].RemoteNode; got != "" {
		t.Errorf("local crew RemoteNode = %q, want empty", got)
	}
}
