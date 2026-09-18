package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"xlyra/server/internal/modelcapabilities"
	"xlyra/server/internal/upstream"
)

const (
	nvidiaBaseURL       = "https://integrate.api.nvidia.com"
	nvidiaCatalogURL    = "https://api.ngc.nvidia.com/v2/search/catalog/resources/ENDPOINT"
	nvidiaCatalogTTL    = 6 * time.Hour
	nvidiaCatalogMaxAge = 24 * time.Hour
	nvidiaCatalogRetry  = 5 * time.Minute
)

// NVIDIA uses the public Build catalog for chat capability and free labels, never for key validation.
// The cache is shared by all sites/keys using this registry; no credentials are cached.
type NVIDIA struct {
	client     *http.Client
	catalogURL string
	now        func() time.Time
	mu         sync.Mutex
	loading    chan struct{}
	entries    []nvidiaCatalogEntry
	fetchedAt  time.Time
	retryAt    time.Time
	lastErr    error
}

type nvidiaCatalogEntry struct {
	ResourceType string `json:"resourceType"`
	Name         string `json:"name"`
	DisplayName  string `json:"displayName"`
	Labels       []struct {
		Key              string   `json:"key"`
		Values           []string `json:"values"`
		UnresolvedValues []string `json:"unresolvedValues"`
	} `json:"labels"`
	Attributes []struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	} `json:"attributes"`
}

func NewNVIDIA() *NVIDIA {
	return &NVIDIA{client: &http.Client{Timeout: 20 * time.Second}, catalogURL: nvidiaCatalogURL, now: time.Now}
}

func (*NVIDIA) SiteTypes() []string    { return []string{"nvidia"} }
func (*NVIDIA) DefaultBaseURL() string { return nvidiaBaseURL }
func (*NVIDIA) Capabilities() []Capability {
	return []Capability{CapabilityHealthProbe, CapabilityListModels}
}
func (*NVIDIA) Scope() HealthProbeScope { return HealthProbeSiteScope }
func (a *NVIDIA) ProbeHealth(ctx context.Context, site SiteConfig, credential string) ([]Model, error) {
	return a.ListModels(ctx, site, credential)
}

func (a *NVIDIA) ListModels(ctx context.Context, site SiteConfig, apiKey string) ([]Model, error) {
	entries, fetchedAt, stale, err := a.catalog(ctx, site)
	if err != nil {
		entries = nil
		stale = true
	}
	base := strings.TrimRight(strings.TrimSpace(site.BaseURL), "/")
	if base == "" {
		base = nvidiaBaseURL
	}
	base = strings.TrimSuffix(base, "/v1")
	var inventory struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := a.getJSON(ctx, site, base+"/v1/models", apiKey, &inventory); err != nil {
		return nil, err
	}
	eligible := make(map[string]nvidiaCatalogEntry)
	catalogNames := make(map[string]bool)
	freeModels := make(map[string]bool)
	for _, entry := range entries {
		publisher, free, chat := "", false, false
		for _, label := range entry.Labels {
			if label.Key == "publisher" && len(label.Values) == 1 {
				publisher = label.Values[0]
			}
			for _, value := range append(append([]string{}, label.UnresolvedValues...), label.Values...) {
				switch strings.ToLower(strings.TrimSpace(value)) {
				case "nim_type_preview", "free endpoint":
					free = true
				case "playgroundtype_chat", "chat", "text-to-text":
					chat = true
				}
			}
		}
		if entry.ResourceType != "ENDPOINT" || publisher == "" || entry.Name == "" {
			continue
		}
		catalogName := nvidiaCatalogName(publisher + "/" + entry.Name)
		catalogNames[catalogName] = true
		if !chat {
			continue
		}
		expired := false
		for _, attr := range entry.Attributes {
			if attr.Key == "DEPRECATION" {
				if date, err := time.Parse("01/02/2006", attr.Value); err == nil && !a.now().Before(date) {
					expired = true
				}
			}
		}
		if !expired {
			eligible[catalogName] = entry
			freeModels[catalogName] = free
		}
	}
	models := make([]Model, 0, len(inventory.Data))
	seen := map[string]bool{}
	for _, item := range inventory.Data {
		modelName := strings.TrimSpace(item.ID)
		if modelName == "" {
			continue
		}
		catalogName := nvidiaCatalogName(modelName)
		entry, catalogMatch := eligible[catalogName]
		if (!catalogMatch && (catalogNames[catalogName] || nvidiaNonChatInventoryModel(modelName))) || seen[catalogName] {
			continue
		}
		seen[catalogName] = true
		source := "nvidia_inventory"
		supportedEndpointTypes := modelcapabilities.InferEndpointTypesForModel(modelName)
		if catalogMatch {
			source = "nvidia_catalog"
			supportedEndpointTypes = []string{"openai"}
		}
		if len(supportedEndpointTypes) == 0 || !containsNVIDIAEndpointType(supportedEndpointTypes, "openai") {
			continue
		}
		caps := map[string]any{
			"source": source, "supported_endpoint_types": supportedEndpointTypes,
			"free_endpoint": freeModels[catalogName], "catalog_match": catalogMatch,
			"catalog_stale": stale,
		}
		if !fetchedAt.IsZero() {
			caps["catalog_fetched_at"] = fetchedAt.UTC().Format(time.RFC3339)
		}
		for _, attr := range entry.Attributes {
			if attr.Key == "DEPRECATION" {
				caps["deprecation_date"] = attr.Value
			}
		}
		displayName := modelName
		if caps["free_endpoint"] == true {
			displayName += " [free]"
		}
		models = append(models, Model{UpstreamName: modelName, DisplayName: displayName, Capabilities: caps})
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("NVIDIA model inventory has no supported chat models")
	}
	sort.Slice(models, func(i, j int) bool {
		left, right := models[i].Capabilities["free_endpoint"] == true, models[j].Capabilities["free_endpoint"] == true
		if left != right {
			return left
		}
		return models[i].UpstreamName < models[j].UpstreamName
	})
	return models, nil
}

