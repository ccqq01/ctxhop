package adapter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCodexDiscoveryPrefersLatestAppName(t *testing.T) {
	home, projectRoot := t.TempDir(), t.TempDir()
	id := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	path := filepath.Join(home, "sessions", "2026", "09", "30", "rollout-2026-09-30T10-20-30-"+id+".jsonl")
	if err := writeSessionAt(path, [][]byte{
		codexTestRecord(t, "2026-09-30T10:20:30Z", "session_meta", map[string]any{"id": id, "cwd": projectRoot}),
		codexTestRecord(t, "2026-09-30T10:20:31Z", "event_msg", map[string]any{"type": "user_message", "message": "generated prompt title"}),
	}); err != nil {
		t.Fatal(err)
	}
	index := "" +
		`{"id":"` + id + `","thread_name":"旧名称","updated_at":"2026-09-29T01:00:00Z"}` + "\n" +
		"not-json\n" +
		`{"id":"` + id + `","thread_name":"简历","updated_at":"2026-09-30T12:34:56Z"}` + "\n" +
		`{"id":"` + id + `","thread_name":"unfinished"`
	if err := os.WriteFile(filepath.Join(home, "session_index.jsonl"), []byte(index), 0o600); err != nil {
		t.Fatal(err)
	}
	refs, err := (CodexLayout{Home: home}).DiscoverSessions(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	wantTime := time.Date(2026, 9, 30, 12, 34, 56, 0, time.UTC)
	if len(refs) != 1 || refs[0].Title != "简历" || refs[0].TitleSource != "codex-app" || !refs[0].TitleUpdatedAt.Equal(wantTime) {
		t.Fatalf("Codex refs = %+v", refs)
	}
}

func TestCodexDiscoveryClearedAppNameFallsBackToPrompt(t *testing.T) {
	home, projectRoot := t.TempDir(), t.TempDir()
	id := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	path := filepath.Join(home, "sessions", "2026", "09", "30", "rollout-2026-09-30T10-20-30-"+id+".jsonl")
	if err := writeSessionAt(path, [][]byte{
		codexTestRecord(t, "2026-09-30T10:20:30Z", "session_meta", map[string]any{"id": id, "cwd": projectRoot}),
		codexTestRecord(t, "2026-09-30T10:20:31Z", "event_msg", map[string]any{"type": "user_message", "message": "generated prompt title"}),
	}); err != nil {
		t.Fatal(err)
	}
	index := `{"id":"` + id + `","thread_name":"Old name","updated_at":"2026-09-29T01:00:00Z"}` + "\n" +
		`{"id":"` + id + `","thread_name":"","updated_at":"2026-09-30T12:34:56Z"}` + "\n"
	if err := os.WriteFile(filepath.Join(home, "session_index.jsonl"), []byte(index), 0o600); err != nil {
		t.Fatal(err)
	}
	refs, err := (CodexLayout{Home: home}).DiscoverSessions(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].Title != "generated prompt title" || refs[0].TitleSource != "" {
		t.Fatalf("Codex refs = %+v", refs)
	}
}

func TestCodexDiscoveryKeepsLongExplicitAppName(t *testing.T) {
	home, projectRoot := t.TempDir(), t.TempDir()
	id := "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	path := filepath.Join(home, "sessions", "2026", "09", "30", "rollout-2026-09-30T10-20-30-"+id+".jsonl")
	if err := writeSessionAt(path, [][]byte{codexTestRecord(t, "2026-09-30T10:20:30Z", "session_meta", map[string]any{"id": id, "cwd": projectRoot})}); err != nil {
		t.Fatal(err)
	}
	name := strings.Repeat("项目", 60)
	index := `{"id":"` + id + `","thread_name":"` + name + `","updated_at":"2026-09-30T12:34:56Z"}` + "\n"
	if err := os.WriteFile(filepath.Join(home, "session_index.jsonl"), []byte(index), 0o600); err != nil {
		t.Fatal(err)
	}
	refs, err := (CodexLayout{Home: home}).DiscoverSessions(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].Title != name {
		t.Fatalf("explicit App title was truncated: %+v", refs)
	}
}
