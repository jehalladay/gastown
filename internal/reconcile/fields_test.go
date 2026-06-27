package reconcile

import (
	"encoding/json"
	"testing"
)

// finds the Resolution for a field, or fails.
func find(t *testing.T, rs []Resolution, field string) Resolution {
	t.Helper()
	for _, r := range rs {
		if r.Field == field {
			return r
		}
	}
	t.Fatalf("no resolution reported for %q (got %+v)", field, rs)
	return Resolution{}
}

func TestResolveConflicts_LWW(t *testing.T) {
	// theirs has a later updated_at => theirs wins all lww fields.
	ours := map[string]string{"title": "old", "updated_at": "2026-06-26 10:00:00"}
	theirs := map[string]string{"title": "new", "updated_at": "2026-06-26 12:00:00"}
	got, rep := ResolveConflicts(ours, theirs)
	if got["title"] != "new" {
		t.Fatalf("LWW: want theirs 'new', got %q", got["title"])
	}
	if r := find(t, rep, "title"); r.Winner != "theirs" {
		t.Fatalf("LWW winner: want theirs, got %q", r.Winner)
	}

	// equal timestamps => ours wins (deterministic tie-break)
	ours["updated_at"] = "2026-06-26 12:00:00"
	got2, _ := ResolveConflicts(ours, theirs)
	if got2["title"] != "old" {
		t.Fatalf("LWW tie: want ours 'old', got %q", got2["title"])
	}
}

func TestResolveConflicts_LWWUnverifiable(t *testing.T) {
	// missing updated_at => lww fields must PAUSE, not guess.
	ours := map[string]string{"title": "a"}
	theirs := map[string]string{"title": "b"}
	_, rep := ResolveConflicts(ours, theirs)
	if r := find(t, rep, "title"); r.Winner != "pause" {
		t.Fatalf("missing updated_at: want pause, got %q", r.Winner)
	}
}

func TestResolveConflicts_Immutable(t *testing.T) {
	ours := map[string]string{"content_hash": "aaa", "updated_at": "2026-06-26 10:00:00"}
	theirs := map[string]string{"content_hash": "bbb", "updated_at": "2026-06-26 12:00:00"}
	got, rep := ResolveConflicts(ours, theirs)
	if got["content_hash"] != "aaa" {
		t.Fatalf("immutable must keep ours, got %q", got["content_hash"])
	}
	if r := find(t, rep, "content_hash"); r.Winner != "flag" {
		t.Fatalf("immutable conflict: want flag, got %q", r.Winner)
	}
}

func TestResolveConflicts_OrFlag(t *testing.T) {
	ours := map[string]string{"pinned": "0", "updated_at": "2026-06-26 10:00:00"}
	theirs := map[string]string{"pinned": "1", "updated_at": "2026-06-26 09:00:00"}
	got, _ := ResolveConflicts(ours, theirs)
	if got["pinned"] != "1" {
		t.Fatalf("OR flag: true at either site => true, got %q", got["pinned"])
	}
}

func TestResolveConflicts_MaxCompaction(t *testing.T) {
	ours := map[string]string{"compaction_level": "1", "updated_at": "2026-06-26 12:00:00"}
	theirs := map[string]string{"compaction_level": "3", "updated_at": "2026-06-26 09:00:00"}
	got, _ := ResolveConflicts(ours, theirs)
	if got["compaction_level"] != "3" {
		t.Fatalf("compaction monotonic: want 3, got %q", got["compaction_level"])
	}
}

func TestResolveConflicts_DeferContent(t *testing.T) {
	ours := map[string]string{"status": "open", "updated_at": "2026-06-26 10:00:00"}
	theirs := map[string]string{"status": "closed", "updated_at": "2026-06-26 12:00:00"}
	got, rep := ResolveConflicts(ours, theirs)
	// must NOT LWW-flip status — §3 owns it; ours value left untouched here
	if got["status"] != "open" {
		t.Fatalf("status must defer to §3, not LWW-flip; got %q", got["status"])
	}
	if r := find(t, rep, "status"); r.Winner != "defer-content" {
		t.Fatalf("status winner: want defer-content, got %q", r.Winner)
	}
}

func TestResolveConflicts_MetadataDeepMerge(t *testing.T) {
	// disjoint keys union; colliding key takes LWW winner (theirs newer here).
	ours := map[string]string{"metadata": `{"a":1,"shared":"old"}`, "updated_at": "2026-06-26 10:00:00"}
	theirs := map[string]string{"metadata": `{"b":2,"shared":"new"}`, "updated_at": "2026-06-26 12:00:00"}
	got, _ := ResolveConflicts(ours, theirs)
	var m map[string]any
	if err := json.Unmarshal([]byte(got["metadata"]), &m); err != nil {
		t.Fatalf("metadata not valid json: %v", err)
	}
	if m["a"] == nil || m["b"] == nil {
		t.Fatalf("metadata must union keys, got %v", m)
	}
	if m["shared"] != "new" {
		t.Fatalf("metadata colliding key must take LWW winner 'new', got %v", m["shared"])
	}
}

func TestResolveConflicts_Unmodeled(t *testing.T) {
	ours := map[string]string{"some_new_col": "x", "updated_at": "2026-06-26 10:00:00"}
	theirs := map[string]string{"some_new_col": "y", "updated_at": "2026-06-26 12:00:00"}
	_, rep := ResolveConflicts(ours, theirs)
	if r := find(t, rep, "some_new_col"); r.Winner != "pause" {
		t.Fatalf("unmodeled col must pause, got %q", r.Winner)
	}
}

func TestResolveConflicts_NoConflictNoReport(t *testing.T) {
	ours := map[string]string{"title": "same", "updated_at": "2026-06-26 10:00:00"}
	theirs := map[string]string{"title": "same", "updated_at": "2026-06-26 12:00:00"}
	_, rep := ResolveConflicts(ours, theirs)
	for _, r := range rep {
		if r.Field == "title" {
			t.Fatalf("identical field must not be reported as a conflict")
		}
	}
}
