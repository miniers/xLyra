package gateway

import (
	"context"
	"net/http"
	"strings"
	"testing"

	routeengine "xlyra/server/internal/router"
)

func TestClassifyFactoryRoute(t *testing.T) {
	t.Parallel()

	cases := []struct {
		model string
		want  factoryRouteTarget
	}{
		{"claude-opus-5-5", factoryRouteClaude},
		{"claude-3-7-sonnet-20250219", factoryRouteClaude},
		{"atlas-07-21", factoryRouteClaude},
		{"aster-07-15", factoryRouteClaude},
		{"auto", factoryRouteClaude},
		{"auto-fast", factoryRouteClaude},
		{"anthropic-latest-premium", factoryRouteClaude},
		{"anthropic-latest-fast", factoryRouteClaude},
		{"openai-latest-premium", factoryRouteGPT},
		{"openai-latest-balanced", factoryRouteGPT},
		{"oss-latest-premium", factoryRouteCoreOpenSource},
		{"oss-latest-fast", factoryRouteCoreOpenSource},
		{"shield-risk", factoryRouteCoreOpenSource},
		{"shield-downgrade", factoryRouteCoreOpenSource},
		{"inkling", factoryRouteCoreOpenSource},
		{"minimax-m3", factoryRouteMiniMax},
		{"minimax-m2.7", factoryRouteMiniMax},
		{"gpt-6.1-sol", factoryRouteGPT},
		{"gpt-5.6-sol", factoryRouteGPT},
		{"gpt-5.4", factoryRouteGPT},
		{"gpt-5.3-codex", factoryRouteGPT},
		{"grok-4.7", factoryRouteGrok},
		{"grok-4.6", factoryRouteGrok},
		{"gemini-3.8-flash", factoryRouteGemini},
		{"garnet-07-15", factoryRouteGemini},
		{"deepseek-v4.1-flash", factoryRouteCoreOpenSource},
		{"glm-5.3", factoryRouteCoreOpenSource},
		{"kimi-k3", factoryRouteCoreOpenSource},
		{"qwen3.8-max", factoryRouteCoreOpenSource},
		{"nemotron-3-ultra", factoryRouteCoreOpenSource},
		// Prefix stripped
		{"openai/gpt-5.5", factoryRouteGPT},
		{"anthropic/claude-sonnet-5", factoryRouteClaude},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.model, func(t *testing.T) {
			t.Parallel()
			got := classifyFactoryRoute(tc.model)
			if got != tc.want {
				t.Errorf("classifyFactoryRoute(%q) = %d, want %d", tc.model, got, tc.want)
			}
		})
	}
}

