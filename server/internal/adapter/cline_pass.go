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
	clinePassSiteType       = "cline_pass"
	clinePassDefaultBaseURL = "https://api.cline.bot/api/v1"
)

var clinePassFallbackModels = []struct {
	ID   string
	Name string
}{
	{ID: "qwen3.8-max", Name: "Qwen 3.8 Max"},
	{ID: "qwen3.7-max", Name: "Qwen 3.7 Max"},
	{ID: "qwen3.7-plus", Name: "Qwen 3.7 Plus"},
	{ID: "deepseek-v4-pro", Name: "DeepSeek V4 Pro"},
	{ID: "deepseek-v4.1-flash", Name: "DeepSeek V4.1 Flash"},
	{ID: "glm-5.3", Name: "GLM 5.3"},
	{ID: "glm-5.3-flash", Name: "GLM 5.3 Flash"},
	{ID: "kimi-k3", Name: "Kimi K3"},
	{ID: "mimo-v2.5-pro", Name: "MiMo V2.5 Pro"},
	{ID: "mimo-v2.5", Name: "MiMo V2.5"},
	{ID: "minimax-m3", Name: "MiniMax M3"},
	{ID: "muse-spark-1.3-contributor", Name: "Muse Spark 1.3 Contributor"},
}

var clinePassRetiredModels = map[string]struct{}{
	"glm-5.2":           {},
	"kimi-k2.7-code":    {},
	"kimi-k2.6":         {},
	"deepseek-v4-flash": {},
}

type ClinePass struct {
	client *http.Client
}

func NewClinePass() ClinePass {
	return ClinePass{
		client: &http.Client{Timeout: 20 * time.Second},
	}
}

func (ClinePass) SiteTypes() []string {
	return []string{clinePassSiteType}
}

func (ClinePass) Capabilities() []Capability {
	return []Capability{
		CapabilityValidateCredential,
		CapabilityListModels,
		CapabilityHealthProbe,
	}
}

func (ClinePass) DefaultBaseURL() string {
	return clinePassDefaultBaseURL
}

func (ClinePass) Scope() HealthProbeScope {
	return HealthProbeCredentialScope
}

type clinePassModelItem struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	OwnedBy string         `json:"owned_by"`
	Meta    map[string]any `json:"metadata"`
}

func clinePassBaseURL(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return clinePassDefaultBaseURL
	}
	if !strings.HasSuffix(base, "/api/v1") && !strings.HasSuffix(base, "/v1") {
		base += "/api/v1"
	}
	return base
}

func (a ClinePass) ValidateCredentials(ctx context.Context, site SiteConfig, apiKey string) error {
	client := httpClientForSite(site, a.client)
	baseURL := strings.TrimRight(strings.TrimSpace(site.BaseURL), "/")
	if baseURL == "" {
		baseURL = "https://api.cline.bot"
	}
	baseURL = strings.TrimSuffix(baseURL, "/api/v1")
	baseURL = strings.TrimSuffix(baseURL, "/v1")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/v1/users/me", nil)
	if err != nil {
		return fmt.Errorf("create users/me request: %w", err)
	}
	if strings.TrimSpace(apiKey) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(apiKey))
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("validate credential: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return nil
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return upstream.NewHTTPError("upstream returned", resp.StatusCode, resp.Header, body)
	}

	// If /users/me returned another code, fall back to testing ListModels
	_, err = a.ListModels(ctx, site, apiKey)
	return err
}

func (a ClinePass) ProbeHealth(ctx context.Context, site SiteConfig, apiKey string) ([]Model, error) {
	return a.ListModels(ctx, site, apiKey)
}

func (a ClinePass) ListModels(ctx context.Context, site SiteConfig, apiKey string) ([]Model, error) {
	baseURL := clinePassBaseURL(site.BaseURL)
	modelsURL := baseURL + "/models"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL, nil)
	if err != nil {
		return a.fallbackModels(), nil
	}

	if strings.TrimSpace(apiKey) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(apiKey))
	}
	req.Header.Set("Accept", "application/json")

	resp, err := httpClientForSite(site, a.client).Do(req)
	if err != nil {
		return a.fallbackModels(), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return a.fallbackModels(), nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return a.fallbackModels(), nil
	}

	var payload struct {
		Data []clinePassModelItem `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || len(payload.Data) == 0 {
		return a.fallbackModels(), nil
	}

	models := make([]Model, 0, len(payload.Data))
	for _, item := range payload.Data {
		rawModelID := strings.TrimSpace(item.ID)
		if !strings.HasPrefix(rawModelID, "cline-pass/") {
			continue
		}
		modelID := strings.TrimPrefix(rawModelID, "cline-pass/")
		if _, retired := clinePassRetiredModels[modelID]; retired {
			continue
		}
		if _, retired := clinePassRetiredModels[rawModelID]; retired {
			continue
		}
		endpointTypes := []string{"openai", "anthropic-messages"}
		capabilities := map[string]any{
			"object":                   item.Object,
			"supported_endpoint_types": endpointTypes,
			"source":                   "cline_pass",
			"tool_call":                true,
			"supports_tools":           true,
			"streaming":                true,
		}
		if item.OwnedBy != "" {
			capabilities["owned_by"] = item.OwnedBy
		}
		if len(item.Meta) > 0 {
			capabilities["metadata"] = item.Meta
		}
		models = append(models, Model{
			UpstreamName: modelID,
			DisplayName:  modelID,
			Capabilities: capabilities,
		})
	}

	if len(models) == 0 {
		return a.fallbackModels(), nil
	}
	return models, nil
}

func (a ClinePass) fallbackModels() []Model {
	models := make([]Model, 0, len(clinePassFallbackModels))
	for _, item := range clinePassFallbackModels {
		models = append(models, Model{
			UpstreamName: item.ID,
			DisplayName:  item.Name,
			Capabilities: map[string]any{
				"object":                   "model",
				"supported_endpoint_types": []string{"openai", "anthropic-messages"},
				"source":                   "cline_pass_curated",
				"tool_call":                true,
				"supports_tools":           true,
				"streaming":                true,
			},
		})
	}
	return models
}
