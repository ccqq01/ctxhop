package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CCCCY-ci/ctxhop/internal/adapter"
	"github.com/CCCCY-ci/ctxhop/internal/crypto"
	"github.com/CCCCY-ci/ctxhop/internal/remote"
	"github.com/CCCCY-ci/ctxhop/internal/syncer"
	"github.com/CCCCY-ci/ctxhop/internal/syncflow"
)

type rejectParentRemote struct {
	remote.Remote
	parentSessionID string
}

func (r rejectParentRemote) Put(ctx context.Context, key string, body io.Reader, size int64) error {
	if strings.Contains(key, "/sessions/"+r.parentSessionID+"/") {
		return remote.ErrPermission
	}
	return r.Remote.Put(ctx, key, body, size)
}

func TestPushCodexLineageStopsAfterParentUploadFails(t *testing.T) {
	projectRoot, home, stateRoot := t.TempDir(), t.TempDir(), t.TempDir()
	layout := adapter.CodexLayout{Home: home}
	parentID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	childID := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	if err := layout.WriteSession(projectRoot, parentID, [][]byte{
		pushLineageRecord(t, 0, "session_meta", map[string]any{"id": parentID, "cwd": projectRoot}),
		pushLineageRecord(t, 1, "event_msg", map[string]any{"type": "user_message", "message": "parent"}),
	}); err != nil {
		t.Fatal(err)
	}
	if err := layout.WriteSession(projectRoot, childID, [][]byte{
		pushLineageRecord(t, 2, "session_meta", map[string]any{
			"id": childID, "cwd": projectRoot, "history_base": map[string]any{"thread_id": parentID, "end_ordinal_exclusive": 2, "end_byte_offset": 0},
		}),
		pushLineageRecord(t, 3, "event_msg", map[string]any{"type": "user_message", "message": "child"}),
	}); err != nil {
		t.Fatal(err)
	}
	refs, err := layout.DiscoverSessions(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	ordered, serial, err := expandCodexPushLineage(layout, refs, childID)
	if err != nil || !serial {
		t.Fatalf("lineage refs: serial=%t err=%v", serial, err)
	}
	dataKey := crypto.NewDataKey()
	defer dataKey.Close()
	public, err := dataKey.IdentityPublic()
	if err != nil {
		t.Fatal(err)
	}
	identifierKey, err := dataKey.IdentifierKey()
	if err != nil {
		t.Fatal(err)
	}
	const projectID = "projectone"
	parentSessionID, err := crypto.SessionID(identifierKey, projectID, parentID)
	if err != nil {
		t.Fatal(err)
	}
	store, err := remote.NewDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	queue, err := syncer.NewQueueStore(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	pusher, err := syncflow.NewQueuedPusher(queue, syncer.DefaultRetryPolicy(), classifyPushFailure)
	if err != nil {
		t.Fatal(err)
	}
	space := adapter.PathSpace{ProjectRoot: projectRoot, AgentHome: home}
	installation := adapter.Installation{DataDir: home, Compatibility: adapter.CompatFull}
	summary := pushDiscoveredSessionsWithOptions(t.Context(), "deviceone", identifierKey, projectID, "", layout, installation, space, rejectParentRemote{store, parentSessionID}, public, pusher, stateRoot, projectRoot, ordered, pushSessionOptions{serialLineage: serial})
	if summary.Pushed != 0 || summary.Failed != 1 || summary.Skipped != 1 {
		t.Fatalf("push continued after parent failed: %+v", summary)
	}
}

func TestPushCodexLineageRejectsIncompleteParentTail(t *testing.T) {
	projectRoot := t.TempDir()
	layout := adapter.CodexLayout{Home: t.TempDir()}
	parentID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	childID := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	if err := layout.WriteSession(projectRoot, parentID, [][]byte{
		pushLineageRecord(t, 0, "session_meta", map[string]any{"id": parentID, "cwd": projectRoot}),
		pushLineageRecord(t, 1, "event_msg", map[string]any{"type": "user_message", "message": "parent"}),
	}); err != nil {
		t.Fatal(err)
	}
	if err := layout.WriteSession(projectRoot, childID, [][]byte{pushLineageRecord(t, 2, "session_meta", map[string]any{
		"id": childID, "cwd": projectRoot, "history_base": map[string]any{"thread_id": parentID, "end_ordinal_exclusive": 2, "end_byte_offset": 0},
	})}); err != nil {
		t.Fatal(err)
	}
	path, err := layout.ReadStoredSession(parentID)
	if err != nil || len(path) != 2 {
		t.Fatalf("read parent = %d records, err=%v", len(path), err)
	}
	refs, err := layout.DiscoverSessions(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	// ReplaceSession writes complete records; append an unterminated tail using
	// the discovered parent file path through the public dated-session tree.
	for _, ref := range refs {
		if ref.NativeID != parentID {
			continue
		}
		matches, err := filepath.Glob(filepath.Join(layout.SessionsDir(), "*", "*", "*", "*"+parentID+".jsonl"))
		if err != nil || len(matches) != 1 {
			t.Fatalf("parent path matches=%v err=%v", matches, err)
		}
		f, err := os.OpenFile(matches[0], os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString(`{"type":`); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := expandCodexPushLineage(layout, refs, childID); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("accepted incomplete source parent: %v", err)
	}
}

func TestPrepareCodexPushRefsKeepsHealthySessionsWhenOneLineageIsBroken(t *testing.T) {
	projectRoot := t.TempDir()
	layout := adapter.CodexLayout{Home: t.TempDir()}
	goodID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	brokenID := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	if err := layout.WriteSession(projectRoot, goodID, [][]byte{pushLineageRecord(t, 0, "session_meta", map[string]any{"id": goodID, "cwd": projectRoot})}); err != nil {
		t.Fatal(err)
	}
	if err := layout.WriteSession(projectRoot, brokenID, [][]byte{pushLineageRecord(t, 2, "session_meta", map[string]any{
		"id": brokenID, "cwd": projectRoot, "history_base": map[string]any{"thread_id": "cccccccc-cccc-4ccc-8ccc-cccccccccccc", "end_ordinal_exclusive": 2, "end_byte_offset": 0},
	})}); err != nil {
		t.Fatal(err)
	}
	refs, err := layout.DiscoverSessions(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	ordered, serial, failures := prepareCodexPushRefs(layout, refs, "")
	if len(failures) != 1 || serial || len(ordered) != 1 || ordered[0].NativeID != goodID {
		t.Fatalf("ordered=%+v serial=%t failures=%v", ordered, serial, failures)
	}
}

func TestPushCodexLineageExpandsSelectedChildParentFirst(t *testing.T) {
	projectRoot := t.TempDir()
	layout := adapter.CodexLayout{Home: t.TempDir()}
	parentID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	childID := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	if err := layout.WriteSession(projectRoot, parentID, [][]byte{
		pushLineageRecord(t, 0, "session_meta", map[string]any{"id": parentID, "cwd": projectRoot}),
		pushLineageRecord(t, 1, "event_msg", map[string]any{"type": "user_message", "message": "parent"}),
	}); err != nil {
		t.Fatal(err)
	}
	if err := layout.WriteSession(projectRoot, childID, [][]byte{
		pushLineageRecord(t, 2, "session_meta", map[string]any{
			"id": childID, "cwd": projectRoot, "history_mode": "paginated",
			"history_base": map[string]any{"thread_id": parentID, "end_ordinal_exclusive": 2, "end_byte_offset": 111},
		}),
		pushLineageRecord(t, 3, "event_msg", map[string]any{"type": "user_message", "message": "child"}),
	}); err != nil {
		t.Fatal(err)
	}
	refs, err := layout.DiscoverSessions(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	ordered, hasLineage, err := expandCodexPushLineage(layout, refs, childID)
	if err != nil {
		t.Fatal(err)
	}
	if !hasLineage || len(ordered) != 2 || ordered[0].NativeID != parentID || ordered[1].NativeID != childID {
		t.Fatalf("ordered refs = %+v, hasLineage=%t", ordered, hasLineage)
	}
}

func TestPushCodexLineageRejectsMissingAndCyclicParent(t *testing.T) {
	for _, tc := range []struct{ name, parentID string }{
		{name: "missing", parentID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"},
		{name: "self-cycle", parentID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			projectRoot := filepath.Join(t.TempDir(), "project")
			layout := adapter.CodexLayout{Home: t.TempDir()}
			childID := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
			if err := layout.WriteSession(projectRoot, childID, [][]byte{pushLineageRecord(t, 2, "session_meta", map[string]any{
				"id": childID, "cwd": projectRoot, "history_mode": "paginated",
				"history_base": map[string]any{"thread_id": tc.parentID, "end_ordinal_exclusive": 2, "end_byte_offset": 111},
			})}); err != nil {
				t.Fatal(err)
			}
			refs, err := layout.DiscoverSessions(projectRoot)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := expandCodexPushLineage(layout, refs, childID); err == nil || (!strings.Contains(err.Error(), "parent") && !strings.Contains(err.Error(), "cycle")) {
				t.Fatalf("expected parent/cycle error, got %v", err)
			}
		})
	}
}

func TestPushCodexLineageFailureExplainsSourceRepair(t *testing.T) {
	var summary pushSummary
	summary.failContext("codex", "codex-lineage", errors.New("missing parent"))
	if !strings.Contains(summary.failureDetails, "open the source conversation") {
		t.Fatalf("failure detail = %q", summary.failureDetails)
	}
}

func pushLineageRecord(t *testing.T, ordinal uint64, recordType string, payload map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"ordinal": ordinal, "type": recordType, "payload": payload, "timestamp": "2026-09-30T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