func TestFactoryProtocolAdapterUpstreamPathsAndHeaders(t *testing.T) {
	t.Parallel()

	cases := []struct {
		model            string
		wantPath         string
		wantProvider     string
		wantAnthropicVer bool
		wantOpenAIPlat   bool
	}{
		{"claude-opus-5-5", "/api/llm/a/v1/messages", "anthropic", true, false},
		{"minimax-m3", "/api/llm/a/v1/messages", "fireworks", true, false},
		{"gpt-6.1-sol", "/api/llm/o/v1/responses", "openai", false, true},
		{"grok-4.7", "/api/llm/o/v1/responses", "xai", false, false},
		{"gemini-3.8-flash", "/api/llm/o/v1/chat/completions", "google", false, false},
		{"deepseek-v4.1-flash", "/api/llm/o/v1/chat/completions", "fireworks", false, false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.model, func(t *testing.T) {
			t.Parallel()

			req := gatewayRequest{
				DownstreamPath: gatewayEndpointChatCompletions,
				RequestedModel: tc.model,
				Payload: map[string]any{
					"model":    tc.model,
					"messages": []any{map[string]any{"role": "user", "content": "hi"}},
				},
			}
			cand := routeengine.Candidate{
				Site: routeengine.CandidateSite{
					SiteType: "factory",
					BaseURL:  "https://api.factory.ai",
				},
				Model: routeengine.CandidateModel{
					UpstreamName: tc.model,
				},
			}

			adapter := newFactoryProtocolAdapter(req, cand)
			if path := adapter.UpstreamPath(cand.Site.BaseURL); path != "https://api.factory.ai"+tc.wantPath {
				t.Errorf("UpstreamPath = %q, want %q", path, "https://api.factory.ai"+tc.wantPath)
			}

			httpReq, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://api.factory.ai", nil)
			if err != nil {
				t.Fatal(err)
			}
			adapter.ApplyUpstreamHeaders(httpReq, "test-api-key", "", false)

			if auth := httpReq.Header.Get("Authorization"); auth != "Bearer test-api-key" {
				t.Errorf("Authorization = %q, want Bearer test-api-key", auth)
			}
			if prov := httpReq.Header.Get("x-api-provider"); prov != tc.wantProvider {
				t.Errorf("x-api-provider = %q, want %q", prov, tc.wantProvider)
			}
			if tc.wantAnthropicVer && httpReq.Header.Get("anthropic-version") != factoryAnthropicVersion {
				t.Errorf("missing or wrong anthropic-version header")
			}
			if tc.wantOpenAIPlat && httpReq.Header.Get("OpenAI-Platform") != factoryOpenAIPlatform {
				t.Errorf("missing or wrong OpenAI-Platform header")
			}
			if client := httpReq.Header.Get("X-Factory-Client"); client != "sdk" {
				t.Errorf("X-Factory-Client = %q, want sdk", client)
			}
			if sdk := httpReq.Header.Get("X-Factory-Sdk"); sdk != "typescript/0.9.1" {
				t.Errorf("X-Factory-Sdk = %q, want typescript/0.9.1", sdk)
			}
			if httpReq.Header.Get("x-session-id") == "" {
				t.Errorf("missing x-session-id header")
			}
			if httpReq.Header.Get("x-assistant-message-id") == "" {
				t.Errorf("missing x-assistant-message-id header")
			}
		})
	}
}

func TestFactoryProtocolAdapterStripsStreamOptions(t *testing.T) {
	t.Parallel()

	models := []string{
		"claude-opus-5-5",
		"gpt-6.1-sol",
		"grok-4.7",
		"gemini-3.8-flash",
		"deepseek-v4.1-flash",
		"kimi-k3",
	}

	for _, model := range models {
		model := model
		t.Run(model, func(t *testing.T) {
			t.Parallel()

			// Downstream requests stream with stream_options: include_usage = true
			req := gatewayRequest{
				DownstreamPath: gatewayEndpointChatCompletions,
				RequestedModel: model,
				Stream:         true,
				Payload: map[string]any{
					"model":          model,
					"messages":       []any{map[string]any{"role": "user", "content": "hello"}},
					"stream":         true,
					"stream_options": map[string]any{"include_usage": true},
				},
			}
			cand := routeengine.Candidate{
				Site: routeengine.CandidateSite{
					SiteType: "factory",
					BaseURL:  "https://api.factory.ai",
				},
				Model: routeengine.CandidateModel{
					UpstreamName: model,
				},
			}

			adapter := newFactoryProtocolAdapter(req, cand)
			payload, err := adapter.BuildUpstreamPayload(req, cand)
			if err != nil {
				t.Fatalf("BuildUpstreamPayload error: %v", err)
			}

			if _, exists := payload["stream_options"]; exists {
				t.Errorf("model %q: stream_options was NOT stripped from upstream payload: %#v", model, payload["stream_options"])
			}
		})
	}
}

