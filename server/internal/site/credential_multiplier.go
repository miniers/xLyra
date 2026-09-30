package site

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"xlyra/server/internal/adapter"
	"xlyra/server/internal/store"
)

const (
	upstreamCostMultiplierSourceMetaKey  = "upstream_cost_multiplier_source"
	upstreamCostMultiplierProbeMetaKey   = "upstream_cost_multiplier_probe"
	upstreamCostMultiplierHistoryMetaKey = "upstream_cost_multiplier_history"

	upstreamCostMultiplierSourceManual = "manual"
	upstreamCostMultiplierSourceAuto   = "auto"
)

type APIKeyCostMultiplierProbe struct {
	Status                  string    `json:"status"`
	Source                  string    `json:"source"`
	ResolvedRateMultiplier  *float64  `json:"resolved_rate_multiplier,omitempty"`
	EffectiveRateMultiplier *float64  `json:"effective_rate_multiplier,omitempty"`
	ProbedAt                time.Time `json:"probed_at"`
	Error                   string    `json:"error,omitempty"`
}

type APIKeyCostMultiplierChange struct {
	ChangedAt time.Time `json:"changed_at"`
	From      float64   `json:"from"`
	To        float64   `json:"to"`
	Source    string    `json:"source"`
}

func upstreamCostMultiplierSource(meta map[string]any) string {
	if strings.EqualFold(strings.TrimSpace(anyString(meta[upstreamCostMultiplierSourceMetaKey])), upstreamCostMultiplierSourceAuto) {
		return upstreamCostMultiplierSourceAuto
	}
	return upstreamCostMultiplierSourceManual
}

func normalizeUpstreamCostMultiplierSource(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", upstreamCostMultiplierSourceManual:
		return upstreamCostMultiplierSourceManual, nil
	case upstreamCostMultiplierSourceAuto:
		return upstreamCostMultiplierSourceAuto, nil
	default:
		return "", fmt.Errorf("upstream_cost_multiplier_source must be manual or auto")
	}
}

func validMultiplierProbeValue(value *float64) *float64 {
	if value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) {
		return nil
	}
	normalized, err := normalizeUpstreamCostMultiplier(*value)
	if err != nil {
		return nil
	}
	return &normalized
}

func appendAPIKeyCostMultiplierChange(meta map[string]any, from float64, to float64, source string, changedAt time.Time) {
	if math.Abs(from-to) < 1e-9 {
		return
	}
	history := []APIKeyCostMultiplierChange{}
	if raw, ok := meta[upstreamCostMultiplierHistoryMetaKey]; ok {
		if encoded, err := json.Marshal(raw); err == nil {
			_ = json.Unmarshal(encoded, &history)
		}
	}
	history = append(history, APIKeyCostMultiplierChange{
		ChangedAt: changedAt.UTC(),
		From:      from,
		To:        to,
		Source:    strings.TrimSpace(source),
	})
	meta[upstreamCostMultiplierHistoryMetaKey] = history
}

