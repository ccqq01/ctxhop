package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CCCCY-ci/ctxhop/internal/adapter"
	"github.com/CCCCY-ci/ctxhop/internal/crypto"
	"github.com/CCCCY-ci/ctxhop/internal/remote"
	"github.com/CCCCY-ci/ctxhop/internal/sessionhub"
	"github.com/CCCCY-ci/ctxhop/internal/syncer"
	"github.com/CCCCY-ci/ctxhop/internal/syncflow"
)

func TestResumeCodexLineageRejectsMissingAndCyclicRemoteParent(t *testing.T) {
	childID := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	for _, tc := range []struct {
		name, parentID, want string
	}{
		{name: "missing", parentID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", want: "not available"},
		{name: "cycle", parentID: childID, want: "cycle"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := syncflow.RestorePlan{CanonicalRecords: [][]byte{pushLineageRecord(t, 2, "session_meta", map[string]any{
				"history_base": map[string]any{"thread_id": tc.parentID, "end_ordinal_exclusive": 2, "end_byte_offset": 0},
			})}}
			_, err := planCodexResumeLineage(t.Context(), nil, childID, "devicea", plan, &domainAccess{}, adapter.PathSpace{}, adapter.Installation{}, false)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q error, got %v", tc.want, err)
			}
		})
	}
}

