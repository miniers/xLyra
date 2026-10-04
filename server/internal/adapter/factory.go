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

	factoryUserAgent       = "factory-cli/0.232.0"
	factoryClientVersion   = "0.232.0"
	factoryClientHeader    = "cli"
	factoryProtocolVersion = "1.244.0"
)

var factoryCuratedModels = []struct {
	ID          string
	DisplayName string
	Tier        string
	Kind        string
}{
	// Router series
	{ID: "auto", DisplayName: "Factory Auto Router", Tier: "standard", Kind: "router"},
	{ID: "auto-fast", DisplayName: "Factory Auto Router Fast", Tier: "standard", Kind: "router"},

	// Aliases series
	{ID: "openai-latest-premium", DisplayName: "OpenAI Latest Premium", Tier: "premium", Kind: "concrete"},
	{ID: "openai-latest-balanced", DisplayName: "OpenAI Latest Balanced", Tier: "standard", Kind: "concrete"},
	{ID: "openai-latest-fast", DisplayName: "OpenAI Latest Fast", Tier: "standard", Kind: "concrete"},
	{ID: "anthropic-latest-premium", DisplayName: "Anthropic Latest Premium", Tier: "premium", Kind: "concrete"},
	{ID: "anthropic-latest-balanced", DisplayName: "Anthropic Latest Balanced", Tier: "standard", Kind: "concrete"},
	{ID: "anthropic-latest-fast", DisplayName: "Anthropic Latest Fast", Tier: "standard", Kind: "concrete"},
	{ID: "oss-latest-premium", DisplayName: "OSS Latest Premium", Tier: "core", Kind: "concrete"},
	{ID: "oss-latest-balanced", DisplayName: "OSS Latest Balanced", Tier: "core", Kind: "concrete"},
	{ID: "oss-latest-fast", DisplayName: "OSS Latest Fast", Tier: "core", Kind: "concrete"},

	// Claude series
	{ID: "claude-opus-5-5", DisplayName: "Claude Opus 5.5", Tier: "premium", Kind: "concrete"},
	{ID: "claude-opus-5-5-fast", DisplayName: "Claude Opus 5.5 Fast Mode", Tier: "premium", Kind: "concrete"},
	{ID: "claude-opus-5", DisplayName: "Claude Opus 5", Tier: "premium", Kind: "concrete"},
	{ID: "claude-opus-5-fast", DisplayName: "Claude Opus 5 Fast Mode", Tier: "premium", Kind: "concrete"},
	{ID: "claude-opus-4-8", DisplayName: "Claude Opus 4.8", Tier: "premium", Kind: "concrete"},
	{ID: "claude-opus-4-8-fast", DisplayName: "Claude Opus 4.8 Fast Mode", Tier: "premium", Kind: "concrete"},
	{ID: "claude-opus-4-7", DisplayName: "Claude Opus 4.7", Tier: "premium", Kind: "concrete"},
	{ID: "claude-opus-4-7-fast", DisplayName: "Claude Opus 4.7 Fast Mode", Tier: "premium", Kind: "concrete"},
	{ID: "claude-opus-4-6", DisplayName: "Claude Opus 4.6", Tier: "premium", Kind: "concrete"},
	{ID: "claude-opus-4-6-fast", DisplayName: "Claude Opus 4.6 Fast Mode", Tier: "premium", Kind: "concrete"},
	{ID: "claude-opus-4-5-20251101", DisplayName: "Claude Opus 4.5", Tier: "premium", Kind: "concrete"},
	{ID: "claude-opus-4-1-20250805", DisplayName: "Claude Opus 4.1", Tier: "premium", Kind: "concrete"},
	{ID: "claude-sonnet-5-5", DisplayName: "Claude Sonnet 5.5", Tier: "standard", Kind: "concrete"},
	{ID: "claude-sonnet-5", DisplayName: "Claude Sonnet 5", Tier: "standard", Kind: "concrete"},
	{ID: "claude-sonnet-4-6", DisplayName: "Claude Sonnet 4.6", Tier: "standard", Kind: "concrete"},
	{ID: "claude-sonnet-4-5-20250929", DisplayName: "Claude Sonnet 4.5", Tier: "standard", Kind: "concrete"},
	{ID: "claude-sonnet-4-20250514", DisplayName: "Claude Sonnet 4", Tier: "standard", Kind: "concrete"},
	{ID: "claude-3-7-sonnet-20250219", DisplayName: "Claude 3.7 Sonnet", Tier: "standard", Kind: "concrete"},
	{ID: "claude-3-5-sonnet-20241022", DisplayName: "Claude 3.5 Sonnet v2", Tier: "standard", Kind: "concrete"},
	{ID: "claude-fable-5.1", DisplayName: "Claude Fable 5.1", Tier: "premium", Kind: "concrete"},
	{ID: "claude-fable-5", DisplayName: "Claude Fable 5", Tier: "premium", Kind: "concrete"},
	{ID: "claude-haiku-4-5-20251001", DisplayName: "Claude Haiku 4.5", Tier: "standard", Kind: "concrete"},
	{ID: "claude-3-5-haiku-20241022", DisplayName: "Claude 3.5 Haiku", Tier: "standard", Kind: "concrete"},
	{ID: "atlas-07-21", DisplayName: "Atlas 07/21 (Preview)", Tier: "standard", Kind: "concrete"},
	{ID: "aster-07-15", DisplayName: "Aster 07/15 (Preview)", Tier: "standard", Kind: "concrete"},

	// GPT series
	{ID: "gpt-6.1-sol", DisplayName: "GPT-6.1 Sol", Tier: "premium", Kind: "concrete"},
	{ID: "gpt-6-sol", DisplayName: "GPT-6 Sol", Tier: "premium", Kind: "concrete"},
	{ID: "gpt-6-astra", DisplayName: "GPT-6 Astra", Tier: "premium", Kind: "concrete"},
	{ID: "gpt-6-luna", DisplayName: "GPT-6 Luna", Tier: "premium", Kind: "concrete"},
	{ID: "gpt-5.6-sol", DisplayName: "GPT-5.6 Sol", Tier: "premium", Kind: "concrete"},
	{ID: "gpt-5.6-sol-fast", DisplayName: "GPT-5.6 Sol Fast Mode", Tier: "premium", Kind: "concrete"},
	{ID: "gpt-5.6-terra", DisplayName: "GPT-5.6 Terra", Tier: "premium", Kind: "concrete"},
	{ID: "gpt-5.6-luna", DisplayName: "GPT-5.6 Luna", Tier: "premium", Kind: "concrete"},
	{ID: "gpt-5.5-pro", DisplayName: "GPT-5.5 Pro", Tier: "premium", Kind: "concrete"},
	{ID: "gpt-5.5", DisplayName: "GPT-5.5", Tier: "premium", Kind: "concrete"},
	{ID: "gpt-5.5-fast", DisplayName: "GPT-5.5 Fast Mode", Tier: "premium", Kind: "concrete"},
	{ID: "gpt-5.4", DisplayName: "GPT-5.4", Tier: "standard", Kind: "concrete"},
	{ID: "gpt-5.4-fast", DisplayName: "GPT-5.4 Fast Mode", Tier: "standard", Kind: "concrete"},
	{ID: "gpt-5.4-mini", DisplayName: "GPT-5.4 Mini", Tier: "standard", Kind: "concrete"},
	{ID: "gpt-5.4-mini-fast", DisplayName: "GPT-5.4 Mini Fast Mode", Tier: "standard", Kind: "concrete"},
	{ID: "gpt-5.3-codex", DisplayName: "GPT-5.3 Codex", Tier: "standard", Kind: "concrete"},
	{ID: "gpt-5.3-codex-fast", DisplayName: "GPT-5.3 Codex Fast Mode", Tier: "standard", Kind: "concrete"},
	{ID: "gpt-5.2-codex", DisplayName: "GPT-5.2 Codex", Tier: "standard", Kind: "concrete"},
	{ID: "gpt-5.2", DisplayName: "GPT-5.2", Tier: "standard", Kind: "concrete"},
	{ID: "gpt-5.1-codex-max", DisplayName: "GPT-5.1 Codex Max", Tier: "standard", Kind: "concrete"},
	{ID: "gpt-5.1-codex", DisplayName: "GPT-5.1 Codex", Tier: "standard", Kind: "concrete"},
	{ID: "gpt-5.1", DisplayName: "GPT-5.1", Tier: "standard", Kind: "concrete"},
	{ID: "gpt-5-codex", DisplayName: "GPT-5 Codex", Tier: "standard", Kind: "concrete"},
	{ID: "gpt-5-mini-2025-08-07", DisplayName: "GPT-5 Mini", Tier: "standard", Kind: "concrete"},
	{ID: "gpt-5-2025-08-07", DisplayName: "GPT-5", Tier: "standard", Kind: "concrete"},

	// Gemini series
	{ID: "gemini-3.8-flash", DisplayName: "Gemini 3.8 Flash", Tier: "standard", Kind: "concrete"},
	{ID: "gemini-3.7-flash", DisplayName: "Gemini 3.7 Flash", Tier: "standard", Kind: "concrete"},
	{ID: "gemini-3.6-flash", DisplayName: "Gemini 3.6 Flash", Tier: "standard", Kind: "concrete"},
	{ID: "gemini-3.5-flash", DisplayName: "Gemini 3.5 Flash", Tier: "standard", Kind: "concrete"},
	{ID: "gemini-3.1-pro-preview", DisplayName: "Gemini 3.1 Pro", Tier: "standard", Kind: "concrete"},
	{ID: "gemini-3-pro-preview", DisplayName: "Gemini 3 Pro", Tier: "standard", Kind: "concrete"},
	{ID: "gemini-3-flash-preview", DisplayName: "Gemini 3 Flash", Tier: "standard", Kind: "concrete"},
	{ID: "gemini-2.5-pro", DisplayName: "Gemini 2.5 Pro", Tier: "standard", Kind: "concrete"},
	{ID: "gemini-2.5-flash", DisplayName: "Gemini 2.5 Flash", Tier: "standard", Kind: "concrete"},
	{ID: "garnet-07-15", DisplayName: "Garnet 07/15 (Preview)", Tier: "standard", Kind: "concrete"},

	// Grok series
	{ID: "grok-4.7", DisplayName: "Grok 4.7", Tier: "premium", Kind: "concrete"},
	{ID: "grok-4.6", DisplayName: "Grok 4.6", Tier: "premium", Kind: "concrete"},
	{ID: "grok-4.5", DisplayName: "Grok 4.5", Tier: "premium", Kind: "concrete"},

	// Droid Core open source series
	{ID: "kimi-k3", DisplayName: "Kimi K3", Tier: "core", Kind: "concrete"},
	{ID: "kimi-k2.7-code", DisplayName: "Kimi K2.7 Code", Tier: "core", Kind: "concrete"},
	{ID: "kimi-k2.6", DisplayName: "Kimi K2.6", Tier: "core", Kind: "concrete"},
	{ID: "glm-5.3", DisplayName: "GLM-5.3", Tier: "core", Kind: "concrete"},
	{ID: "glm-5.3-flash", DisplayName: "GLM-5.3 Flash", Tier: "core", Kind: "concrete"},
	{ID: "glm-5.2", DisplayName: "GLM-5.2", Tier: "core", Kind: "concrete"},
	{ID: "glm-5.2-fast", DisplayName: "GLM-5.2 Fast Mode", Tier: "core", Kind: "concrete"},
	{ID: "glm-5.1", DisplayName: "GLM-5.1", Tier: "core", Kind: "concrete"},
	{ID: "glm-5", DisplayName: "GLM-5", Tier: "core", Kind: "concrete"},
	{ID: "deepseek-v4.1-flash", DisplayName: "DeepSeek V4.1 Flash", Tier: "core", Kind: "concrete"},
	{ID: "deepseek-v4-pro", DisplayName: "DeepSeek V4 Pro", Tier: "core", Kind: "concrete"},
	{ID: "deepseek-v4-flash-0731", DisplayName: "DeepSeek V4 Flash 0731", Tier: "core", Kind: "concrete"},
	{ID: "minimax-m3", DisplayName: "MiniMax M3", Tier: "core", Kind: "concrete"},
	{ID: "minimax-m2.7", DisplayName: "MiniMax M2.7", Tier: "core", Kind: "concrete"},
	{ID: "minimax-m2.5", DisplayName: "MiniMax M2.5", Tier: "core", Kind: "concrete"},
	{ID: "qwen3.8-max", DisplayName: "Qwen3.8 Max", Tier: "core", Kind: "concrete"},
	{ID: "mistral-medium-3.5", DisplayName: "Mistral Medium 3.5", Tier: "core", Kind: "concrete"},
	{ID: "nemotron-3-ultra", DisplayName: "Nemotron 3 Ultra", Tier: "core", Kind: "concrete"},

	// Special safety / audit models
	{ID: "shield-risk", DisplayName: "Factory Shield Risk", Tier: "standard", Kind: "concrete"},
	{ID: "shield-downgrade", DisplayName: "Factory Shield Downgrade", Tier: "standard", Kind: "concrete"},
	{ID: "inkling", DisplayName: "Inkling", Tier: "core", Kind: "concrete"},
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
	req.Header.Set("X-Factory-Protocol-Version", factoryProtocolVersion)
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
	if strings.TrimSpace(apiKey) != "" {
		remoteModels, err := a.fetchRemoteModels(ctx, site, apiKey)
		if err == nil && len(remoteModels) > 0 {
			return remoteModels, nil
		}
	}
	return a.curatedModels(), nil
}

