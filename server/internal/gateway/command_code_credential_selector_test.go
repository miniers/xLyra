package gateway

import (
	"testing"

	"github.com/google/uuid"

	"xlyra/server/internal/store"
)

func TestCommandCodeStickySelector(t *testing.T) {
	siteID := uuid.New()
	id1 := uuid.New()
	id2 := uuid.New()
	id3 := uuid.New()

	creds := []store.GatewayCredential{
		{Credential: store.SiteCredential{ID: id1}},
		{Credential: store.SiteCredential{ID: id2}},
		{Credential: store.SiteCredential{ID: id3}},
	}

	// Initially no sticky account, order should be preserved
	ordered := orderCommandCodeStickyCredentials(siteID, creds)
	if ordered[0].Credential.ID != id1 {
		t.Fatalf("expected id1 at front, got %v", ordered[0].Credential.ID)
	}

	// id2 succeeds
	recordCommandCodeCredentialSuccess(siteID, id2)

	// Now id2 should be moved to the front
	ordered2 := orderCommandCodeStickyCredentials(siteID, creds)
	if ordered2[0].Credential.ID != id2 {
		t.Fatalf("expected sticky id2 at front, got %v", ordered2[0].Credential.ID)
	}

	// id2 fails (e.g. hits 429)
	recordCommandCodeCredentialFailure(siteID, id2)

	// After failure, sticky affinity is cleared, original order preserved
	ordered3 := orderCommandCodeStickyCredentials(siteID, creds)
	if ordered3[0].Credential.ID != id1 {
		t.Fatalf("expected id1 at front after failure, got %v", ordered3[0].Credential.ID)
	}
}
