package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"xlyra/server/internal/upstream"
)

const (
	FactorySiteType       = "factory"
	FactoryDefaultBaseURL = "https://api.factory.ai"

	factorySiteType       = FactorySiteType
	factoryDefaultBaseURL = FactoryDefaultBaseURL

	factoryUserAgent     = "factory-cli/0.232.0"
	factoryClientVersion = "0.232.0"
	factoryClientHeader  = "cli"
)

var factoryCuratedModels = []struct {
	ID          string
	DisplayName string
}{
	// Claude series
	{ID: "claude-opus-4-8", DisplayName: "Claude Opus 4.8"},
	{ID: "claude-opus-4-7", DisplayName: "Claude Opus 4.7"},
	{ID: "claude-sonnet-5", DisplayName: "Claude Sonnet 5"},
	{ID: "claude-sonnet-4-6", DisplayName: "Claude Sonnet 4.6"},
	{ID: "claude-haiku-4-5-20251001", DisplayName: "Claude Haiku 4.5"},

	// GPT series
	{ID: "gpt-5.5", DisplayName: "GPT-5.5"},
	{ID: "gpt-5.5-pro", DisplayName: "GPT-5.5 Pro"},
	{ID: "gpt-5.4", DisplayName: "GPT-5.4"},
	{ID: "gpt-5.4-mini", DisplayName: "GPT-5.4 Mini"},
	{ID: "gpt-5.3-codex", DisplayName: "GPT-5.3 Codex"},
	{ID: "gpt-5.2", DisplayName: "GPT-5.2"},

	// Droid Core open source series
	{ID: "deepseek-v4-pro", DisplayName: "DeepSeek V4 Pro"},
	{ID: "glm-5.2", DisplayName: "GLM 5.2"},
	{ID: "glm-5.1", DisplayName: "GLM 5.1"},
	{ID: "kimi-k2.7-code", DisplayName: "Kimi K2.7 Code"},
	{ID: "kimi-k2.6", DisplayName: "Kimi K2.6"},
	{ID: "minimax-m3", DisplayName: "MiniMax M3"},
	{ID: "nemotron-3-ultra", DisplayName: "Nemotron 3 Ultra"},
}

type Factory struct {
	client *http.Client
}

