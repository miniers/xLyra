package site

import (
	"context"
	"database/sql"
	"testing"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"xlyra/server/internal/adapter"
	"xlyra/server/internal/store"
)

func TestSeedAdapterModelPricingWritesTypedValues(t *testing.T) {
	siteID := uuid.New()
	modelID := uuid.New()
	var created store.SiteModelPricing
	db := siteGormWithCallbacks(t, siteGormCallbacks{
		query: func(tx *gorm.DB) {
			tx.AddError(gorm.ErrRecordNotFound)
		},
		create: siteCaptureCreate(t, "site model pricing", func(item store.SiteModelPricing) {
			created = item
		}),
	})

	err := seedAdapterModelPricing(context.Background(), db, store.Site{ID: siteID}, store.SiteModel{
		ID:           modelID,
		UpstreamName: "MiniMaxAI/MiniMax-M3",
	}, adapter.ModelPricing{
		GroupName:           "default",
		BillingType:         "tokens",
		Currency:            "USD",
		GroupRatio:          1,
		InputValue:          0.3,
		HasInputValue:       true,
		OutputValue:         1.2,
		HasOutputValue:      true,
		CacheRatio:          0.2,
		HasCacheRatio:       true,
		CreateCacheRatio:    1,
		HasCreateCacheRatio: true,
		Raw: map[string]any{
			"source": "command_code",
			"unit":   "USD per 1M tokens",
		},
	})
	if err != nil {
		t.Fatalf("seedAdapterModelPricing error: %v", err)
	}
	if created.SiteID != siteID || !created.SiteModelID.Valid || created.SiteModelID.UUID != modelID {
		t.Fatalf("created model identity = site %s model %#v, want site %s model %s", created.SiteID, created.SiteModelID, siteID, modelID)
	}
	if created.ModelName != "MiniMaxAI/MiniMax-M3" || created.BillingType != "tokens" || created.Currency != "USD" {
		t.Fatalf("created model metadata = %#v", created)
	}
	if !created.InputValue.Valid || created.InputValue.Float64 != 0.3 || !created.OutputValue.Valid || created.OutputValue.Float64 != 1.2 {
		t.Fatalf("created token prices = input %#v output %#v", created.InputValue, created.OutputValue)
	}
	if !created.CacheRatio.Valid || created.CacheRatio.Float64 != 0.2 || !created.CreateCacheRatio.Valid || created.CreateCacheRatio.Float64 != 1 {
		t.Fatalf("created cache ratios = read %#v write %#v", created.CacheRatio, created.CreateCacheRatio)
	}
	if created.PricingSource != "command_code" || !created.Available {
		t.Fatalf("created source/availability = %q/%v", created.PricingSource, created.Available)
	}
	raw := siteMustJSONMap(t, []byte(created.Raw))
	if raw["source"] != "command_code" || raw["unit"] != "USD per 1M tokens" {
		t.Fatalf("created raw metadata = %#v", raw)
	}
}

func TestSeedAdapterModelPricingPreservesManualOverride(t *testing.T) {
	siteID := uuid.New()
	modelID := uuid.New()
	manual := store.SiteModelPricing{
		ID:             uuid.New(),
		SiteID:         siteID,
		SiteModelID:    uuid.NullUUID{UUID: modelID, Valid: true},
		ModelName:      "MiniMaxAI/MiniMax-M3",
		GroupName:      "default",
		ManualOverride: true,
		InputValue:     validSiteFloat(99),
		Available:      true,
	}
	created := false
	updated := false
	db := siteGormWithCallbacks(t, siteGormCallbacks{
		query: func(tx *gorm.DB) {
			dest, ok := tx.Statement.Dest.(*store.SiteModelPricing)
			if !ok {
				tx.AddError(gorm.ErrInvalidData)
				return
			}
			*dest = manual
			tx.RowsAffected = 1
			tx.Statement.RowsAffected = 1
		},
		create: func(tx *gorm.DB) {
			created = true
			tx.AddError(gorm.ErrInvalidData)
		},
		update: func(tx *gorm.DB) {
			updated = true
			tx.AddError(gorm.ErrInvalidData)
		},
	})

	err := seedAdapterModelPricing(context.Background(), db, store.Site{ID: siteID}, store.SiteModel{
		ID:           modelID,
		UpstreamName: "MiniMaxAI/MiniMax-M3",
	}, adapter.ModelPricing{
		InputValue:     0.3,
		HasInputValue:  true,
		OutputValue:    1.2,
		HasOutputValue: true,
		Raw:            map[string]any{"source": "command_code"},
	})
	if err != nil {
		t.Fatalf("seedAdapterModelPricing manual override error: %v", err)
	}
	if created || updated {
		t.Fatalf("manual override was written: created=%v updated=%v", created, updated)
	}
}

func validSiteFloat(value float64) sql.NullFloat64 {
	return sql.NullFloat64{Float64: value, Valid: true}
}

func TestDefaultModelPricingGrokImage(t *testing.T) {
	site := store.Site{SiteType: "grok"}
	image := adapter.Model{
		UpstreamName: "grok-imagine-image-quality",
		Capabilities: map[string]any{"supported_endpoint_types": []string{"openai-image"}},
	}
	value, ok := defaultModelPricing(site, image)
	if !ok || value != grokImagePerRequestUSD {
		t.Fatalf("grok image default = (%v, %v), want (%v, true)", value, ok, grokImagePerRequestUSD)
	}

	imageAny := adapter.Model{
		Capabilities: map[string]any{"supported_endpoint_types": []any{"openai-image"}},
	}
	if _, ok := defaultModelPricing(site, imageAny); !ok {
		t.Fatal("[]any endpoint types should be recognized")
	}
}

func TestDefaultModelPricingSkipsNonImageAndNonGrok(t *testing.T) {
	grokChat := adapter.Model{
		UpstreamName: "grok-4.5",
		Capabilities: map[string]any{"supported_endpoint_types": []string{"openai", "openai-response"}},
	}
	if _, ok := defaultModelPricing(store.Site{SiteType: "grok"}, grokChat); ok {
		t.Fatal("grok chat model must not get a default per-request price")
	}

	otherImage := adapter.Model{
		Capabilities: map[string]any{"supported_endpoint_types": []string{"openai-image"}},
	}
	if _, ok := defaultModelPricing(store.Site{SiteType: "openai"}, otherImage); ok {
		t.Fatal("non-grok image model must not get the grok default")
	}
}
