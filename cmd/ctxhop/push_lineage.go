package main

import (
	"fmt"

	"github.com/CCCCY-ci/ctxhop/internal/adapter"
)

// prepareCodexPushRefs keeps unrelated healthy sessions moving during a full
// push. A selected-session push remains strict: that child and every ancestor
// must be complete before any one of them is published.
func prepareCodexPushRefs(layout adapter.SessionLayout, refs []adapter.SessionRef, selectedID string) ([]adapter.SessionRef, bool, []error) {
	if selectedID != "" {
		ordered, serial, err := expandCodexPushLineage(layout, refs, selectedID)
		if err != nil {
			return nil, false, []error{err}
		}
		return ordered, serial, nil
	}
	ordered := make([]adapter.SessionRef, 0, len(refs))
	seen := make(map[string]bool, len(refs))
	var failures []error
	serial := false
	for _, ref := range refs {
		if seen[ref.NativeID] {
			continue
		}
		chain, hasLineage, err := expandCodexPushLineage(layout, refs, ref.NativeID)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		serial = serial || hasLineage
		for _, item := range chain {
			if !seen[item.NativeID] {
				seen[item.NativeID] = true
				ordered = append(ordered, item)
			}
		}
	}
	return ordered, serial, failures
}

// expandCodexPushLineage includes every physical rollout required by the
// selected Codex session, ordered from the oldest parent to the child. The
// project-filtered refs are the security boundary: a parent from another
// project is not silently published into this project's Remote namespace.
func expandCodexPushLineage(layout adapter.SessionLayout, refs []adapter.SessionRef, selectedID string) ([]adapter.SessionRef, bool, error) {
	if layout.Name() != "codex" {
		if selectedID == "" {
			return refs, false, nil
		}
		return filterPushSession(refs, selectedID), false, nil
	}
	byID := make(map[string]adapter.SessionRef, len(refs))
	for _, ref := range refs {
		byID[ref.NativeID] = ref
	}
	if selectedID != "" {
		if _, found := byID[selectedID]; !found {
			return nil, false, nil
		}
	}
	state := make(map[string]uint8, len(byID))
	ordered := make([]adapter.SessionRef, 0, len(byID))
	hasLineage := false
	var visit func(string, int) error
	visit = func(id string, depth int) error {
		if depth >= maxCodexLineageDepth {
			return fmt.Errorf("Codex history exceeds %d ancestors", maxCodexLineageDepth)
		}
		switch state[id] {
		case 1:
			return fmt.Errorf("Codex history cycle at native session %s", id)
		case 2:
			return nil
		}
		ref, found := byID[id]
		if !found {
			return fmt.Errorf("Codex history parent %s is unavailable in the current project", id)
		}
		state[id] = 1
		data, err := layout.ReadSession(ref)
		if err != nil {
			return fmt.Errorf("read Codex history %s: %w", id, err)
		}
		if data.DroppedTail || data.Skipped != 0 {
			return fmt.Errorf("Codex history %s is incomplete on the source device", id)
		}
		base, found, err := adapter.CodexHistoryBase(data.Records)
		if err != nil {
			return fmt.Errorf("inspect Codex history %s: %w", id, err)
		}
		if found {
			hasLineage = true
			if err := visit(base.ThreadID, depth+1); err != nil {
				return err
			}
			parentData, err := layout.ReadSession(byID[base.ThreadID])
			if err != nil {
				return fmt.Errorf("read Codex history parent %s: %w", base.ThreadID, err)
			}
			if parentData.DroppedTail || parentData.Skipped != 0 {
				return fmt.Errorf("Codex history parent %s is incomplete on the source device", base.ThreadID)
			}
			if _, err := adapter.RebaseCodexHistoryBase(data.Records, parentData.Records); err != nil {
				return fmt.Errorf("validate Codex history cutoff for %s: %w", id, err)
			}
		}
		state[id] = 2
		ordered = append(ordered, ref)
		return nil
	}
	if selectedID != "" {
		if err := visit(selectedID, 0); err != nil {
			return nil, false, err
		}
	} else {
		for _, ref := range refs {
			if err := visit(ref.NativeID, 0); err != nil {
				return nil, false, err
			}
		}
	}
	return ordered, hasLineage, nil
}
