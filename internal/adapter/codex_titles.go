package adapter

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

const maxCodexAppTitleRunes = 256

type codexIndexedTitle struct {
	title     string
	updatedAt time.Time
}

// indexedThreadNames reads Codex's append-only thread-name index. It is
// optional metadata: a missing, busy or malformed index must not hide valid
// sessions. Only discovered IDs are retained in memory and the latest entry
// for each ID wins, including an empty name that clears an earlier rename.
func (l CodexLayout) indexedThreadNames(refs map[string]SessionRef) map[string]codexIndexedTitle {
	if len(refs) == 0 {
		return nil
	}
	file, err := os.Open(filepath.Join(l.Home, "session_index.jsonl"))
	if err != nil {
		return nil
	}
	defer file.Close() //nolint:errcheck // optional read-only metadata

	names := make(map[string]codexIndexedTitle)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for scanner.Scan() {
		var entry struct {
			ID         string `json:"id"`
			ThreadName string `json:"thread_name"`
			UpdatedAt  string `json:"updated_at"`
		}
		if json.Unmarshal(scanner.Bytes(), &entry) != nil {
			continue
		}
		if _, wanted := refs[entry.ID]; !wanted {
			continue
		}
		title := cleanCodexAppTitle(entry.ThreadName)
		if title == "" {
			delete(names, entry.ID)
			continue
		}
		when, _ := time.Parse(time.RFC3339Nano, entry.UpdatedAt)
		names[entry.ID] = codexIndexedTitle{title: title, updatedAt: when}
	}
	return names
}

func cleanCodexAppTitle(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	var safe strings.Builder
	for _, r := range value {
		if !unicode.IsControl(r) {
			safe.WriteRune(r)
		}
	}
	runes := []rune(safe.String())
	if len(runes) > maxCodexAppTitleRunes {
		return string(runes[:maxCodexAppTitleRunes-1]) + "…"
	}
	return string(runes)
}
