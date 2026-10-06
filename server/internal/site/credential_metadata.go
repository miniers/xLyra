package site

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"xlyra/server/internal/adapter"
	"xlyra/server/internal/store"
)

func credentialAdapterSite(site adapter.SiteConfig, metadata map[string]any) adapter.SiteConfig {
	merged := make(map[string]any, len(site.Meta)+len(metadata))
	for key, value := range site.Meta {
		merged[key] = value
	}
	for key, value := range metadata {
		merged[key] = value
	}
	site.Meta = merged
	return site
}

func (s *Service) syncCredentialMetadata(ctx context.Context, module adapter.Module, site store.Site, apiKey *APIKeyCredential) (adapter.SiteConfig, error) {
	config := s.toAdapterSite(ctx, site)
	fetcher, ok := adapter.AsCredentialMetadataFetcher(module)
	if !ok {
		return config, nil
	}
	config = credentialAdapterSite(config, apiKey.Meta)
	metadata, err := fetcher.FetchCredentialMetadata(ctx, config, apiKey.Secret)
	if err != nil {
		if ctx.Err() != nil {
			return config, ctx.Err()
		}
		slog.Default().WarnContext(ctx, "credential metadata refresh failed",
			"site_id", site.ID, "credential_id", apiKey.Credential.ID, "error", err)
		return config, nil
	}
	if len(metadata) == 0 {
		return config, nil
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return config, fmt.Errorf("marshal credential metadata: %w", err)
	}
	updated, err := store.NewSiteCredentialRepository(s.db.DB()).PatchMeta(ctx, apiKey.Credential.ID, encoded)
	if err != nil {
		return config, err
	}
	var merged map[string]any
	if err := json.Unmarshal(updated.Meta, &merged); err != nil {
		return config, fmt.Errorf("decode refreshed credential metadata: %w", err)
	}
	apiKey.Credential = updated
	apiKey.Meta = merged
	return credentialAdapterSite(s.toAdapterSite(ctx, site), merged), nil
}
