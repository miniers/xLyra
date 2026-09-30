package site

import (
	"testing"

	"github.com/google/uuid"

	"xlyra/server/internal/store"
)

func TestBuildCredentialModelPricingsUsesKeyRatioAndAvailableModelBindings(t *testing.T) {
	t.Parallel()

	for _, siteType := range []string{"openai", "google_gemini"} {
		t.Run(siteType, func(t *testing.T) {
			siteID := uuid.New()
			newAPIID := uuid.New()
			modelID := uuid.New()
			primaryID := uuid.New()
			backupID := uuid.New()
			ignoredID := uuid.New()
			items := buildCredentialModelPricings(
				[]store.Site{{ID: siteID, SiteType: siteType}, {ID: newAPIID, SiteType: "newapi"}},
				[]store.SiteCredential{
					{ID: primaryID, SiteID: siteID, CredentialType: "api_key:primary", DisplayName: "Primary", RoutingPriority: 5, UpstreamCostMultiplier: 1.2, Meta: store.JSON(`{"enabled":true}`)},
					{ID: backupID, SiteID: siteID, CredentialType: "api_key:backup", DisplayName: "Backup", RoutingPriority: 2, UpstreamCostMultiplier: 0.8, Meta: store.JSON(`{"enabled":true}`)},
					{ID: ignoredID, SiteID: newAPIID, CredentialType: "api_key:managed", DisplayName: "Managed", UpstreamCostMultiplier: 3, Meta: store.JSON(`{"enabled":true}`)},
				},
				[]store.SiteAPIKeyState{
					{SiteCredentialID: primaryID, SiteID: siteID, Enabled: true},
					{SiteCredentialID: backupID, SiteID: siteID, Enabled: false},
					{SiteCredentialID: ignoredID, SiteID: newAPIID, Enabled: true},
				},
				[]store.SiteAPIKeyModel{
					{SiteID: siteID, SiteCredentialID: primaryID, SiteModelID: uuid.NullUUID{UUID: modelID, Valid: true}, Available: true, Enabled: true},
					{SiteID: siteID, SiteCredentialID: backupID, SiteModelID: uuid.NullUUID{UUID: modelID, Valid: true}, Available: true, Enabled: true},
					{SiteID: siteID, SiteCredentialID: primaryID, SiteModelID: uuid.NullUUID{UUID: uuid.New(), Valid: true}, Available: false, Enabled: true},
					{SiteID: newAPIID, SiteCredentialID: ignoredID, SiteModelID: uuid.NullUUID{UUID: uuid.New(), Valid: true}, Available: true, Enabled: true},
				},
			)

			if len(items) != 3 {
				t.Fatalf("credential pricing count = %d, want 3", len(items))
			}
			byCredential := make(map[uuid.UUID]CredentialModelPricing, len(items))
			for _, item := range items {
				byCredential[item.SiteCredentialID] = item
			}
			primary, ok := byCredential[primaryID]
			if !ok || primary.GroupRatio != 1.2 || !primary.CredentialUsable {
				t.Fatalf("primary pricing = %#v", primary)
			}
			backup, ok := byCredential[backupID]
			if !ok || backup.GroupRatio != 0.8 || backup.CredentialEnabled || backup.CredentialUsable {
				t.Fatalf("backup pricing = %#v", backup)
			}
			managed, ok := byCredential[ignoredID]
			if !ok || managed.GroupRatio != 3 || !managed.CredentialUsable {
				t.Fatalf("newapi pricing = %#v", managed)
			}
		})
	}
}
