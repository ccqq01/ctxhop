package adapter

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCodexLayoutDiscoversReadsAndWritesSessions(t *testing.T) {
	home := t.TempDir()
	projectRoot := filepath.Join(t.TempDir(), "source project")
	id := "11111111-1111-4111-8111-111111111111"
	path := filepath.Join(home, "sessions", "2026", "08", "20", "rollout-2026-08-20T10-20-30-"+id+".jsonl")
	arguments, err := json.Marshal(map[string]string{"file_path": filepath.Join(projectRoot, "main.go")})
	if err != nil {
		t.Fatal(err)
	}
	records := [][]byte{
		codexTestRecord(t, "2026-08-20T10:20:30Z", "session_meta", map[string]any{
			"session_id":  id,
			"cwd":         projectRoot,
			"cli_version": "0.148.0",
			"timestamp":   "2026-08-20T10:20:30Z",
		}),
		codexTestRecord(t, "2026-08-20T10:20:31Z", "event_msg", map[string]any{
			"type":    "user_message",
			"message": "continue the Codex adapter",
		}),
		codexTestRecord(t, "2026-08-20T10:20:32Z", "turn_context", map[string]any{
			"cwd":             projectRoot,
			"workspace_roots": []string{projectRoot},
		}),
		codexTestRecord(t, "2026-08-20T10:20:33Z", "response_item", map[string]any{
			"type":      "function_call",
			"name":      "apply_patch",
			"arguments": string(arguments),
		}),
	}
	if err := writeSessionAt(path, records); err != nil {
		t.Fatal(err)
	}

	layout := CodexLayout{Home: home}
	refs, err := layout.DiscoverSessions(projectRoot)
	if err != nil {
		t.Fatalf("DiscoverSessions: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("refs = %+v, want one session", refs)
	}
	ref := refs[0]
	if ref.Agent != "codex" || ref.NativeID != id || ref.ProjectPath != projectRoot {
		t.Fatalf("ref = %+v", ref)
	}
	if ref.Title != "continue the Codex adapter" || ref.Size == 0 {
		t.Fatalf("ref metadata = %+v", ref)
	}

	data, err := layout.ReadSession(ref)
	if err != nil || len(data.Records) != len(records) {
		t.Fatalf("ReadSession records=%d error=%v", len(data.Records), err)
	}
	accesses := layout.TouchedFiles(data.Records, projectRoot)
	if len(accesses) != 1 || accesses[0].Path != "main.go" || !accesses[0].Written {
		t.Fatalf("touched files = %+v", accesses)
	}

	newID := "22222222-2222-4222-8222-222222222222"
	newRecords := [][]byte{
		codexTestRecord(t, "2026-08-20T11:20:30Z", "session_meta", map[string]any{
			"session_id": newID,
			"cwd":        projectRoot,
			"timestamp":  "2026-08-20T11:20:30Z",
		}),
		codexTestRecord(t, "2026-08-20T11:20:31Z", "event_msg", map[string]any{
			"type":    "user_message",
			"message": "restored Codex session",
		}),
	}
	if err := layout.WriteSession(projectRoot, newID, newRecords); err != nil {
		t.Fatalf("WriteSession: %v", err)
	}
	if err := layout.WriteSession(projectRoot, newID, newRecords); !strings.Contains(err.Error(), "session already exists") {
		t.Fatalf("second WriteSession error = %v", err)
	}
	if err := layout.ReplaceSession(projectRoot, newID, newRecords); err != nil {
		t.Fatalf("ReplaceSession: %v", err)
	}
	written, err := layout.ReadSession(SessionRef{NativeID: newID})
	if err != nil || len(written.Records) != len(newRecords) {
		t.Fatalf("written records=%d error=%v", len(written.Records), err)
	}
}

func TestCodexTitleSkipsInternalContextAndKeepsUserPrompt(t *testing.T) {
	home := t.TempDir()
	projectRoot := filepath.Join(t.TempDir(), "project")
	id := "44444444-4444-4444-8444-444444444444"
	path := filepath.Join(home, "sessions", "2026", "08", "20", "rollout-2026-08-20T10-20-30-"+id+".jsonl")
	records := [][]byte{
		codexTestRecord(t, "2026-08-20T10:20:29Z", "session_meta", map[string]any{
			"session_id": id,
			"cwd":        projectRoot,
		}),
		codexTestRecord(t, "2026-08-20T10:20:30Z", "response_item", map[string]any{
			"role": "user", "content": []map[string]string{{"text": "<recommended_plugins> internal setup"}},
		}),
		codexTestRecord(t, "2026-08-20T10:20:31Z", "response_item", map[string]any{
			"role": "user", "content": []map[string]string{{"text": "帮我准备面试复盘"}},
		}),
	}
	if err := writeSessionAt(path, records); err != nil {
		t.Fatal(err)
	}
	refs, err := (CodexLayout{Home: home}).DiscoverSessions(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].Title != "帮我准备面试复盘" {
		t.Fatalf("refs = %+v, want the genuine user prompt as title", refs)
	}
}

func TestCodexLayoutSkipsAnotherProject(t *testing.T) {
	home := t.TempDir()
	projectRoot := filepath.Join(t.TempDir(), "project")
	otherRoot := filepath.Join(t.TempDir(), "other")
	id := "33333333-3333-4333-8333-333333333333"
	path := filepath.Join(home, "sessions", "2026", "08", "20", "rollout-2026-08-20T10-20-30-"+id+".jsonl")
	if err := writeSessionAt(path, [][]byte{codexTestRecord(t, "2026-08-20T10:20:30Z", "session_meta", map[string]any{
		"session_id": id,
		"cwd":        otherRoot,
	})}); err != nil {
		t.Fatal(err)
	}
	refs, err := (CodexLayout{Home: home}).DiscoverSessions(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 0 {
		t.Fatalf("refs = %+v, want no sessions from another project", refs)
	}
}

func TestCodexCanonicalizesWorkspaceRootsAndEmbeddedToolArguments(t *testing.T) {
	projectRoot := filepath.Join(t.TempDir(), "source project")
	agentHome := filepath.Join(t.TempDir(), "codex home")
	arguments, err := json.Marshal(map[string]string{"file_path": filepath.Join(projectRoot, "main.go")})
	if err != nil {
		t.Fatal(err)
	}
	record := codexTestRecord(t, "2026-08-20T10:20:30Z", "response_item", map[string]any{
		"workspace_roots": []string{projectRoot},
		"arguments":       string(arguments),
	})
	canonicalizer := NewCanonicalizer(PathSpace{ProjectRoot: projectRoot, AgentHome: agentHome})
	canonical, err := canonicalizer.Record(record)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(canonical), TokenProject) || strings.Contains(string(canonical), projectRoot) {
		t.Fatalf("canonical record = %s", canonical)
	}
	localized, err := Localize(canonical, PathSpace{ProjectRoot: filepath.Join(t.TempDir(), "target project"), AgentHome: filepath.Join(t.TempDir(), "target codex")})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(localized), TokenProject) {
		t.Fatalf("localized record still contains token: %s", localized)
	}
}