func TestFactoryProtocolAdapterEnsuresSystemPrompt(t *testing.T) {
	t.Parallel()

	req := gatewayRequest{
		DownstreamPath: gatewayEndpointChatCompletions,
		RequestedModel: "gpt-6.1-sol",
		Payload: map[string]any{
			"model":    "gpt-6.1-sol",
			"messages": []any{map[string]any{"role": "user", "content": "write code"}},
		},
	}
	cand := routeengine.Candidate{
		Site: routeengine.CandidateSite{
			SiteType: "factory",
			BaseURL:  "https://api.factory.ai",
		},
		Model: routeengine.CandidateModel{
			UpstreamName: "gpt-6.1-sol",
		},
	}

	adapter := newFactoryProtocolAdapter(req, cand)
	payload, err := adapter.BuildUpstreamPayload(req, cand)
	if err != nil {
		t.Fatalf("BuildUpstreamPayload error: %v", err)
	}

	instructions, ok := payload["instructions"].(string)
	if !ok || !strings.Contains(instructions, "You are Droid") {
		t.Errorf("instructions missing Factory system declaration: %#v", payload["instructions"])
	}
}

func TestFactoryProtocolAdapterStripsEmptyTools(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		tools      any
		toolChoice any
	}{
		{name: "empty_slice", tools: []any{}, toolChoice: "auto"},
		{name: "nil_tools", tools: nil, toolChoice: "auto"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			payloadMap := map[string]any{
				"model":       "claude-opus-5-5",
				"messages":    []any{map[string]any{"role": "user", "content": "hi"}},
				"tools":       tc.tools,
				"tool_choice": tc.toolChoice,
			}
			req := gatewayRequest{
				DownstreamPath: gatewayEndpointChatCompletions,
				RequestedModel: "claude-opus-5-5",
				Payload:        payloadMap,
			}
			cand := routeengine.Candidate{
				Site: routeengine.CandidateSite{
					SiteType: "factory",
					BaseURL:  "https://api.factory.ai",
				},
				Model: routeengine.CandidateModel{
					UpstreamName: "claude-opus-5-5",
				},
			}

			adapter := newFactoryProtocolAdapter(req, cand)
			payload, err := adapter.BuildUpstreamPayload(req, cand)
			if err != nil {
				t.Fatalf("BuildUpstreamPayload error: %v", err)
			}

			if _, exists := payload["tools"]; exists {
				t.Errorf("tools was not removed from payload: %#v", payload["tools"])
			}
			if _, exists := payload["tool_choice"]; exists {
				t.Errorf("tool_choice was not removed: %#v", payload["tool_choice"])
			}
		})
	}
}

