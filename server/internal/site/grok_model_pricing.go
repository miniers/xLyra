package site

import (
	"context"
	"strings"

	"xlyra/server/internal/adapter"
	"xlyra/server/internal/store"
)

const grokImagePerRequestUSD = 0.05

func seedDefaultModelPricing(ctx context.Context, tx store.Tx, site store.Site, siteModel store.SiteModel, model adapter.Model) error {
	if model.Pricing != nil {
		return seedAdapterModelPricing(ctx, tx, site, siteModel, *model.Pricing)
	}

	value, ok := defaultModelPricing(site, model)
	if !ok {
		return nil
	}
	_, err := store.NewSiteModelPricingRepository(tx).Upsert(ctx, store.UpsertSiteModelPricingParams{
		SiteID:          site.ID,
		SiteModelID:     siteModel.ID,
		ModelName:       siteModel.UpstreamName,
		GroupName:       "default",
		QuotaType:       1,
		BillingType:     "per_request",
		Currency:        "USD",
		GroupRatio:      1,
		PerRequestValue: value,
		PricingSource:   "default",
		PreserveManual:  true,
		Available:       true,
	})
	return err
}

func seedAdapterModelPricing(ctx context.Context, tx store.Tx, site store.Site, siteModel store.SiteModel, pricing adapter.ModelPricing) error {
	groupName := defaultString(pricing.GroupName, "default")
	billingType := defaultString(pricing.BillingType, "tokens")
	currency := defaultString(pricing.Currency, "USD")
	groupRatio := pricing.GroupRatio
	if groupRatio <= 0 {
		groupRatio = 1
	}
	quotaType := pricing.QuotaType
	if billingType == "per_request" {
		quotaType = 1
	}

	_, err := store.NewSiteModelPricingRepository(tx).Upsert(ctx, store.UpsertSiteModelPricingParams{
		SiteID:               site.ID,
		SiteModelID:          siteModel.ID,
		ModelName:            siteModel.UpstreamName,
		GroupName:            groupName,
		QuotaType:            quotaType,
		BillingType:          billingType,
		Currency:             currency,
		GroupRatio:           groupRatio,
		ModelRatio:           nullableFloat64(pricing.ModelRatio, pricing.HasModelRatio),
		CompletionRatio:      nullableFloat64(pricing.CompletionRatio, pricing.HasCompletionRatio),
		CacheRatio:           nullableFloat64(pricing.CacheRatio, pricing.HasCacheRatio),
		CreateCacheRatio:     nullableFloat64(pricing.CreateCacheRatio, pricing.HasCreateCacheRatio),
		CreateCache1hRatio:   nullableFloat64(pricing.CreateCache1hRatio, pricing.HasCreateCache1hRatio),
		ImageRatio:           nullableFloat64(pricing.ImageRatio, pricing.HasImageRatio),
		AudioRatio:           nullableFloat64(pricing.AudioRatio, pricing.HasAudioRatio),
		AudioCompletionRatio: nullableFloat64(pricing.AudioCompletionRatio, pricing.HasAudioCompletionRatio),
		ModelPrice:           nullableFloat64(pricing.ModelPrice, pricing.HasModelPrice),
		PerRequestValue:      nullableFloat64(pricing.PerRequestValue, pricing.HasPerRequestValue),
		InputValue:           nullableFloat64(pricing.InputValue, pricing.HasInputValue),
		OutputValue:          nullableFloat64(pricing.OutputValue, pricing.HasOutputValue),
		VendorID:             nullableInt64(pricing.VendorID, pricing.HasVendorID),
		VendorName:           nullableString(pricing.VendorName),
		VendorIcon:           nullableString(pricing.VendorIcon),
		Description:          nullableString(pricing.Description),
		OwnerBy:              nullableString(pricing.OwnerBy),
		PricingSource:        defaultString(anyString(pricing.Raw["source"]), "adapter"),
		PreserveManual:       true,
		Available:            true,
		Raw:                  jsonBytes(pricing.Raw),
	})
	return err
}

func defaultModelPricing(site store.Site, model adapter.Model) (float64, bool) {
	if !strings.EqualFold(strings.TrimSpace(site.SiteType), "grok") {
		return 0, false
	}
	if !modelSupportsImageEndpoint(model) {
		return 0, false
	}
	return grokImagePerRequestUSD, true
}

func modelSupportsImageEndpoint(model adapter.Model) bool {
	raw, ok := model.Capabilities["supported_endpoint_types"]
	if !ok {
		return false
	}
	switch values := raw.(type) {
	case []string:
		for _, value := range values {
			if strings.EqualFold(strings.TrimSpace(value), "openai-image") {
				return true
			}
		}
	case []any:
		for _, value := range values {
			text, _ := value.(string)
			if strings.EqualFold(strings.TrimSpace(text), "openai-image") {
				return true
			}
		}
	}
	return false
}