func NewFactory() Factory {
	return Factory{
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

var (
	_ Module                 = Factory{}
	_ DefaultBaseURLProvider = Factory{}
	_ CapabilityDescriber    = Factory{}
	_ CredentialValidator    = Factory{}
	_ GatewayModelLister     = Factory{}
	_ HealthProbe            = Factory{}
	_ BalanceFetcher         = Factory{}
)

func (Factory) SiteTypes() []string {
	return []string{factorySiteType}
}

func (Factory) DefaultBaseURL() string {
	return factoryDefaultBaseURL
}

func (Factory) Capabilities() []Capability {
	return []Capability{
		CapabilityValidateCredential,
		CapabilityListModels,
		CapabilityHealthProbe,
		CapabilityFetchBalance,
	}
}

func (Factory) Scope() HealthProbeScope {
	return HealthProbeCredentialScope
}

type FactoryWhoamiResponse struct {
	OrgID  string `json:"orgId"`
	UserID string `json:"userId"`
}

func (a Factory) ValidateCredentials(ctx context.Context, site SiteConfig, apiKey string) error {
	_, _, err := a.Whoami(ctx, site, apiKey)
	return err
}

func (a Factory) Whoami(ctx context.Context, site SiteConfig, apiKey string) (string, string, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(site.BaseURL), "/")
	if baseURL == "" {
		baseURL = factoryDefaultBaseURL
	}
	url := baseURL + "/api/cli/whoami"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", "", fmt.Errorf("create whoami request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(apiKey))
	req.Header.Set("User-Agent", factoryUserAgent)
	req.Header.Set("X-Client-Version", factoryClientVersion)
	req.Header.Set("X-Factory-Client", factoryClientHeader)
	req.Header.Set("Accept", "application/json")

	resp, err := httpClientForSite(site, a.client).Do(req)
	if err != nil {
		return "", "", fmt.Errorf("call whoami: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", "", upstream.NewHTTPError("upstream returned", resp.StatusCode, resp.Header, body)
	}

	var payload struct {
		OrgID       string `json:"orgId"`
		UserID      string `json:"userId"`
		OrgIDSnake  string `json:"org_id"`
		UserIDSnake string `json:"user_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", "", fmt.Errorf("decode whoami response: %w", err)
	}

	orgID := strings.TrimSpace(payload.OrgID)
	if orgID == "" {
		orgID = strings.TrimSpace(payload.OrgIDSnake)
	}
	userID := strings.TrimSpace(payload.UserID)
	if userID == "" {
		userID = strings.TrimSpace(payload.UserIDSnake)
	}

	if orgID == "" && userID == "" {
		return "", "", fmt.Errorf("whoami response missing orgId and userId")
	}

	return orgID, userID, nil
}

func (a Factory) ListModels(ctx context.Context, site SiteConfig, apiKey string) ([]Model, error) {
	return a.curatedModels(), nil
}

func (a Factory) ProbeHealth(ctx context.Context, site SiteConfig, credential string) ([]Model, error) {
	if strings.TrimSpace(credential) != "" {
		if err := a.ValidateCredentials(ctx, site, credential); err != nil {
			return nil, err
		}
	}
	return a.curatedModels(), nil
}

func (a Factory) curatedModels() []Model {
	models := make([]Model, 0, len(factoryCuratedModels))
	for _, item := range factoryCuratedModels {
		models = append(models, Model{
			UpstreamName: item.ID,
			DisplayName:  item.DisplayName,
			Capabilities: map[string]any{
				"object":                   "model",
				"supported_endpoint_types": []string{"openai", "openai-response", "anthropic-messages"},
				"source":                   "factory_curated",
				"tool_call":                true,
				"supports_tools":           true,
				"streaming":                true,
			},
		})
	}
	return models
}

func (a Factory) FetchBalance(ctx context.Context, site SiteConfig, auth SystemAuth) (BalanceSnapshot, error) {
	apiKey := strings.TrimSpace(auth.AccessToken)
	if apiKey == "" && auth.Metadata != nil {
		if v, ok := auth.Metadata["api_key"].(string); ok {
			apiKey = strings.TrimSpace(v)
		}
	}
	if apiKey == "" && site.Meta != nil {
		if v, ok := site.Meta["api_key"].(string); ok {
			apiKey = strings.TrimSpace(v)
		}
	}

	return a.FetchBalanceWithKey(ctx, site, apiKey)
}

func (a Factory) FetchBalanceWithKey(ctx context.Context, site SiteConfig, apiKey string) (BalanceSnapshot, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(site.BaseURL), "/")
	if baseURL == "" {
		baseURL = factoryDefaultBaseURL
	}
	url := baseURL + "/api/billing/limits"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return BalanceSnapshot{}, fmt.Errorf("create limits request: %w", err)
	}

	if strings.TrimSpace(apiKey) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(apiKey))
	}
	req.Header.Set("User-Agent", factoryUserAgent)
	req.Header.Set("X-Client-Version", factoryClientVersion)
	req.Header.Set("X-Factory-Client", factoryClientHeader)
	req.Header.Set("Accept", "application/json")

	resp, err := httpClientForSite(site, a.client).Do(req)
	if err != nil {
		return BalanceSnapshot{}, fmt.Errorf("call limits: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return BalanceSnapshot{}, upstream.NewHTTPError("upstream returned", resp.StatusCode, resp.Header, body)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return BalanceSnapshot{}, fmt.Errorf("read limits response: %w", err)
	}

	var rawPayload map[string]any
	if err := json.Unmarshal(body, &rawPayload); err != nil {
		return BalanceSnapshot{}, fmt.Errorf("decode limits response: %w", err)
	}

	limitsMap, _ := rawPayload["limits"].(map[string]any)
	if limitsMap == nil {
		limitsMap = rawPayload
	}

	standardMap, _ := limitsMap["standard"].(map[string]any)
	coreMap, _ := limitsMap["core"].(map[string]any)

	standardParsed := parseFactoryLimitTier(standardMap)
	coreParsed := parseFactoryLimitTier(coreMap)

	balance := map[string]any{
		"type":     "factory",
		"standard": standardParsed,
		"core":     coreParsed,
		"limits": map[string]any{
			"standard": standardParsed,
			"core":     coreParsed,
		},
		"raw": rawPayload,
	}

	return BalanceSnapshot{Raw: balance}, nil
}

func parseFactoryLimitWindow(raw any) map[string]any {
	m, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	result := make(map[string]any, len(m)+2)
	for k, v := range m {
		result[k] = v
	}
	if pct, ok := float64FromAny(m["usedPercent"]); ok {
		result["usedPercent"] = pct
		result["used_percent"] = pct
	} else if pct, ok := float64FromAny(m["used_percent"]); ok {
		result["usedPercent"] = pct
		result["used_percent"] = pct
	}
	return result
}

func parseFactoryLimitTier(raw map[string]any) map[string]any {
	if raw == nil {
		return nil
	}
	tier := make(map[string]any, len(raw)+4)
	for k, v := range raw {
		tier[k] = v
	}
	if w := parseFactoryLimitWindow(raw["fiveHour"]); w != nil {
		tier["fiveHour"] = w
		tier["five_hour"] = w
	} else if w := parseFactoryLimitWindow(raw["five_hour"]); w != nil {
		tier["fiveHour"] = w
		tier["five_hour"] = w
	}
	if w := parseFactoryLimitWindow(raw["weekly"]); w != nil {
		tier["weekly"] = w
	}
	if w := parseFactoryLimitWindow(raw["monthly"]); w != nil {
		tier["monthly"] = w
	}
	return tier
}
