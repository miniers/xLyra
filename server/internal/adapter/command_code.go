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
	commandCodeSiteType        = "command_code"
	commandCodeDefaultBaseURL  = "https://api.commandcode.ai"
	CommandCodeProtocolVersion = "1.53.1"
	CommandCodeUserAgent       = "cli"
)

// Fallback curated model catalog when upstream /provider/v1/models is unavailable
var commandCodeFallbackModels = []struct {
	ID   string
	Name string
}{
	{ID: "claude-sonnet-4-6", Name: "Claude Sonnet 4.6"},
	{ID: "claude-opus-4-8", Name: "Claude Opus 4.8"},
	{ID: "claude-opus-4-7", Name: "Claude Opus 4.7"},
	{ID: "claude-haiku-4-5-20251001", Name: "Claude Haiku 4.5"},
	{ID: "gpt-5.5", Name: "GPT-5.5"},
	{ID: "gpt-5.4", Name: "GPT-5.4"},
	{ID: "gpt-5.4-mini", Name: "GPT-5.4 Mini"},
	{ID: "gpt-5.3-codex", Name: "GPT-5.3 Codex"},
	{ID: "deepseek/deepseek-v4-pro", Name: "DeepSeek V4 Pro"},
	{ID: "deepseek/deepseek-v4-flash", Name: "DeepSeek V4 Flash"},
	{ID: "moonshotai/Kimi-K2.6", Name: "Kimi K2.6"},
	{ID: "moonshotai/Kimi-K2.5", Name: "Kimi K2.5"},
	{ID: "zai-org/GLM-5.1", Name: "GLM 5.1"},
	{ID: "zai-org/GLM-5", Name: "GLM 5"},
	{ID: "MiniMaxAI/MiniMax-M3", Name: "MiniMax M3"},
	{ID: "MiniMaxAI/MiniMax-M2.7", Name: "MiniMax M2.7"},
	{ID: "MiniMaxAI/MiniMax-M2.5", Name: "MiniMax M2.5"},
	{ID: "Qwen/Qwen3.6-Max-Preview", Name: "Qwen 3.6 Max Preview"},
	{ID: "Qwen/Qwen3.6-Plus", Name: "Qwen 3.6 Plus"},
	{ID: "Qwen/Qwen3.7-Max", Name: "Qwen 3.7 Max"},
	{ID: "stepfun/Step-3.7-Flash", Name: "Step 3.7 Flash"},
	{ID: "stepfun/Step-3.5-Flash", Name: "Step 3.5 Flash"},
	{ID: "xiaomi/mimo-v2.5-pro", Name: "MiMo V2.5 Pro"},
	{ID: "xiaomi/mimo-v2.5", Name: "MiMo V2.5"},
	{ID: "google/gemini-3.5-flash", Name: "Gemini 3.5 Flash"},
	{ID: "google/gemini-3.1-flash-lite", Name: "Gemini 3.1 Flash Lite"},
}

type CommandCode struct {
	client *http.Client
}