func (a Factory) ProbeHealth(ctx context.Context, site SiteConfig, credential string) ([]Model, error) {
	if strings.TrimSpace(credential) != "" {
		if err := a.ValidateCredentials(ctx, site, credential); err != nil {
			return nil, err
		}
	}
	return a.ListModels(ctx, site, credential)
}

type factoryRemoteModelItem struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	OwnedBy       string `json:"owned_by"`
	ContextWindow int    `json:"context_window"`
}

func (a Factory) fetchRemoteModels(ctx context.Context, site SiteConfig, apiKey string) ([]Model, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(site.BaseURL), "/")
	if baseURL == "" {
		baseURL = factoryDefaultBaseURL
	}
	url := baseURL + "/api/models"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create models request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(apiKey))
	req.Header.Set("User-Agent", factoryUserAgent)
	req.Header.Set("X-Client-Version", factoryClientVersion)
	req.Header.Set("X-Factory-Client", factoryClientHeader)
	req.Header.Set("X-Factory-Protocol-Version", factoryProtocolVersion)
	req.Header.Set("Accept", "application/json")

	resp, err := httpClientForSite(site, a.client).Do(req)
	if err != nil {
		return nil, fmt.Errorf("call models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("upstream returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("read models response: %w", err)
	}

	var payload struct {
		Data []factoryRemoteModelItem `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || len(payload.Data) == 0 {
		return nil, fmt.Errorf("decode models response: %w", err)
	}

	models := make([]Model, 0, len(payload.Data))
	for _, item := range payload.Data {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			continue
		}
		displayName := strings.TrimSpace(item.Name)
		if displayName == "" {
			displayName = id
		}
		caps := map[string]any{
			"object":                   "model",
			"supported_endpoint_types": []string{"openai", "openai-response", "anthropic-messages"},
			"source":                   "factory_remote",
			"tool_call":                true,
			"supports_tools":           true,
			"streaming":                true,
		}
		if item.ContextWindow > 0 {
			caps["context_window"] = item.ContextWindow
		}
		if item.OwnedBy != "" {
			caps["owned_by"] = item.OwnedBy
		}
		models = append(models, Model{
			UpstreamName: id,
			DisplayName:  displayName,
			Capabilities: caps,
		})
	}

	if len(models) == 0 {
		return nil, fmt.Errorf("no models in remote response")
	}

	return mergeFactoryModels(models, a.curatedModels()), nil
}

func mergeFactoryModels(primary []Model, fallback []Model) []Model {
	seen := make(map[string]bool, len(primary)+len(fallback))
	result := make([]Model, 0, len(primary)+len(fallback))
	for _, m := range primary {
		id := strings.ToLower(strings.TrimSpace(m.UpstreamName))
		if id != "" && !seen[id] {
			seen[id] = true
			result = append(result, m)
		}
	}
	for _, m := range fallback {
		id := strings.ToLower(strings.TrimSpace(m.UpstreamName))
		if id != "" && !seen[id] {
			seen[id] = true
			result = append(result, m)
		}
	}
	return result
}

func (a Factory) curatedModels() []Model {
	models := make([]Model, 0, len(factoryCuratedModels))
	for _, item := range factoryCuratedModels {
		caps := map[string]any{
			"object":                   "model",
			"supported_endpoint_types": []string{"openai", "openai-response", "anthropic-messages"},
			"source":                   "factory_curated",
			"tool_call":                true,
			"supports_tools":           true,
			"streaming":                true,
		}
		if item.Tier != "" {
			caps["tier"] = item.Tier
		}
		if item.Kind != "" {
			caps["kind"] = item.Kind
		}
		models = append(models, Model{
			UpstreamName: item.ID,
			DisplayName:  item.DisplayName,
			Capabilities: caps,
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
	req.Header.Set("X-Factory-Protocol-Version", factoryProtocolVersion)
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
	premiumMap, _ := limitsMap["premium"].(map[string]any)

	standardParsed := parseFactoryLimitTier(standardMap)
	coreParsed := parseFactoryLimitTier(coreMap)
	premiumParsed := parseFactoryLimitTier(premiumMap)

	balance := map[string]any{
		"type":     "factory",
		"standard": standardParsed,
		"core":     coreParsed,
		"premium":  premiumParsed,
		"limits": map[string]any{
			"standard": standardParsed,
			"core":     coreParsed,
			"premium":  premiumParsed,
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
