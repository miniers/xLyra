package site

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"xlyra/server/internal/adapter"
	"xlyra/server/internal/store"
)

type credentialMetadataTestModule struct {
	fetch func(context.Context, adapter.SiteConfig, string) (map[string]any, error)
}

func (credentialMetadataTestModule) SiteTypes() []string { return []string{"factory"} }

func (m credentialMetadataTestModule) FetchCredentialMetadata(ctx context.Context, site adapter.SiteConfig, secret string) (map[string]any, error) {
	return m.fetch(ctx, site, secret)
}

func TestSyncCredentialMetadataIsolatesKeysAndPreservesManualSettings(t *testing.T) {
	t.Parallel()
	siteID := uuid.New()
	keys := []APIKeyCredential{
		{Credential: store.SiteCredential{ID: uuid.New()}, Secret: "first", Meta: map[string]any{"region": "us"}},
		{Credential: store.SiteCredential{ID: uuid.New()}, Secret: "second", Meta: map[string]any{"region": "eu"}},
	}
	persisted := map[uuid.UUID]map[string]any{}
	for _, key := range keys {
		persisted[key.Credential.ID] = map[string]any{"region": key.Meta["region"], "name": "concurrent edit", "enabled": true}
	}
	var updatedID uuid.UUID
	service := siteServiceWithCallbacks(t, siteGormCallbacks{
		update: func(tx *gorm.DB) {
			updatedID = tx.Statement.Model.(*store.SiteCredential).ID
			updates := tx.Statement.Dest.(map[string]any)
			if len(updates) != 1 {
				t.Fatalf("metadata update touches unrelated fields: %#v", updates)
			}
			expression := updates["meta"].(clause.Expr)
			var patch map[string]any
			if err := json.Unmarshal([]byte(expression.Vars[0].(string)), &patch); err != nil {
				t.Fatal(err)
			}
			for key, value := range patch {
				persisted[updatedID][key] = value
			}
			tx.RowsAffected = 1
		},
		query: func(tx *gorm.DB) {
			credential := tx.Statement.Dest.(*store.SiteCredential)
			*credential = store.SiteCredential{ID: updatedID, SiteID: siteID, Meta: siteJSONMeta(t, persisted[updatedID])}
			tx.RowsAffected = 1
		},
	})
	module := credentialMetadataTestModule{fetch: func(_ context.Context, _ adapter.SiteConfig, secret string) (map[string]any, error) {
		return map[string]any{"factory_identity": map[string]any{"user_id": secret}}, nil
	}}
	site := store.Site{ID: siteID, SiteType: "factory", Meta: siteJSONMeta(t, map[string]any{"region": "site-region"})}
	for index := range keys {
		config, err := service.syncCredentialMetadata(t.Context(), module, site, &keys[index])
		if err != nil {
			t.Fatal(err)
		}
		identity := config.Meta["factory_identity"].(map[string]any)
		if identity["user_id"] != keys[index].Secret || config.Meta["region"] != keys[index].Meta["region"] || config.Meta["name"] != "concurrent edit" {
			t.Fatalf("credential metadata = %#v", config.Meta)
		}
	}
	if persisted[keys[0].Credential.ID]["factory_identity"].(map[string]any)["user_id"] != "first" {
		t.Fatal("second credential overwrote first credential identity")
	}
	if siteMustJSONMap(t, site.Meta)["region"] != "site-region" {
		t.Fatal("credential sync changed site metadata")
	}
}

func TestSyncCredentialMetadataFailureKeepsPreviousIdentity(t *testing.T) {
	t.Parallel()
	key := APIKeyCredential{Meta: map[string]any{"factory_identity": map[string]any{"region": "eu"}}}
	module := credentialMetadataTestModule{fetch: func(context.Context, adapter.SiteConfig, string) (map[string]any, error) {
		return nil, errors.New("temporary upstream failure")
	}}
	config, err := siteServiceWithoutStore().syncCredentialMetadata(t.Context(), module, store.Site{}, &key)
	if err != nil || config.Meta["factory_identity"].(map[string]any)["region"] != "eu" {
		t.Fatalf("metadata after failure = %#v, %v", config.Meta, err)
	}
}

func TestSyncCredentialMetadataPreservesCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	module := credentialMetadataTestModule{fetch: func(context.Context, adapter.SiteConfig, string) (map[string]any, error) {
		return nil, context.Canceled
	}}
	_, err := siteServiceWithoutStore().syncCredentialMetadata(ctx, module, store.Site{}, &APIKeyCredential{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("metadata refresh error = %v, want cancellation", err)
	}
}
