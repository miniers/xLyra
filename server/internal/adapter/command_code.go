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
	}
}

func (CommandCode) DefaultBaseURL() string {
	return commandCodeDefaultBaseURL
}

func (CommandCode) Scope() HealthProbeScope {
	return HealthProbeCredentialScope
}

type commandCodeModelItem struct {
	ID            string `json:"id"`
	Object        string `json:"object"`
	Created       int64  `json:"created"`
	OwnedBy       string `json:"owned_by"`
	ContextLength int    `json:"context_length"`
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
	return a.ListModels(ctx, site, credential)
}

func (a CommandCode) ListModels(ctx context.Context, site SiteConfig, credential string) ([]Model, error) {
	items, err := a.fetchModelCatalog(ctx, site, credential)
	if err != nil || len(items) == 0 {
		return commandCodeFallbackModelList(), nil
	}

	models := make([]Model, 0, len(items))
	for _, item := range items {
		modelID := strings.TrimSpace(item.ID)
		if modelID == "" {
			continue
		}
		caps := map[string]any{
			"source":                   "command_code_spec",
			"supported_endpoint_types": []string{"openai", "anthropic-messages", "openai-responses"},
			"tool_call":                true,
			"supports_tools":           true,
			"streaming":                true,
		}
		if item.ContextLength > 0 {
			caps["context_length"] = item.ContextLength
		}
		models = append(models, Model{
			UpstreamName: modelID,
			DisplayName:  modelID,
			Capabilities: caps,
		})
	}
	return models, nil
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
		models = append(models, Model{
			UpstreamName: item.ID,
			DisplayName:  item.Name,
			Capabilities: map[string]any{
				"source":                   "command_code_spec",
				"supported_endpoint_types": []string{"openai", "anthropic-messages", "openai-responses"},
				"tool_call":                true,
				"supports_tools":           true,
				"streaming":                true,
			},
		})
	}
	return models
}

func applyCommandCodeAuthHeaders(req *http.Request, apiKey string) {
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(apiKey))
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