func APIKeyCostMultiplierHistory(meta map[string]any) []APIKeyCostMultiplierChange {
	raw, ok := meta[upstreamCostMultiplierHistoryMetaKey]
	if !ok {
		return nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var history []APIKeyCostMultiplierChange
	if err := json.Unmarshal(encoded, &history); err != nil {
		return nil
	}
	return history
}

func newAPIGroupRatio(snapshot adapter.PricingSnapshot, group string) (float64, bool) {
	group = strings.TrimSpace(group)
	if group == "" {
		return 0, false
	}
	root, ok := snapshot.Raw.(map[string]any)
	if !ok {
		return 0, false
	}
	ratios, ok := root["group_ratio"].(map[string]any)
	if !ok {
		return 0, false
	}
	rawRatio, ok := ratios[group]
	if !ok {
		return 0, false
	}
	ratio := validMultiplierProbeValue(quotaProbeFloat(rawRatio))
	if ratio == nil {
		return 0, false
	}
	return *ratio, true
}

func probeSub2APIBilling(ctx context.Context, client *http.Client, baseURL string, secret string) APIKeyCostMultiplierProbe {
	probe := APIKeyCostMultiplierProbe{
		Status:   "error",
		Source:   QuotaProbeTypeSub2API,
		ProbedAt: time.Now().UTC(),
	}
	payload, err := quotaProbeGetJSON(ctx, client, quotaProbeURL(baseURL, "/v1/sub2api/billing"), secret)
	if err != nil {
		probe.Error = safeMultiplierProbeError(err)
		return probe
	}

	probe.ResolvedRateMultiplier = validMultiplierProbeValue(multiplierPayloadValue(payload, "resolved_rate_multiplier"))
	probe.EffectiveRateMultiplier = validMultiplierProbeValue(multiplierPayloadValue(payload, "effective_rate_multiplier"))
	if probe.ResolvedRateMultiplier == nil {
		probe.Status = "incomplete"
		probe.Error = "billing response did not contain a valid resolved_rate_multiplier"
		return probe
	}
	probe.Status = "ok"
	return probe
}

func multiplierPayloadValue(payload map[string]any, key string) *float64 {
	if value, ok := payload[key]; ok {
		return quotaProbeFloat(value)
	}
	for _, envelope := range []string{"data", "billing"} {
		if nested, ok := payload[envelope].(map[string]any); ok {
			if value, exists := nested[key]; exists {
				return quotaProbeFloat(value)
			}
		}
	}
	return nil
}

func safeMultiplierProbeError(err error) string {
	if httpErr, ok := err.(*quotaProbeHTTPError); ok {
		return fmt.Sprintf("HTTP %d", httpErr.StatusCode)
	}
	if err == nil {
		return "probe failed"
	}
	return "billing probe request failed"
}

func newAPIKeyCostMultiplierProbe(snapshot adapter.PricingSnapshot, group string) APIKeyCostMultiplierProbe {
	probe := APIKeyCostMultiplierProbe{
		Source:   QuotaProbeTypeNewAPI,
		ProbedAt: time.Now().UTC(),
	}
	if strings.TrimSpace(group) == "" {
		probe.Status = "incomplete"
		probe.Error = "api key group is unavailable"
		return probe
	}
	ratio, ok := newAPIGroupRatio(snapshot, group)
	if !ok {
		probe.Status = "incomplete"
		probe.Error = "pricing group ratio is unavailable"
		return probe
	}
	probe.Status = "ok"
	probe.ResolvedRateMultiplier = &ratio
	return probe
}

func marshalCostMultiplierProbeMeta(meta map[string]any, probe APIKeyCostMultiplierProbe) (store.JSON, error) {
	meta[upstreamCostMultiplierProbeMetaKey] = probe
	encoded, err := json.Marshal(meta)
	if err != nil {
		return nil, fmt.Errorf("marshal credential multiplier probe: %w", err)
	}
	return store.JSON(encoded), nil
}

func (s *Service) updateAPIKeyMultiplierProbe(
	ctx context.Context,
	credential store.SiteCredential,
	meta map[string]any,
	probe APIKeyCostMultiplierProbe,
) (store.SiteCredential, error) {
	if meta == nil {
		meta = map[string]any{}
	}
	var value *float64
	if probe.Status == "ok" {
		probeValue := validMultiplierProbeValue(probe.ResolvedRateMultiplier)
		if probeValue != nil {
			var previousValue *float64
			if previousProbe, ok := apiKeyMultiplierProbe(meta); ok {
				previousValue = validMultiplierProbeValue(previousProbe.ResolvedRateMultiplier)
			}
			if previousValue != nil {
				appendAPIKeyCostMultiplierChange(meta, *previousValue, *probeValue, probe.Source, probe.ProbedAt)
			} else if upstreamCostMultiplierSource(meta) == upstreamCostMultiplierSourceAuto {
				appendAPIKeyCostMultiplierChange(meta, store.SiteCredentialUpstreamCostMultiplier(credential), *probeValue, probe.Source, probe.ProbedAt)
			}
			if upstreamCostMultiplierSource(meta) == upstreamCostMultiplierSourceAuto {
				// Only automatic mode is allowed to change the effective value.
				value = probeValue
			}
		}
	}
	encoded, err := marshalCostMultiplierProbeMeta(meta, probe)
	if err != nil {
		return store.SiteCredential{}, err
	}
	return store.NewSiteCredentialRepository(s.db.DB()).UpdateProbeState(ctx, credential.ID, store.UpdateSiteCredentialProbeParams{
		ExpectedUpdatedAt:      credential.UpdatedAt,
		Meta:                   encoded,
		UpstreamCostMultiplier: value,
	})
}

func APIKeyCostMultiplierProbePayload(meta map[string]any) (map[string]any, bool) {
	probe, ok := meta[upstreamCostMultiplierProbeMetaKey].(map[string]any)
	if !ok {
		return nil, false
	}
	result := map[string]any{}
	for _, key := range []string{
		"status",
		"source",
		"resolved_rate_multiplier",
		"effective_rate_multiplier",
		"probed_at",
		"error",
	} {
		if value, exists := probe[key]; exists {
			result[key] = value
		}
	}
	return result, len(result) > 0
}

func (s *Service) markAPIKeyMultiplierProbeIncomplete(ctx context.Context, siteID uuid.UUID, message string) {
	credentialRepo := store.NewSiteCredentialRepository(s.db.DB())
	credentials, err := credentialRepo.ListBySite(ctx, siteID)
	if err != nil {
		return
	}
	for _, credential := range credentials {
		if !quotaProbeCredentialEligible(credential.CredentialType) {
			continue
		}
		meta := map[string]any{}
		_ = json.Unmarshal(credential.Meta, &meta)
		probe := APIKeyCostMultiplierProbe{
			Status:   "incomplete",
			Source:   QuotaProbeTypeNewAPI,
			ProbedAt: time.Now().UTC(),
			Error:    message,
		}
		_, _ = s.updateAPIKeyMultiplierProbe(ctx, credential, meta, probe)
	}
}

func (s *Service) storedAPIKeyGroup(ctx context.Context, siteID uuid.UUID, credentialID uuid.UUID, meta map[string]any) string {
	if group := strings.TrimSpace(anyString(meta["group"])); group != "" {
		return group
	}
	states, err := store.NewSiteAPIKeyStateRepository(s.db.DB()).ListBySite(ctx, siteID)
	if err != nil {
		return ""
	}
	for _, state := range states {
		if state.SiteCredentialID == credentialID && state.GroupName.Valid {
			return strings.TrimSpace(state.GroupName.String)
		}
	}
	return ""
}

func (s *Service) probeNewAPIKeyMultiplier(ctx context.Context, site store.Site, module adapter.Module, credential store.SiteCredential, meta map[string]any) APIKeyCostMultiplierProbe {
	probe := APIKeyCostMultiplierProbe{
		Status:   "incomplete",
		Source:   QuotaProbeTypeNewAPI,
		ProbedAt: time.Now().UTC(),
	}
	auth, err := s.SystemAuth(ctx, site.ID)
	if err != nil || strings.TrimSpace(auth.AccessToken) == "" || auth.UserID <= 0 {
		probe.Error = "system auth is unavailable"
		return probe
	}
	pricingFetcher, ok := adapter.AsPricingFetcher(module)
	if !ok {
		probe.Error = "pricing endpoint is unsupported"
		return probe
	}
	snapshot, err := pricingFetcher.FetchPricing(ctx, s.toAdapterSite(ctx, site), auth)
	if err != nil {
		probe.Error = "pricing endpoint request failed"
		return probe
	}
	return newAPIKeyCostMultiplierProbe(snapshot, s.storedAPIKeyGroup(ctx, site.ID, credential.ID, meta))
}

func (s *Service) probeNewAPIKeyMultiplierFromSnapshot(ctx context.Context, credential store.SiteCredential, meta map[string]any, snapshot adapter.PricingSnapshot, group string) {
	if strings.TrimSpace(group) == "" {
		group = strings.TrimSpace(anyString(meta["group"]))
	}
	if strings.TrimSpace(group) == "" {
		group = s.storedAPIKeyGroup(ctx, credential.SiteID, credential.ID, meta)
	}
	probe := newAPIKeyCostMultiplierProbe(snapshot, group)
	_, _ = s.updateAPIKeyMultiplierProbe(ctx, credential, meta, probe)
}

func (s *Service) probeNewAPIKeyMultipliersFromSnapshot(ctx context.Context, preparedKeys []preparedRefreshKey, snapshot adapter.PricingSnapshot) {
	for _, preparedKey := range preparedKeys {
		meta := map[string]any{}
		_ = json.Unmarshal(preparedKey.credential.Meta, &meta)
		s.probeNewAPIKeyMultiplierFromSnapshot(ctx, preparedKey.credential, meta, snapshot, anyString(preparedKey.freshKey.Raw["group"]))
	}
}

func APIKeyCostMultiplierSource(meta map[string]any) string {
	return upstreamCostMultiplierSource(meta)
}

func apiKeyMultiplierProbe(meta map[string]any) (APIKeyCostMultiplierProbe, bool) {
	raw, ok := meta[upstreamCostMultiplierProbeMetaKey]
	if !ok {
		return APIKeyCostMultiplierProbe{}, false
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return APIKeyCostMultiplierProbe{}, false
	}
	var probe APIKeyCostMultiplierProbe
	if err := json.Unmarshal(encoded, &probe); err != nil {
		return APIKeyCostMultiplierProbe{}, false
	}
	return probe, true
}
