package main

import (
	"strings"
	"testing"
	"time"

	"github.com/CCCCY-ci/ctxhop/internal/adapter"
	"github.com/CCCCY-ci/ctxhop/internal/crypto"
	"github.com/CCCCY-ci/ctxhop/internal/project"
	"github.com/CCCCY-ci/ctxhop/internal/sessionhub"
	"github.com/CCCCY-ci/ctxhop/internal/syncer"
)

func TestSessionListPrefersCodexAppNameOverCachedPromptTitle(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	created := time.Date(2026, 9, 29, 1, 0, 0, 0, time.UTC)
	current := project.Project{Root: t.TempDir(), Identity: project.Identity{Kind: project.KindManual, Value: "manual:title-project"}}
	v1ProjectID, err := crypto.ProjectID(key, current.Identity.Value)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := sessionhub.NewDefaultRegistry(key, created)
	if err != nil {
		t.Fatal(err)
	}
	projectRecord, err := registry.EnsureProject(key, sessionhub.ProjectIdentityManual, current.Identity.Value, created)
	if err != nil {
		t.Fatal(err)
	}
	const nativeID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	legacyID, err := crypto.SessionID(key, v1ProjectID, nativeID)
	if err != nil {
		t.Fatal(err)
	}
	creator := sessionhub.SessionCreator{Agent: "codex", DeviceID: "deviceaa"}
	registered, err := registry.EnsureNativeSession(key, projectRecord.Descriptor.ProjectID, "codex", nativeID, legacyID, "automatic prompt title", created, creator)
	if err != nil {
		t.Fatal(err)
	}
	remoteName := sessionhub.SessionDescriptor{
		Version: sessionhub.ModelVersion, SessionID: registered.Descriptor.SessionID, ProjectID: projectRecord.Descriptor.ProjectID,
		Title: "简历", TitleSource: "codex-app", TitleUpdatedAt: created.Add(time.Hour),
		CreatedAt: created, CreatedBy: creator, Lifecycle: sessionhub.SessionActive,
	}
	collection := listCollection{
		current: current, identifierKey: key, projectID: v1ProjectID, localDeviceID: "devicelocal",
		remoteReplicas: []syncer.ProjectReplicaMetadataRef{{SessionID: remoteName.SessionID, SessionDescriptor: &remoteName}},
	}
	report, err := buildSessionList(collection, registry)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Sessions) != 1 || report.Sessions[0].Title != "简历" {
		t.Fatalf("remote App name was hidden by registry: %+v", report.Sessions)
	}
	collection.remoteReplicas = nil
	collection.localSessions = []adapter.SessionRef{{Agent: "codex", NativeID: nativeID, Title: "复盘", TitleSource: "codex-app", TitleUpdatedAt: created.Add(2 * time.Hour), CreatedAt: created}}
	report, err = buildSessionList(collection, registry)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Sessions) != 1 || report.Sessions[0].Title != "复盘" {
		t.Fatalf("local App name was hidden by registry: %+v", report.Sessions)
	}
}
