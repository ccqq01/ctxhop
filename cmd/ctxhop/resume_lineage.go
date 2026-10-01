package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/CCCCY-ci/ctxhop/internal/adapter"
	"github.com/CCCCY-ci/ctxhop/internal/syncer"
	"github.com/CCCCY-ci/ctxhop/internal/syncflow"
)

const maxCodexLineageDepth = 64

type nativeResumeDependency struct {
	NativeID string
	Plan     syncflow.RestorePlan
}

func nativeResumeDependencyIDs(dependencies []nativeResumeDependency) []string {
	if len(dependencies) == 0 {
		return nil
	}
	ids := make([]string, len(dependencies))
	for i, dependency := range dependencies {
		ids[i] = dependency.NativeID
	}
	return ids
}

// planCodexResumeLineage downloads and validates the entire chain before a
// preview or apply can report the selected child as restorable.
func planCodexResumeLineage(ctx context.Context, groups []syncer.ProjectReplicaMetadataRef, childID, sourceDeviceID string, child syncflow.RestorePlan, access *domainAccess, space adapter.PathSpace, installation adapter.Installation, allowLimited bool) ([]nativeResumeDependency, error) {
	if access == nil {
		return nil, errors.New("resume: Codex history Remote access is unavailable")
	}
	seen := map[string]bool{childID: true}
	var dependencies []nativeResumeDependency
	var visit func(string, syncflow.RestorePlan) error
	visit = func(id string, plan syncflow.RestorePlan) error {
		base, found, err := adapter.CodexHistoryBase(plan.CanonicalRecords)
		if err != nil {
			return fmt.Errorf("resume: inspect Codex history %s: %w", id, err)
		}
		if !found {
			return nil
		}
		if seen[base.ThreadID] || len(seen) >= maxCodexLineageDepth {
			return fmt.Errorf("resume: Codex history cycle or excessive depth at %s", base.ThreadID)
		}
		seen[base.ThreadID] = true
		parent, found := matchingCodexLineageReplica(groups, base.ThreadID, sourceDeviceID)
		if !found {
			return fmt.Errorf("resume: Codex history parent %s is not available from the selected source device; push the complete lineage there first", base.ThreadID)
		}
		snapshot, err := syncer.FetchCompleteReplica(ctx, access.Store, parent.Layout, access.Identities)
		if err != nil {
			return fmt.Errorf("resume: download Codex history parent %s: %w", base.ThreadID, safeResumePlanError(err))
		}
		parentPlan, err := syncflow.PlanNativeReplicaRestore(snapshot, space, installation, syncflow.RestoreOptions{AllowLimited: allowLimited})
		if err != nil {
			return fmt.Errorf("resume: plan Codex history parent %s: %w", base.ThreadID, safeResumePlanError(err))
		}
		if _, err := adapter.RebaseCodexHistoryBase(plan.LocalizedRecords, parentPlan.LocalizedRecords); err != nil {
			return fmt.Errorf("resume: validate Codex history cutoff for %s: %w", id, err)
		}
		if err := visit(base.ThreadID, parentPlan); err != nil {
			return err
		}
		dependencies = append(dependencies, nativeResumeDependency{NativeID: base.ThreadID, Plan: parentPlan})
		return nil
	}
	if err := visit(childID, child); err != nil {
		return nil, err
	}
	return dependencies, nil
}

func matchingCodexLineageReplica(groups []syncer.ProjectReplicaMetadataRef, nativeID, deviceID string) (syncer.ReplicaMetadata, bool) {
	var chosen syncer.ReplicaMetadata
	found := false
	for _, group := range groups {
		for _, replica := range group.Replicas {
			if replica.Tip == nil || replica.Descriptor.Source.Agent != "codex" || replica.Descriptor.Source.NativeSessionID != nativeID || replica.Layout.DeviceID() != deviceID {
				continue
			}
			if !found || replica.Descriptor.Source.Generation > chosen.Descriptor.Source.Generation {
				chosen, found = replica, true
			}
		}
	}
	return chosen, found
}

// applyCodexResumeLineage installs each parent before its child. The final
// selected child is returned as a plan with a byte cutoff rebased against the
// exact parent file currently on disk.
func applyCodexResumeLineage(ctx context.Context, selection resumeSelection, projectRoot string, options resumeOptions) (syncflow.RestorePlan, error) {
	reader, ok := selection.Agent.Layout.(interface {
		ReadStoredSession(string) ([][]byte, error)
	})
	if !ok {
		return syncflow.RestorePlan{}, errors.New("resume: Codex adapter cannot read installed history")
	}
	for _, dependency := range selection.Dependencies {
		plan := dependency.Plan
		base, found, err := adapter.CodexHistoryBase(plan.LocalizedRecords)
		if err != nil {
			return syncflow.RestorePlan{}, err
		}
		if found {
			parentRecords, err := reader.ReadStoredSession(base.ThreadID)
			if err != nil {
				return syncflow.RestorePlan{}, fmt.Errorf("resume: read installed Codex history parent %s: %w", base.ThreadID, err)
			}
			plan.LocalizedRecords, err = adapter.RebaseCodexHistoryBase(plan.LocalizedRecords, parentRecords)
			if err != nil {
				return syncflow.RestorePlan{}, fmt.Errorf("resume: rebase Codex history %s: %w", dependency.NativeID, err)
			}
		}
		if _, err := syncflow.ApplyRestore(ctx, selection.Agent.Layout, projectRoot, dependency.NativeID, plan, syncflow.RestoreApplyOptions{
			AllowLimited: options.allowLimited, AgentHome: selection.Agent.Installation.DataDir, Agent: "codex", SkipWorkspacePreflight: true,
		}); err != nil {
			return syncflow.RestorePlan{}, fmt.Errorf("resume: restore Codex history parent %s: %w", dependency.NativeID, err)
		}
	}
	child := selection.Plan
	base, found, err := adapter.CodexHistoryBase(child.LocalizedRecords)
	if err != nil {
		return syncflow.RestorePlan{}, err
	}
	if !found {
		return child, nil
	}
	parentRecords, err := reader.ReadStoredSession(base.ThreadID)
	if err != nil {
		return syncflow.RestorePlan{}, fmt.Errorf("resume: read installed Codex history parent %s: %w", base.ThreadID, err)
	}
	child.LocalizedRecords, err = adapter.RebaseCodexHistoryBase(child.LocalizedRecords, parentRecords)
	if err != nil {
		return syncflow.RestorePlan{}, fmt.Errorf("resume: rebase Codex history child: %w", err)
	}
	return child, nil
}