func nvidiaNonChatInventoryModel(name string) bool {
	name = nvidiaCatalogName(name)
	for _, marker := range []string{"embed", "embedding", "rerank", "guard", "safety", "detector", "tts", "speech", "audio"} {
		if strings.Contains(name, marker) {
			return true
		}
	}
	return false
}

func containsNVIDIAEndpointType(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// Build slugs use '-' or '_' where the inference API may use '.', e.g. glm-5-3.
// Only compare full publisher/model names; always send the original API model ID.
func nvidiaCatalogName(value string) string {
	return strings.NewReplacer("_", "-", ".", "-").Replace(strings.ToLower(strings.TrimSpace(value)))
}

func (a *NVIDIA) catalog(ctx context.Context, site SiteConfig) ([]nvidiaCatalogEntry, time.Time, bool, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, time.Time{}, false, err
		}
		a.mu.Lock()
		now := a.now()
		age := now.Sub(a.fetchedAt)
		if len(a.entries) > 0 && age < nvidiaCatalogTTL {
			entries, at := a.entries, a.fetchedAt
			a.mu.Unlock()
			return entries, at, false, nil
		}
		if now.Before(a.retryAt) {
			entries, at, err := a.entries, a.fetchedAt, a.lastErr
			a.mu.Unlock()
			if len(entries) > 0 && age < nvidiaCatalogMaxAge {
				return entries, at, true, nil
			}
			return nil, time.Time{}, false, err
		}
		if a.loading != nil {
			wait := a.loading
			a.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, time.Time{}, false, ctx.Err()
			case <-wait:
				continue
			}
		}
		a.loading = make(chan struct{})
		a.mu.Unlock()
		entries, err := a.fetchCatalog(ctx, site)
		a.mu.Lock()
		if err == nil {
			a.entries, a.fetchedAt, a.retryAt, a.lastErr = entries, a.now(), time.Time{}, nil
		} else if ctx.Err() == nil {
			a.retryAt, a.lastErr = a.now().Add(nvidiaCatalogRetry), err
		}
		close(a.loading)
		a.loading = nil
		a.mu.Unlock()
		if ctx.Err() != nil {
			return nil, time.Time{}, false, ctx.Err()
		}
	}
}

func (a *NVIDIA) fetchCatalog(ctx context.Context, site SiteConfig) ([]nvidiaCatalogEntry, error) {
	// Read the same JSON catalog as Build instead of loading its slow Next.js page.
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var entries []nvidiaCatalogEntry
	for page := 0; page < 20; page++ {
		query, _ := json.Marshal(map[string]any{
			"query": "*:*", "page": page, "pageSize": 100, "scoredSize": 0,
			"filters": []map[string]string{{"field": "orgName", "value": "qc69jvmznzxy"}},
		})
		var result struct {
			ResultPageTotal int `json:"resultPageTotal"`
			Results         []struct {
				Resources []nvidiaCatalogEntry `json:"resources"`
			} `json:"results"`
		}
		if err := a.getJSON(ctx, site, a.catalogURL+"?q="+url.QueryEscape(string(query)), "", &result); err != nil {
			return nil, err
		}
		if result.ResultPageTotal < 1 || result.ResultPageTotal > 20 {
			return nil, fmt.Errorf("invalid NVIDIA catalog pagination")
		}
		for _, group := range result.Results {
			entries = append(entries, group.Resources...)
		}
		if page+1 >= result.ResultPageTotal {
			if len(entries) == 0 {
				return nil, fmt.Errorf("NVIDIA catalog returned no entries")
			}
			return entries, nil
		}
	}
	return nil, fmt.Errorf("NVIDIA catalog exceeded pagination limit")
}

func (a *NVIDIA) getJSON(ctx context.Context, site SiteConfig, endpoint string, apiKey string, dest any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if strings.TrimSpace(apiKey) != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := httpClientForSite(site, a.client).Do(req)
	if err != nil {
		return fmt.Errorf("fetch NVIDIA endpoint: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return upstream.NewHTTPError("NVIDIA endpoint returned", resp.StatusCode, resp.Header, body)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(dest); err != nil {
		return fmt.Errorf("decode NVIDIA endpoint: %w", err)
	}
	return nil
}
