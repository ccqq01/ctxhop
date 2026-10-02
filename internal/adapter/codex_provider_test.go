package adapter

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestCodexRestoreProviderKeepsUserAndToolData(t *testing.T) {
	projectRoot := t.TempDir()
	layout := CodexLayout{Home: t.TempDir()}
	id := "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	records := [][]byte{
		codexTestRecord(t, "2026-09-30T10:00:00Z", "session_meta", map[string]any{
			"id": id, "cwd": projectRoot, "model_provider": "custom",
		}),
		codexTestRecord(t, "2026-09-30T10:00:01Z", "event_msg", map[string]any{
			"type": "thread_settings_applied", "thread_settings": map[string]any{"model_provider_id": "third-party", "model": "gpt-6-sol"},
		}),
		codexTestRecord(t, "2026-09-30T10:00:02Z", "response_item", map[string]any{
			"type": "message", "role": "user", "data": map[string]any{"model_provider": "custom", "model_provider_id": "custom"},
		}),
	}
	if err := layout.WriteSession(projectRoot, id, records); err != nil {
		t.Fatal(err)
	}
	stored, err := layout.ReadStoredSession(id)
	if err != nil {
		t.Fatal(err)
	}
	var meta, settings, user struct {
		Payload map[string]any `json:"payload"`
	}
	for i, target := range []*struct {
		Payload map[string]any `json:"payload"`
	}{&meta, &settings, &user} {
		if err := json.Unmarshal(stored[i], target); err != nil {
			t.Fatal(err)
		}
	}
	if _, found := meta.Payload["model_provider"]; found {
		t.Fatal("source session provider was retained")
	}
	threadSettings := settings.Payload["thread_settings"].(map[string]any)
	if _, found := threadSettings["model_provider_id"]; found {
		t.Fatal("source thread settings provider was retained")
	}
	if !bytes.Contains(stored[2], []byte(`"model_provider":"custom"`)) || !bytes.Contains(stored[2], []byte(`"model_provider_id":"custom"`)) {
		t.Fatalf("user data was changed: %s", stored[2])
	}
}

func TestCodexRestoreKeepsBuiltinOpenAIProvider(t *testing.T) {
	projectRoot := t.TempDir()
	layout := CodexLayout{Home: t.TempDir()}
	id := "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	records := [][]byte{
		codexTestRecord(t, "2026-09-30T10:00:00Z", "session_meta", map[string]any{
			"id": id, "cwd": projectRoot, "model_provider": "openai",
		}),
		codexTestRecord(t, "2026-09-30T10:00:01Z", "event_msg", map[string]any{
			"type": "thread_settings_applied", "thread_settings": map[string]any{"model_provider_id": "openai", "model": "gpt-6-sol"},
		}),
	}
	if err := layout.WriteSession(projectRoot, id, records); err != nil {
		t.Fatal(err)
	}
	stored, err := layout.ReadStoredSession(id)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored[0], records[0]) || !bytes.Equal(stored[1], records[1]) {
		t.Fatalf("built-in provider changed during restore: %s", stored)
	}
}