func NewCommandCode() CommandCode {
	return CommandCode{
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (CommandCode) SiteTypes() []string {
	return []string{commandCodeSiteType}
}

func (CommandCode) Capabilities() []Capability {
	return []Capability{
		CapabilityValidateCredential,
		CapabilityListModels,
		CapabilityHealthProbe,
		CapabilityFetchMetadata,
	}
}

func (CommandCode) DefaultBaseURL() string {
	return commandCodeDefaultBaseURL
}

func (CommandCode) Scope() HealthProbeScope {
	return HealthProbeCredentialScope
}

type commandCodeModelItem struct {
	ID                 string                     `json:"id"`
	Name               string                     `json:"name"`
	DisplayName        string                     `json:"display_name"`
	Object             string                     `json:"object"`
	Created            int64                      `json:"created"`
	OwnedBy            string                     `json:"owned_by"`
	ContextLength      int                        `json:"context_length"`
	Modalities         commandCodeModelModalities `json:"modalities"`
	SupportedEndpoints []string                   `json:"supported_endpoints"`
}

type commandCodeModelModalities struct {
	Input []string `json:"input"`
}

func (a CommandCode) ValidateCredentials(ctx context.Context, site SiteConfig, apiKey string) error {
	client := httpClientForSite(site, a.client)
	baseURL := commandCodeBaseURL(site.BaseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/alpha/whoami", nil)
	if err != nil {
		return fmt.Errorf("create whoami request: %w", err)
	}
	applyCommandCodeAuthHeaders(req, apiKey)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("call whoami: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return upstream.NewHTTPError("Command Code whoami returned", resp.StatusCode, resp.Header, body)
	}
	return nil
}

func (a CommandCode) ProbeHealth(ctx context.Context, site SiteConfig, credential string) ([]Model, error) {
	if err := a.ValidateCredentials(ctx, site, credential); err != nil {
		return nil, err
	}
	// /provider/v1/models is a catalog hint rather than a credential check.
	// Keep the curated list when this endpoint is unavailable for Go keys.
	return a.ListModels(ctx, site, credential)
}

func (a CommandCode) ListModels(ctx context.Context, site SiteConfig, credential string) ([]Model, error) {
	items, err := a.fetchModelCatalog(ctx, site, credential)
	if err != nil || len(items) == 0 {
		return commandCodeFallbackModelList(), nil
	}

	models := commandCodeModelsFromItems(items)
	if len(models) == 0 {
		return commandCodeFallbackModelList(), nil
	}
	return models, nil
}

func commandCodeModelsFromItems(items []commandCodeModelItem) []Model {
	models := make([]Model, 0, len(items))
	for _, item := range items {
		modelID := strings.TrimSpace(item.ID)
		if modelID == "" {
			modelID = strings.TrimSpace(item.Name)
		}
		if modelID == "" {
			continue
		}
		endpointTypes := normalizeCommandCodeEndpointTypes(item.SupportedEndpoints)
		if len(endpointTypes) == 0 {
			if len(item.SupportedEndpoints) > 0 {
				continue
			}
			endpointTypes = commandCodeLegacyEndpointTypes()
		}
		caps := map[string]any{
			"source":                   "command_code_spec",
			"supported_endpoint_types": endpointTypes,
			"tool_call":                true,
			"supports_tools":           true,
			"streaming":                true,
		}
		if len(item.Modalities.Input) > 0 {
			caps["modalities_input"] = append([]string(nil), item.Modalities.Input...)
		}
		if item.ContextLength > 0 {
			caps["context_length"] = item.ContextLength
		}
		pricing, _ := commandCodeModelPricing(modelID)
		models = append(models, Model{
			UpstreamName: modelID,
			DisplayName:  firstNonEmptyString(strings.TrimSpace(item.DisplayName), strings.TrimSpace(item.Name), modelID),
			Capabilities: caps,
			Pricing:      pricing,
		})
	}
	return models
}

func (a CommandCode) fetchModelCatalog(ctx context.Context, site SiteConfig, apiKey string) ([]commandCodeModelItem, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("api key is empty")
	}
	baseURL := commandCodeBaseURL(site.BaseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/provider/v1/models", nil)
	if err != nil {
		return nil, fmt.Errorf("create models request: %w", err)
	}
	applyCommandCodeAuthHeaders(req, apiKey)

	resp, err := httpClientForSite(site, a.client).Do(req)
	if err != nil {
		return nil, fmt.Errorf("call models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, upstream.NewHTTPError("Command Code models returned", resp.StatusCode, resp.Header, body)
	}

	var payload struct {
		Data []commandCodeModelItem `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode Command Code models: %w", err)
	}
	return payload.Data, nil
}

func commandCodeFallbackModelList() []Model {
	models := make([]Model, 0, len(commandCodeFallbackModels))
	for _, item := range commandCodeFallbackModels {
		pricing, _ := commandCodeModelPricing(item.ID)
		models = append(models, Model{
			UpstreamName: item.ID,
			DisplayName:  item.Name,
			Capabilities: map[string]any{
				"source":                   "command_code_spec",
				"supported_endpoint_types": []string{"openai", "anthropic-messages", "openai-response"},
				"tool_call":                true,
				"supports_tools":           true,
				"streaming":                true,
			},
			Pricing: pricing,
		})
	}
	return models
}

func applyCommandCodeAuthHeaders(req *http.Request, apiKey string) {
	key := strings.TrimSpace(apiKey)
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("x-api-key", key)
	req.Header.Set("User-Agent", CommandCodeUserAgent)
	req.Header.Set("x-command-code-version", CommandCodeProtocolVersion)
	req.Header.Set("x-cli-environment", "production")
	req.Header.Set("Accept", "application/json")
}

func commandCodeBaseURL(raw string) string {
	baseURL := strings.TrimRight(strings.TrimSpace(raw), "/")
	if baseURL == "" {
		return commandCodeDefaultBaseURL
	}
	return baseURL
}

func commandCodeLegacyEndpointTypes() []string {
	return []string{"openai", "anthropic-messages", "openai-response"}
}

func normalizeCommandCodeEndpointTypes(raw []string) []string {
	result := make([]string, 0, len(raw))
	seen := map[string]struct{}{}
	for _, value := range raw {
		normalized := strings.ToLower(strings.TrimSpace(value))
		normalized = strings.ReplaceAll(normalized, "_", "-")
		switch {
		case normalized == "openai", strings.Contains(normalized, "chat"):
			normalized = "openai"
		case normalized == "openai-response", strings.Contains(normalized, "response"):
			normalized = "openai-response"
		case normalized == "anthropic-messages", normalized == "messages", strings.Contains(normalized, "message"):
			normalized = "anthropic-messages"
		default:
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}
	return result
}

type commandCodeSubscriptionResponse struct {
	Success *bool `json:"success"`
	Data    *struct {
		PlanID           string          `json:"planId"`
		Status           string          `json:"status"`
		CurrentPeriodEnd json.RawMessage `json:"currentPeriodEnd"`
	} `json:"data"`
}

var commandCodeSubscriptionPlans = []struct {
	Prefix string
	Plan   string
}{
	{Prefix: "individual-provider", Plan: "Provider"},
	{Prefix: "individual-pro-v1", Plan: "Pro"},
	{Prefix: "individual-goat", Plan: "GOAT"},
	{Prefix: "individual-ultra", Plan: "Ultra"},
	{Prefix: "individual-max", Plan: "Max"},
	{Prefix: "individual-pro", Plan: "Pro"},
	{Prefix: "individual-go", Plan: "Go"},
	{Prefix: "teams-pro", Plan: "Teams Pro"},
}

func (a CommandCode) FetchCredentialMetadata(ctx context.Context, site SiteConfig, apiKey string) (map[string]any, error) {
	baseURL := commandCodeBaseURL(site.BaseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/alpha/billing/subscriptions", nil)
	if err != nil {
		return nil, fmt.Errorf("create subscriptions request: %w", err)
	}
	applyCommandCodeAuthHeaders(req, apiKey)
	resp, err := httpClientForSite(site, a.client).Do(req)
	if err != nil {
		return nil, fmt.Errorf("call subscriptions: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, upstream.NewHTTPError("Command Code subscriptions returned", resp.StatusCode, resp.Header, body)
	}

	var payload commandCodeSubscriptionResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode Command Code subscriptions: %w", err)
	}
	if payload.Success != nil && !*payload.Success {
		return nil, fmt.Errorf("Command Code subscriptions response is unavailable")
	}
	subscription := map[string]any{
		"plan_id":            "",
		"plan":               "",
		"status":             "",
		"current_period_end": nil,
		"synced_at":          time.Now().UTC().Format(time.RFC3339),
	}
	if payload.Data == nil {
		return map[string]any{"command_code_subscription": subscription}, nil
	}
	planID := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(payload.Data.PlanID), "_", "-"))
	plan, _ := CommandCodeSubscriptionPlan(planID)
	subscription["plan_id"] = planID
	subscription["plan"] = plan
	subscription["status"] = strings.TrimSpace(payload.Data.Status)
	if len(payload.Data.CurrentPeriodEnd) > 0 && string(payload.Data.CurrentPeriodEnd) != "null" {
		var periodEnd any
		if err := json.Unmarshal(payload.Data.CurrentPeriodEnd, &periodEnd); err != nil {
			return nil, fmt.Errorf("decode Command Code currentPeriodEnd: %w", err)
		}
		subscription["current_period_end"] = periodEnd
	}
	return map[string]any{"command_code_subscription": subscription}, nil
}

func CommandCodeSubscriptionPlan(planID string) (string, bool) {
	planID = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(planID), "_", "-"))
	for _, item := range commandCodeSubscriptionPlans {
		if strings.HasPrefix(planID, item.Prefix) {
			return item.Plan, true
		}
	}
	return "", false
}