func TestFactoryProtocolAdapterHeadersPassthrough(t *testing.T) {
	t.Parallel()

	downstreamHeaders := http.Header{}
	downstreamHeaders.Set("X-Factory-Client", "sdk")
	downstreamHeaders.Set("X-Factory-Sdk", "@factory/droid-sdk@0.9.1")
	downstreamHeaders.Set("X-Factory-Protocol-Version", "1.201.1")
	downstreamHeaders.Set("x-session-id", "session-custom-uuid-1234")
	downstreamHeaders.Set("x-assistant-message-id", "msg-custom-uuid-5678")
	downstreamHeaders.Set("x-message-call-type", "direct")

	req := gatewayRequest{
		DownstreamPath:    gatewayEndpointChatCompletions,
		RequestedModel:    "auto",
		DownstreamHeaders: downstreamHeaders,
		Payload: map[string]any{
			"model":    "auto",
			"messages": []any{map[string]any{"role": "user", "content": "hi"}},
		},
	}
	cand := routeengine.Candidate{
		Site: routeengine.CandidateSite{
			SiteType: "factory",
			BaseURL:  "https://api.factory.ai",
		},
		Model: routeengine.CandidateModel{
			UpstreamName: "auto",
		},
	}

	adapter := newFactoryProtocolAdapter(req, cand)
	httpReq, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://api.factory.ai", nil)
	if err != nil {
		t.Fatal(err)
	}
	adapter.ApplyUpstreamHeaders(httpReq, "test-api-key", "org-from-account", false)

	if client := httpReq.Header.Get("X-Factory-Client"); client != "sdk" {
		t.Errorf("X-Factory-Client = %q, want sdk", client)
	}
	if sdk := httpReq.Header.Get("X-Factory-Sdk"); sdk != "@factory/droid-sdk@0.9.1" {
		t.Errorf("X-Factory-Sdk = %q, want @factory/droid-sdk@0.9.1", sdk)
	}
	if proto := httpReq.Header.Get("X-Factory-Protocol-Version"); proto != "1.201.1" {
		t.Errorf("X-Factory-Protocol-Version = %q, want 1.201.1", proto)
	}
	if sid := httpReq.Header.Get("x-session-id"); sid != "session-custom-uuid-1234" {
		t.Errorf("x-session-id = %q, want session-custom-uuid-1234", sid)
	}
	if mid := httpReq.Header.Get("x-assistant-message-id"); mid != "msg-custom-uuid-5678" {
		t.Errorf("x-assistant-message-id = %q, want msg-custom-uuid-5678", mid)
	}
	if mtype := httpReq.Header.Get("x-message-call-type"); mtype != "direct" {
		t.Errorf("x-message-call-type = %q, want direct", mtype)
	}
	if org := httpReq.Header.Get("X-Factory-Org-Id"); org != "org-from-account" {
		t.Errorf("X-Factory-Org-Id = %q, want org-from-account", org)
	}
	// auto routes to claude by default
	if prov := httpReq.Header.Get("x-api-provider"); prov != "anthropic" {
		t.Errorf("x-api-provider = %q, want anthropic", prov)
	}

	// Test downstream passthrough of cli
	downstreamCliHeaders := http.Header{}
	downstreamCliHeaders.Set("X-Factory-Client", "cli")

	reqCli := gatewayRequest{
		DownstreamPath:    gatewayEndpointChatCompletions,
		RequestedModel:    "auto",
		DownstreamHeaders: downstreamCliHeaders,
		Payload: map[string]any{
			"model":    "auto",
			"messages": []any{map[string]any{"role": "user", "content": "hi"}},
		},
	}
	adapterCli := newFactoryProtocolAdapter(reqCli, cand)
	httpReqCli, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://api.factory.ai", nil)
	if err != nil {
		t.Fatal(err)
	}
	adapterCli.ApplyUpstreamHeaders(httpReqCli, "test-api-key", "", false)
	if client := httpReqCli.Header.Get("X-Factory-Client"); client != "cli" {
		t.Errorf("X-Factory-Client = %q, want cli", client)
	}
	if sdk := httpReqCli.Header.Get("X-Factory-Sdk"); sdk != "" {
		t.Errorf("X-Factory-Sdk = %q, want empty for cli client", sdk)
	}

	// Test downstream uppercase SDK client gets default sdk
	downstreamSdkUpper := http.Header{}
	downstreamSdkUpper.Set("X-Factory-Client", "SDK")

	reqSdkUpper := gatewayRequest{
		DownstreamPath:    gatewayEndpointChatCompletions,
		RequestedModel:    "auto",
		DownstreamHeaders: downstreamSdkUpper,
		Payload: map[string]any{
			"model":    "auto",
			"messages": []any{map[string]any{"role": "user", "content": "hi"}},
		},
	}
	adapterSdkUpper := newFactoryProtocolAdapter(reqSdkUpper, cand)
	httpReqSdkUpper, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://api.factory.ai", nil)
	if err != nil {
		t.Fatal(err)
	}
	adapterSdkUpper.ApplyUpstreamHeaders(httpReqSdkUpper, "test-api-key", "", false)
	if client := httpReqSdkUpper.Header.Get("X-Factory-Client"); client != "SDK" {
		t.Errorf("X-Factory-Client = %q, want SDK", client)
	}
	if sdk := httpReqSdkUpper.Header.Get("X-Factory-Sdk"); sdk != "typescript/0.9.1" {
		t.Errorf("X-Factory-Sdk = %q, want typescript/0.9.1 for uppercase SDK client", sdk)
	}
}
