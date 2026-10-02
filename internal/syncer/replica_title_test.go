package syncer

import (
	"context"
	"crypto/ecdh"
	"testing"
	"time"

	"github.com/CCCCY-ci/ctxhop/internal/remote"
	"github.com/CCCCY-ci/ctxhop/internal/sessionhub"
)

func TestProjectMetadataPrefersNewestCodexAppTitle(t *testing.T) {
	dataKey := newTestDataKey(t)
	defer dataKey.Close()
	public, err := dataKey.IdentityPublic()
	if err != nil {
		t.Fatal(err)
	}
	private, err := dataKey.IdentityPrivate()
	if err != nil {
		t.Fatal(err)
	}
	store, err := remote.NewDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	layout, err := NewSessionHubLayout("hub", "project", "session")
	if err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for _, item := range []struct {
		device, title, source string
		updated               time.Time
	}{
		{device: "deviceaa", title: "automatic prompt title"},
		{device: "devicemm", title: "旧名称", source: "codex-app", updated: created.Add(time.Hour)},
		{device: "devicezz", title: "简历", source: "codex-app", updated: created.Add(2 * time.Hour)},
	} {
		descriptor := sessionhub.SessionDescriptor{
			Version: sessionhub.ModelVersion, SessionID: "session", ProjectID: "project",
			Title: item.title, TitleSource: item.source, TitleUpdatedAt: item.updated,
			CreatedAt: created, CreatedBy: sessionhub.SessionCreator{Agent: "codex", DeviceID: item.device}, Lifecycle: sessionhub.SessionActive,
		}
		if err := PutSessionDescriptorForDevice(context.Background(), store, public, layout, item.device, descriptor); err != nil {
			t.Fatal(err)
		}
	}
	project, err := NewProjectHubLayout("hub", "project")
	if err != nil {
		t.Fatal(err)
	}
	groups, err := FetchProjectReplicaMetadataWithDevices(context.Background(), store, project, []*ecdh.PrivateKey{private}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || groups[0].SessionDescriptor == nil || groups[0].SessionDescriptor.Title != "简历" {
		t.Fatalf("selected title = %+v", groups)
	}
}
