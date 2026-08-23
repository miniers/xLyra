package gateway

import (
	"context"
	"testing"

	"xlyra/server/internal/config"
	"xlyra/server/internal/site"
	"xlyra/server/internal/store"
)

func TestGatewayFirstByteTimeoutUsesCurrentAPIKeyModelPriority(t *testing.T) {
	t.Parallel()

	global := 900
	apiKey := 600
	model := 300
	siteTimeout := 450
	handler := Handler{confFile: nil}
	request := gatewayRequest{
		EffectiveModelKey: "gpt-current",
		APIKeyGatewayConfig: store.APIKeyGatewayConfig{
			FirstByteTimeoutMS: &apiKey,
			Models: map[string]store.APIKeyModelGatewayConfig{
				"gpt-current": {FirstByteTimeoutMS: &model},
				"gpt-other":   {FirstByteTimeoutMS: &global},
			},
		},
	}

	if got := handler.gatewayFirstByteTimeout(request, &site.GatewayConfig{FirstByteTimeoutMS: &siteTimeout}); got.Milliseconds() != int64(model) {
		t.Fatalf("model timeout = %s, want %dms", got, model)
	}

	request.EffectiveModelKey = "gpt-not-configured"
	if got := handler.gatewayFirstByteTimeout(request, &site.GatewayConfig{FirstByteTimeoutMS: &siteTimeout}); got.Milliseconds() != int64(apiKey) {
		t.Fatalf("api key timeout = %s, want %dms", got, apiKey)
	}
}

func TestGatewayFirstByteTimeoutFallsBackToSiteThenGlobal(t *testing.T) {
	t.Parallel()

	handler := Handler{}
	siteTimeout := 450
	request := gatewayRequest{}
	if got := handler.gatewayFirstByteTimeout(request, &site.GatewayConfig{FirstByteTimeoutMS: &siteTimeout}); got.Milliseconds() != int64(siteTimeout) {
		t.Fatalf("site timeout = %s, want %dms", got, siteTimeout)
	}
	if got := handler.gatewayFirstByteTimeout(request, nil); got != 0 {
		t.Fatalf("default timeout = %s, want disabled", got)
	}
}

func TestSameSiteCredentialRetryLimitHonorsExplicitZeroAndGlobalValue(t *testing.T) {
	t.Parallel()

	globalFile, err := config.LoadConfigFile(t.TempDir())
	if err != nil {
		t.Fatalf("load config file: %v", err)
	}
	if err := globalFile.Set("global.general.gateway", map[string]any{
		"max_same_site_credential_retries": 3,
	}); err != nil {
		t.Fatalf("set gateway config: %v", err)
	}

	handler := Handler{confFile: globalFile}
	if got := handler.sameSiteCredentialRetryLimitForConfig(nil); got != 3 {
		t.Fatalf("global retry limit = %d, want 3", got)
	}
	explicitZero := 0
	if got := handler.sameSiteCredentialRetryLimitForConfig(&site.GatewayConfig{MaxSameSiteCredentialRetries: &explicitZero}); got != 0 {
		t.Fatalf("explicit zero retry limit = %d, want 0", got)
	}
}

func TestSameSiteCredentialRetryableOnlyRetriesPreOutputTransientFailures(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		result gatewayAttemptResult
		want   bool
	}{
		{name: "transport", result: gatewayAttemptResult{errorType: "upstream_transport_error"}, want: true},
		{name: "server error", result: gatewayAttemptResult{errorType: "upstream_http_error", upstreamStatusCode: 500}, want: true},
		{name: "client error", result: gatewayAttemptResult{errorType: "upstream_http_error", upstreamStatusCode: 400}, want: false},
		{name: "after output", result: gatewayAttemptResult{errorType: "upstream_transport_error", responseStarted: true}, want: false},
		{name: "success", result: gatewayAttemptResult{success: true}, want: false},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			if got := sameSiteCredentialRetryable(test.result); got != test.want {
				t.Fatalf("sameSiteCredentialRetryable() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestGatewayAttemptContextFailureMapsFinalRecordedError(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(errGatewayFirstByteTimeout)
	result := applyGatewayAttemptContextFailure(ctx, gatewayAttemptResult{success: true})
	if result.success || result.statusCode != 504 || result.errorType != "upstream_first_byte_timeout" {
		t.Fatalf("timeout result = %#v", result)
	}
}

func TestGatewayAttemptContextFailureMapsCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(context.Canceled)
	result := applyGatewayAttemptContextFailure(ctx, gatewayAttemptResult{})
	if result.success || result.statusCode != 499 || result.errorType != "downstream_client_cancelled" {
		t.Fatalf("cancelled result = %#v", result)
	}
}

func TestEstimateGatewayInputTokensCountsSemanticUnicodeAndStructure(t *testing.T) {
	t.Parallel()

	controlOnly := gatewayRequest{Payload: map[string]any{
		"max_tokens":            4096,
		"max_completion_tokens": 4096,
		"stream":                true,
	}}
	if got := estimateGatewayInputTokens(controlOnly); got != 1 {
		t.Fatalf("control-only input estimate = %d, want 1", got)
	}

	request := gatewayRequest{Payload: map[string]any{
		"model": "gpt-test",
		"messages": []any{
			map[string]any{"role": "user", "content": "你好，帮我检查这个请求"},
		},
		"tools": []any{map[string]any{
			"type":     "function",
			"function": map[string]any{"name": "lookup", "parameters": map[string]any{"type": "object"}},
		}},
	}}
	if got := estimateGatewayInputTokens(request); got < 15 {
		t.Fatalf("semantic input estimate = %d, want a non-trivial structured estimate", got)
	}
}
