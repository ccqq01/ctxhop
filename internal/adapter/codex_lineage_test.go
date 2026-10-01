package adapter

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

func TestCodexHistoryBaseAndCutoff(t *testing.T) {
	parentID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	parent := [][]byte{
		lineageRecord(t, 0, "session_meta", map[string]any{"id": parentID}),
		lineageRecord(t, 1, "event_msg", map[string]any{"type": "user_message", "message": "before fork"}),
		lineageRecord(t, 2, "event_msg", map[string]any{"type": "user_message", "message": "after fork"}),
	}
	child := [][]byte{
		lineageRecord(t, 2, "session_meta", map[string]any{
			"id":           "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
			"history_mode": "paginated",
			"history_base": map[string]any{
				"thread_id": parentID, "end_ordinal_exclusive": 2, "end_byte_offset": 999,
			},
		}),
		lineageRecord(t, 3, "event_msg", map[string]any{"type": "user_message", "message": "child"}),
	}
	base, found, err := CodexHistoryBase(child)
	if err != nil || !found || base.ThreadID != parentID || base.EndOrdinalExclusive != 2 {
		t.Fatalf("history base = %+v, found=%t, err=%v", base, found, err)
	}
	localized, err := RebaseCodexHistoryBase(child, parent)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(child[0], lineageRecord(t, 2, "session_meta", map[string]any{
		"id": "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "history_mode": "paginated",
		"history_base": map[string]any{"thread_id": parentID, "end_ordinal_exclusive": 2, "end_byte_offset": 999},
	})) {
		t.Fatal("rebase mutated its input")
	}
	updated, found, err := CodexHistoryBase(localized)
	if err != nil || !found {
		t.Fatalf("updated history base: found=%t err=%v", found, err)
	}
	want := uint64(len(parent[0]) + 1 + len(parent[1]) + 1)
	if updated.EndByteOffset != want {
		t.Fatalf("rebased byte cutoff = %d, want %d", updated.EndByteOffset, want)
	}
	if _, err := RebaseCodexHistoryBase(child, parent[:1]); err == nil {
		t.Fatal("accepted a cutoff beyond the available parent records")
	}
}

func TestCodexCanonicalizesOnlySessionHistoryByteOffset(t *testing.T) {
	base := map[string]any{"thread_id": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "end_ordinal_exclusive": 2, "end_byte_offset": 999}
	meta := lineageRecord(t, 2, "session_meta", map[string]any{"history_base": base})
	user := lineageRecord(t, 3, "response_item", map[string]any{"role": "user", "history_base": base})
	c := NewCanonicalizer(PathSpace{ProjectRoot: "/source", AgentHome: "/agent"})
	canonicalMeta, err := c.Record(meta)
	if err != nil {
		t.Fatal(err)
	}
	canonicalUser, err := c.Record(user)
	if err != nil {
		t.Fatal(err)
	}
	parsed, found, err := CodexHistoryBase([][]byte{canonicalMeta})
	if err != nil || !found || parsed.EndByteOffset != 0 {
		t.Fatalf("canonical history base = %+v, found=%t, err=%v", parsed, found, err)
	}
	if !bytes.Contains(canonicalUser, []byte(`"end_byte_offset":999`)) {
		t.Fatalf("user content was changed: %s", canonicalUser)
	}
}

func TestCodexReadStoredSessionRejectsIncompleteTail(t *testing.T) {
	layout := CodexLayout{Home: t.TempDir()}
	projectRoot := t.TempDir()
	id := "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
	if err := layout.WriteSession(projectRoot, id, [][]byte{lineageRecord(t, 0, "session_meta", map[string]any{"id": id, "cwd": projectRoot})}); err != nil {
		t.Fatal(err)
	}
	path, err := layout.findSessionPath(id)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"type":`); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := layout.ReadStoredSession(id); err == nil {
		t.Fatal("accepted a parent rollout with an incomplete tail")
	}
}

func lineageRecord(t *testing.T, ordinal uint64, recordType string, payload map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"ordinal": ordinal, "type": recordType, "payload": payload, "timestamp": "2026-09-30T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