func TestResumeCodexLineageRestoresParentBeforeChild(t *testing.T) {
	projectA := filepath.Join(t.TempDir(), "source")
	projectB := filepath.Join(t.TempDir(), "target")
	if err := os.MkdirAll(projectA, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(projectB, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectA, "source-only.txt"), []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	homeA, homeB := t.TempDir(), t.TempDir()
	configA, configB := t.TempDir(), t.TempDir()
	remoteRoot := t.TempDir()
	store, err := remote.NewDir(remoteRoot)
	if err != nil {
		t.Fatal(err)
	}
	keyfile, _, err := crypto.NewKeyfile("passphrase")
	if err != nil {
		t.Fatal(err)
	}
	if err := syncer.PublishKeyfile(t.Context(), store, keyfile); err != nil {
		t.Fatal(err)
	}
	dataKey, err := keyfile.UnlockWithPassphrase("passphrase")
	if err != nil {
		t.Fatal(err)
	}
	defer dataKey.Close()
	public, err := keyfile.IdentityPublicKey()
	if err != nil {
		t.Fatal(err)
	}
	identifierKey, err := dataKey.IdentifierKey()
	if err != nil {
		t.Fatal(err)
	}
	const identity = "manual:codex-lineage"
	cA := newCodexRoundTripConfig(t, configA, remoteRoot, public, identifierKey, "devicea", projectA, identity)
	cB := newCodexRoundTripConfig(t, configB, remoteRoot, public, identifierKey, "deviceb", projectB, identity)
	grandparentID := "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	parentID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	childID := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	layoutA := adapter.CodexLayout{Home: homeA}
	if err := layoutA.WriteSession(projectA, grandparentID, [][]byte{
		pushLineageRecord(t, 0, "session_meta", map[string]any{"id": grandparentID, "cwd": projectA, "history_mode": "paginated"}),
		pushLineageRecord(t, 1, "event_msg", map[string]any{"type": "user_message", "message": "grandparent history"}),
	}); err != nil {
		t.Fatal(err)
	}
	if err := layoutA.WriteSession(projectA, parentID, [][]byte{
		pushLineageRecord(t, 2, "session_meta", map[string]any{
			"id": parentID, "cwd": projectA, "history_mode": "paginated",
			"history_base": map[string]any{"thread_id": grandparentID, "end_ordinal_exclusive": 2, "end_byte_offset": 999999},
		}),
		pushLineageRecord(t, 3, "event_msg", map[string]any{"type": "user_message", "message": "parent history"}),
	}); err != nil {
		t.Fatal(err)
	}
	if err := layoutA.WriteSession(projectA, childID, [][]byte{
		pushLineageRecord(t, 4, "session_meta", map[string]any{
			"id": childID, "cwd": projectA, "history_mode": "paginated",
			"history_base": map[string]any{"thread_id": parentID, "end_ordinal_exclusive": 4, "end_byte_offset": 999999},
		}),
		pushLineageRecord(t, 5, "event_msg", map[string]any{"type": "user_message", "message": "child history"}),
		pushLineageRecord(t, 6, "response_item", map[string]any{"type": "function_call", "name": "apply_patch", "file_path": filepath.Join(projectA, "source-only.txt")}),
	}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", homeA)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(t.TempDir(), "missing-claude"))
	summary, err := collectPush(t.Context(), cA, configA, projectA, pushOptions{session: childID})
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if summary.Pushed != 3 || summary.Failed != 0 {
		t.Fatalf("push summary = %+v", summary)
	}
	hubID, err := sessionhub.DeriveHubKey(identifierKey, sessionhub.DefaultHubLogicalID)
	if err != nil {
		t.Fatal(err)
	}
	v2ProjectID, err := sessionhub.DeriveProjectKey(identifierKey, hubID, identity)
	if err != nil {
		t.Fatal(err)
	}
	logicalChild, err := sessionhub.DeriveNativeLogicalSessionKey(identifierKey, v2ProjectID, "codex", childID)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", homeB)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(t.TempDir(), "missing-claude"))
	options := resumeOptions{session: logicalChild, agent: "codex", noEnvironment: true, noWorkspaceContext: true, allowDivergent: true, preview: true}
	preview, err := collectResume(t.Context(), cB, configB, projectB, options, strings.NewReader("passphrase\n"), io.Discard)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(preview.LineageDependencies) != 2 || preview.LineageDependencies[0] != grandparentID || preview.LineageDependencies[1] != parentID {
		t.Fatalf("preview lineage = %+v", preview.LineageDependencies)
	}
	var previewText bytes.Buffer
	if err := writeResumeText(&previewText, preview); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(previewText.String(), "history dependencies: "+grandparentID+","+parentID) {
		t.Fatalf("preview text does not explain lineage: %s", previewText.String())
	}
	layoutB := adapter.CodexLayout{Home: homeB}
	if _, err := layoutB.ReadSession(adapter.SessionRef{NativeID: grandparentID}); err == nil {
		t.Fatal("preview wrote the grandparent")
	}
	if _, err := layoutB.ReadSession(adapter.SessionRef{NativeID: parentID}); err == nil {
		t.Fatal("preview wrote the parent")
	}
	options.preview = false
	options.allowDivergent = false
	if _, err := collectResume(context.Background(), cB, configB, projectB, options, strings.NewReader("passphrase\n"), io.Discard); err == nil {
		t.Fatal("resume accepted a divergent workspace without --allow-divergent")
	}
	if _, err := layoutB.ReadSession(adapter.SessionRef{NativeID: grandparentID}); err == nil {
		t.Fatal("failed workspace preflight wrote the grandparent")
	}
	options.allowDivergent = true
	if _, err := collectResume(context.Background(), cB, configB, projectB, options, strings.NewReader("passphrase\n"), io.Discard); err != nil {
		t.Fatalf("apply: %v", err)
	}
	grandparent, err := layoutB.ReadSession(adapter.SessionRef{NativeID: grandparentID})
	if err != nil {
		t.Fatalf("read restored grandparent: %v", err)
	}
	parent, err := layoutB.ReadSession(adapter.SessionRef{NativeID: parentID})
	if err != nil {
		t.Fatalf("read restored parent: %v", err)
	}
	child, err := layoutB.ReadSession(adapter.SessionRef{NativeID: childID})
	if err != nil {
		t.Fatalf("read restored child: %v", err)
	}
	parentBase, found, err := adapter.CodexHistoryBase(parent.Records)
	if err != nil || !found || parentBase.ThreadID != grandparentID {
		t.Fatalf("parent base = %+v found=%t err=%v", parentBase, found, err)
	}
	wantGrandparentOffset := uint64(len(grandparent.Records[0]) + 1 + len(grandparent.Records[1]) + 1)
	if parentBase.EndByteOffset != wantGrandparentOffset {
		t.Fatalf("parent cutoff = %d, want %d", parentBase.EndByteOffset, wantGrandparentOffset)
	}
	base, found, err := adapter.CodexHistoryBase(child.Records)
	if err != nil || !found || base.ThreadID != parentID {
		t.Fatalf("child base = %+v found=%t err=%v", base, found, err)
	}
	want := uint64(len(parent.Records[0]) + 1 + len(parent.Records[1]) + 1)
	if base.EndByteOffset != want {
		t.Fatalf("child cutoff = %d, want %d", base.EndByteOffset, want)
	}
}
