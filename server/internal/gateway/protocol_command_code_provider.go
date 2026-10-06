package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"xlyra/server/internal/adapter"
	routeengine "xlyra/server/internal/router"
	"xlyra/server/internal/store"
)

// commandCodeProviderProtocolAdapter keeps Command Code's provider endpoint
// on the standard OpenAI/Anthropic conversion path. The legacy adapter remains
// responsible for Go and unknown subscriptions.
type commandCodeProviderProtocolAdapter struct {
	request      gatewayRequest
	candidate    routeengine.Candidate
	delegate     gatewayProtocolAdapter
	providerBase string
}

var (
	_ gatewayProtocolAdapter                = (*commandCodeProviderProtocolAdapter)(nil)
	_ gatewayProtocolCredentialCapabilities = (*commandCodeProviderProtocolAdapter)(nil)
	_ gatewayProtocolBodyAdapter            = (*commandCodeProviderProtocolAdapter)(nil)
	_ gatewayProtocolHeaderAdapter          = (*commandCodeProviderProtocolAdapter)(nil)
)

func resolveCommandCodeCredentialProtocol(request gatewayRequest, candidate routeengine.Candidate, metadata store.JSON) (gatewayProtocolAdapter, error) {
	if !commandCodeProviderSubscriptionUsable(metadata) {
		return newCommandCodeProtocolAdapter(request, candidate), nil
	}

	providerCandidate := candidate
	providerCandidate.Site.SiteType = "openai"
	delegate, err := (openAIProtocolResolver{}).Resolve(context.Background(), request, providerCandidate)
	if err != nil {
		return nil, err
	}
	return &commandCodeProviderProtocolAdapter{
		request:      request,
		candidate:    providerCandidate,
		delegate:     delegate,
		providerBase: commandCodeProviderBaseURL(candidate.Site.BaseURL),
	}, nil
}

func commandCodeProviderSubscriptionUsable(metadata store.JSON) bool {
	var root map[string]any
	if len(metadata) == 0 || json.Unmarshal(metadata, &root) != nil {
		return false
	}
	raw, ok := root["command_code_subscription"].(map[string]any)
	if !ok || raw == nil {
		return false
	}
	knownPlan, ok := adapter.CommandCodeSubscriptionPlan(anyString(raw["plan_id"]))
	if !ok || strings.EqualFold(knownPlan, "Go") {
		return false
	}
	status := strings.ToLower(strings.TrimSpace(anyString(raw["status"])))
	if status == "" || status == "canceled" || status == "cancelled" || status == "incomplete_expired" || status == "expired" {
		return false
	}
	if currentPeriodEnd, exists := raw["current_period_end"]; exists {
		if currentPeriodEnd == nil || strings.EqualFold(anyString(currentPeriodEnd), "null") {
			return false
		}
		periodEnd, ok := commandCodeProviderPeriodEnd(currentPeriodEnd)
		if !ok || !periodEnd.After(time.Now()) {
			return false
		}
	}
	return true
}

func commandCodeProviderPeriodEnd(value any) (time.Time, bool) {
	switch typed := value.(type) {
	case string:
		text := strings.TrimSpace(typed)
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
			if parsed, err := time.Parse(layout, text); err == nil {
				return parsed, true
			}
		}
		if numeric, err := strconv.ParseFloat(text, 64); err == nil {
			return commandCodeProviderUnixTime(numeric), true
		}
	case float64:
		return commandCodeProviderUnixTime(typed), typed > 0
	case json.Number:
		numeric, err := typed.Float64()
		return commandCodeProviderUnixTime(numeric), err == nil && numeric > 0
	case int64:
		return commandCodeProviderUnixTime(float64(typed)), typed > 0
	case int:
		return commandCodeProviderUnixTime(float64(typed)), typed > 0
	}
	return time.Time{}, false
}

func commandCodeProviderUnixTime(value float64) time.Time {
	if value > 1e12 {
		return time.UnixMilli(int64(value))
	}
	return time.Unix(int64(value), 0)
}

func commandCodeProviderBaseURL(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return "https://api.commandcode.ai"
	}
	return base
}

func (a *commandCodeProviderProtocolAdapter) ProtocolName() string {
	if a == nil || a.delegate == nil {
		return "command_code_provider"
	}
	return "command_code_provider_" + a.delegate.ProtocolName()
}

func (a *commandCodeProviderProtocolAdapter) BuildUpstreamPayload(request gatewayRequest, _ routeengine.Candidate) (map[string]any, error) {
	return a.delegate.BuildUpstreamPayload(request, a.candidate)
}

func (a *commandCodeProviderProtocolAdapter) UpstreamPath(baseURL string) string {
	base := strings.TrimSpace(baseURL)
	if base == "" {
		base = a.providerBase
	}
	if base == "" {
		base = commandCodeProviderBaseURL("")
	}
	return prefixCommandCodeProviderPath(a.delegate.UpstreamPath(base))
}

func prefixCommandCodeProviderPath(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	path := strings.TrimRight(parsed.Path, "/")
	if strings.Contains(path, "/provider/v1/") || strings.HasSuffix(path, "/provider/v1") {
		return parsed.String()
	}
	if index := strings.LastIndex(path, "/v1/"); index >= 0 {
		parsed.Path = path[:index] + "/provider" + path[index:]
	} else if strings.HasSuffix(path, "/v1") {
		parsed.Path = strings.TrimSuffix(path, "/v1") + "/provider/v1"
	}
	return parsed.String()
}

func (a *commandCodeProviderProtocolAdapter) TransformBufferedResponse(statusCode int, headers http.Header, body []byte) (gatewayBufferedResponse, error) {
	return a.delegate.TransformBufferedResponse(statusCode, headers, body)
}

func (a *commandCodeProviderProtocolAdapter) ProxyStream(ctx context.Context, w http.ResponseWriter, resp *http.Response, startedAt time.Time, _ routeengine.Candidate) (streamCaptureState, bool, error) {
	return a.delegate.ProxyStream(ctx, w, resp, startedAt, a.candidate)
}

func (a *commandCodeProviderProtocolAdapter) CredentialEndpointTypes() []string {
	if capabilities, ok := a.delegate.(gatewayProtocolCredentialCapabilities); ok {
		return capabilities.CredentialEndpointTypes()
	}
	return textCredentialEndpointTypes()
}

func (a *commandCodeProviderProtocolAdapter) BuildUpstreamBody(request gatewayRequest, payload map[string]any) ([]byte, string, error) {
	if bodyAdapter, ok := a.delegate.(gatewayProtocolBodyAdapter); ok {
		return bodyAdapter.BuildUpstreamBody(request, payload)
	}
	body, err := json.Marshal(payload)
	return body, "application/json", err
}

func (a *commandCodeProviderProtocolAdapter) ApplyUpstreamHeaders(req *http.Request, upstreamKey string, accountID string, stream bool) {
	if req == nil {
		return
	}
	if headerAdapter, ok := a.delegate.(gatewayProtocolHeaderAdapter); ok {
		headerAdapter.ApplyUpstreamHeaders(req, upstreamKey, accountID, stream)
	}
	key := strings.TrimSpace(upstreamKey)
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("x-api-key", key)
	for _, header := range []string{
		"x-command-code-version", "x-cli-environment", "x-project-slug", "x-taste-learning", "x-session-id", "traceparent",
	} {
		req.Header.Del(header)
	}
}

func (a *commandCodeProviderProtocolAdapter) usesRawBufferedResponse() bool {
	if raw, ok := a.delegate.(rawBufferedResponseProtocol); ok {
		return raw.usesRawBufferedResponse()
	}
	return false
}