func TestCodexCanonicalizesPathKeyedChanges(t *testing.T) {
	projectRoot := filepath.Join(t.TempDir(), "source project")
	path := filepath.Join(projectRoot, "internal", "example.go")
	record := codexTestRecord(t, "2026-08-20T10:20:30Z", "event_msg", map[string]any{
		"item": map[string]any{
			"changes": map[string]any{
				path: map[string]any{"content": "snapshot"},
			},
		},
	})

	canonicalizer := NewCanonicalizer(PathSpace{ProjectRoot: projectRoot, AgentHome: filepath.Join(t.TempDir(), "codex")})
	canonical, err := canonicalizer.Record(record)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(canonical), TokenProject+"/internal/example.go") {
		t.Fatalf("canonical record did not rewrite changes key: %s", canonical)
	}
	if findings := canonicalizer.UnknownPathFields(); len(findings) != 0 {
		t.Fatalf("unexpected unknown path fields: %v", findings)
	}

	targetRoot := filepath.Join(t.TempDir(), "target project")
	localized, err := Localize(canonical, PathSpace{ProjectRoot: targetRoot, AgentHome: filepath.Join(t.TempDir(), "codex")})
	if err != nil {
		t.Fatal(err)
	}
	expectedPath := strings.ReplaceAll(filepath.Join(targetRoot, "internal", "example.go"), `\`, `\\`)
	if !strings.Contains(string(localized), expectedPath) {
		t.Fatalf("localized record did not restore changes key: %s", localized)
	}
}

func TestCodexDiscoveryUsesFileModificationTimeForUpdatedSession(t *testing.T) {
	home := t.TempDir()
	projectRoot := filepath.Join(t.TempDir(), "project")
	id := "55555555-5555-4555-8555-555555555555"
	path := filepath.Join(home, "sessions", "2026", "08", "20", "rollout-2026-08-20T10-20-30-"+id+".jsonl")
	records := [][]byte{
		codexTestRecord(t, "2026-08-20T10:20:30Z", "session_meta", map[string]any{
			"id":          id,
			"session_id":  "thread-5555",
			"cwd":         projectRoot,
			"cli_version": "0.148.0",
		}),
		codexTestRecord(t, "2026-08-20T10:20:31Z", "event_msg", map[string]any{
			"type":    "user_message",
			"message": "early title",
		}),
		codexTestRecord(t, "2026-08-20T10:21:45Z", "event_msg", map[string]any{
			"type": "task_complete",
		}),
	}
	if err := writeSessionAt(path, records); err != nil {
		t.Fatal(err)
	}
	latest := time.Date(2026, 8, 20, 10, 22, 0, 0, time.UTC)
	if err := os.Chtimes(path, latest, latest); err != nil {
		t.Fatal(err)
	}

	refs, err := (CodexLayout{Home: home}).DiscoverSessions(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 {
		t.Fatalf("refs = %+v, want one session", refs)
	}
	if !refs[0].UpdatedAt.Equal(latest) {
		t.Fatalf("updated = %s, want file modification time %s", refs[0].UpdatedAt, latest)
	}
}

func TestCodexDiscoveryKeepsPaginatedChildRolloutIdentity(t *testing.T) {
	home := t.TempDir()
	projectRoot := filepath.Join(t.TempDir(), "project")
	parentID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	childID := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	parentPath := filepath.Join(home, "sessions", "2026", "08", "19", "rollout-2026-08-19T10-20-30-"+parentID+".jsonl")
	path := filepath.Join(home, "sessions", "2026", "08", "20", "rollout-2026-08-20T10-20-30-"+parentID+"_"+childID+".jsonl")
	if err := writeSessionAt(parentPath, [][]byte{
		codexTestRecord(t, "2026-08-19T10:20:30Z", "session_meta", map[string]any{
			"id":   parentID,
			"cwd":  projectRoot,
			"type": "session_meta",
		}),
	}); err != nil {
		t.Fatal(err)
	}
	records := [][]byte{
		codexTestRecord(t, "2026-08-20T10:20:30Z", "session_meta", map[string]any{
			"id":   parentID,
			"cwd":  projectRoot,
			"type": "session_meta",
		}),
		codexTestRecord(t, "2026-08-20T10:20:31Z", "event_msg", map[string]any{
			"type":    "user_message",
			"message": "continued paginated history",
		}),
		codexTestRecord(t, "2026-08-20T10:20:32Z", "response_item", map[string]any{
			"history_base": map[string]string{"thread_id": parentID},
		}),
	}
	if err := writeSessionAt(path, records); err != nil {
		t.Fatal(err)
	}

	refs, err := (CodexLayout{Home: home}).DiscoverSessions(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 {
		t.Fatalf("refs = %+v, want parent and child rollouts", refs)
	}
	seen := map[string]bool{}
	for _, ref := range refs {
		seen[ref.NativeID] = true
	}
	if !seen[parentID] || !seen[childID] {
		t.Fatalf("refs = %+v, want parent %q and child %q", refs, parentID, childID)
	}
	for _, ref := range refs {
		if _, err := (CodexLayout{Home: home}).ReadSession(ref); err != nil {
			t.Fatalf("ReadSession(%s) error = %v", ref.NativeID, err)
		}
	}
}

func TestCodexReadSessionClampsStalePaginatedForkBoundary(t *testing.T) {
	home := t.TempDir()
	projectRoot := filepath.Join(t.TempDir(), "project")
	parentID := "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	childID := "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	parentPath := filepath.Join(home, "sessions", "2026", "08", "19", "rollout-2026-08-19T10-20-30-"+parentID+".jsonl")
	childPath := filepath.Join(home, "sessions", "2026", "08", "20", "rollout-2026-08-20T10-20-30-"+parentID+"_"+childID+".jsonl")
	parentRecords := [][]byte{codexTestRecord(t, "2026-08-19T10:20:30Z", "session_meta", map[string]any{
		"id": parentID, "cwd": projectRoot,
	})}
	childRecords := [][]byte{codexTestRecord(t, "2026-08-20T10:20:30Z", "session_meta", map[string]any{
		"id": childID, "cwd": projectRoot, "forked_from_id": parentID, "forked_from_ordinal_exclusive": 9,
	})}
	if err := writeSessionAt(parentPath, parentRecords); err != nil {
		t.Fatal(err)
	}
	if err := writeSessionAt(childPath, childRecords); err != nil {
		t.Fatal(err)
	}

	data, err := (CodexLayout{Home: home}).ReadSession(SessionRef{NativeID: childID, localPath: childPath})
	if err != nil {
		t.Fatal(err)
	}
	var record codexRecord
	if err := json.Unmarshal(data.Records[0], &record); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if got := int(payload["forked_from_ordinal_exclusive"].(float64)); got != 1 {
		t.Fatalf("fork boundary = %d, want source record count 1", got)
	}
}

func TestCodexWriteSessionStripsForeignCustomProvider(t *testing.T) {
	home := t.TempDir()
	projectRoot := filepath.Join(t.TempDir(), "project")
	id := "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
	records := [][]byte{codexTestRecord(t, "2026-08-20T10:20:30Z", "session_meta", map[string]any{
		"id": id, "cwd": projectRoot, "model_provider": "custom",
		"thread_settings": map[string]any{"model_provider_id": "custom", "model": "gpt-6-luna"},
	})}
	layout := CodexLayout{Home: home}
	if err := layout.WriteSession(projectRoot, id, records); err != nil {
		t.Fatal(err)
	}
	data, err := layout.ReadSession(SessionRef{NativeID: id})
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(data.Records[0], &value); err != nil {
		t.Fatal(err)
	}
	payload := value["payload"].(map[string]any)
	if _, ok := payload["model_provider"]; ok {
		t.Fatal("foreign model_provider was retained")
	}
	settings := payload["thread_settings"].(map[string]any)
	if _, ok := settings["model_provider_id"]; ok {
		t.Fatal("foreign model_provider_id was retained")
	}
}

func TestCodexDetectReadsRecordedVersionWithoutVersionGating(t *testing.T) {
	home := t.TempDir()
	id := "44444444-4444-4444-8444-444444444444"
	path := filepath.Join(home, "sessions", "2026", "08", "20", "rollout-2026-08-20T10-20-30-"+id+".jsonl")
	if err := writeSessionAt(path, [][]byte{codexTestRecord(t, "2026-08-20T10:20:30Z", "session_meta", map[string]any{
		"session_id":  id,
		"cwd":         t.TempDir(),
		"cli_version": "9.99.0",
	})}); err != nil {
		t.Fatal(err)
	}
	installation, err := (CodexLayout{Home: home}).Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if installation.Version != "9.99.0" || installation.Compatibility != CompatFull {
		t.Fatalf("installation = %+v", installation)
	}
}

func codexTestRecord(t *testing.T, timestamp, recordType string, payload map[string]any) []byte {
	t.Helper()
	record := map[string]any{"timestamp": timestamp, "type": recordType, "payload": payload}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestCodexDefaultHomeResolvesOverride(t *testing.T) {
	override := filepath.Join(t.TempDir(), "codex")
	t.Setenv("CODEX_HOME", override)
	got, err := DefaultCodexHome()
	if err != nil {
		t.Fatal(err)
	}
	if got != override {
		t.Fatalf("DefaultCodexHome = %q, want %q", got, override)
	}
	if _, err := os.Stat(got); !os.IsNotExist(err) {
		t.Fatalf("override unexpectedly exists: %v", err)
	}
}
