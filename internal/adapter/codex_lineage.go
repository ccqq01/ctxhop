package adapter

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// CodexHistoryBaseRef identifies the immediately preceding physical rollout
// in a Codex paginated conversation. The byte offset is machine-local; the
// ordinal and thread ID identify the logical cutoff across devices.
type CodexHistoryBaseRef struct {
	ThreadID            string `json:"thread_id"`
	EndOrdinalExclusive uint64 `json:"end_ordinal_exclusive"`
	EndByteOffset       uint64 `json:"end_byte_offset"`
}

// CodexHistoryBase reads only the first session_meta record. A nil history
// base marks a standalone rollout; malformed metadata must not be treated as
// standalone because that would publish or restore incomplete history.
func CodexHistoryBase(records [][]byte) (CodexHistoryBaseRef, bool, error) {
	if len(records) == 0 {
		return CodexHistoryBaseRef{}, false, errors.New("Codex history is empty")
	}
	var head struct {
		Type    string `json:"type"`
		Payload struct {
			HistoryBase *CodexHistoryBaseRef `json:"history_base"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(records[0], &head); err != nil {
		return CodexHistoryBaseRef{}, false, fmt.Errorf("parse Codex session metadata: %w", err)
	}
	if head.Type != "session_meta" {
		return CodexHistoryBaseRef{}, false, errors.New("Codex history does not start with session_meta")
	}
	if head.Payload.HistoryBase == nil {
		return CodexHistoryBaseRef{}, false, nil
	}
	base := *head.Payload.HistoryBase
	if strings.TrimSpace(base.ThreadID) == "" || base.EndOrdinalExclusive == 0 {
		return CodexHistoryBaseRef{}, false, errors.New("Codex history has an invalid parent reference")
	}
	return base, true, nil
}

// RebaseCodexHistoryBase recomputes a child's byte cutoff against the exact
// bytes that will be written for its parent on this device. Codex rollouts are
// JSONL files, with one newline after every record.
func RebaseCodexHistoryBase(records, parentRecords [][]byte) ([][]byte, error) {
	base, found, err := CodexHistoryBase(records)
	if err != nil {
		return nil, err
	}
	out := append([][]byte(nil), records...)
	if !found {
		return out, nil
	}
	if len(parentRecords) == 0 {
		return nil, errors.New("Codex history parent is empty")
	}
	var byteOffset uint64
	var previous uint64
	var havePrevious bool
	var included bool
	for index, raw := range parentRecords {
		var line struct {
			Ordinal *uint64 `json:"ordinal"`
		}
		if err := json.Unmarshal(raw, &line); err != nil || line.Ordinal == nil {
			return nil, fmt.Errorf("Codex history parent record %d has no valid ordinal", index+1)
		}
		ordinal := *line.Ordinal
		if havePrevious && ordinal <= previous {
			return nil, fmt.Errorf("Codex history parent ordinal is not increasing at record %d", index+1)
		}
		previous, havePrevious = ordinal, true
		if ordinal >= base.EndOrdinalExclusive {
			continue
		}
		included = true
		byteOffset += uint64(len(raw)) + 1
	}
	if !included || previous < base.EndOrdinalExclusive-1 {
		return nil, errors.New("Codex history cutoff is beyond the available parent records")
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(records[0], &envelope); err != nil {
		return nil, fmt.Errorf("parse Codex child metadata: %w", err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(envelope["payload"], &payload); err != nil {
		return nil, fmt.Errorf("parse Codex child payload: %w", err)
	}
	base.EndByteOffset = byteOffset
	updatedBase, err := json.Marshal(base)
	if err != nil {
		return nil, err
	}
	payload["history_base"] = updatedBase
	updatedPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	envelope["payload"] = updatedPayload
	updatedHead, err := json.Marshal(envelope)
	if err != nil {
		return nil, err
	}
	out[0] = updatedHead
	return out, nil
}
