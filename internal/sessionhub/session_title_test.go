package sessionhub

import (
	"testing"
	"time"
)

func TestSessionDescriptorPreservesCodexAppTitleMetadata(t *testing.T) {
	session := testSession()
	session.Title = "简历"
	session.TitleSource = "codex-app"
	session.TitleUpdatedAt = time.Date(2026, 9, 30, 12, 34, 56, 0, time.UTC)
	encoded, err := session.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := ParseSessionDescriptor(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Title != session.Title || decoded.TitleSource != session.TitleSource || !decoded.TitleUpdatedAt.Equal(session.TitleUpdatedAt) {
		t.Fatalf("title metadata = %+v", decoded)
	}
}
