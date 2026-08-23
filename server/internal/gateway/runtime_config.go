package gateway

import (
	"context"
	"math"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"xlyra/server/internal/catalog"
	"xlyra/server/internal/config"
	"xlyra/server/internal/site"
	"xlyra/server/internal/store"
)

const maxSameSiteCredentialRetriesLimit = 20

func (h Handler) gatewayFirstByteTimeout(request gatewayRequest, siteConfig *site.GatewayConfig) time.Duration {
	global := config.ReadGeneralConfig(h.confFile).Gateway.FirstByteTimeoutMS
	if global < 0 {
		global = 0
	}

	apiKeyConfig := request.APIKeyGatewayConfig
	if modelConfig, ok := apiKeyModelGatewayConfig(apiKeyConfig.Models, request.EffectiveModelKey); ok && modelConfig.FirstByteTimeoutMS != nil {
		return millisecondsDuration(*modelConfig.FirstByteTimeoutMS)
	}
	if apiKeyConfig.FirstByteTimeoutMS != nil {
		return millisecondsDuration(*apiKeyConfig.FirstByteTimeoutMS)
	}
	if siteConfig != nil && siteConfig.FirstByteTimeoutMS != nil {
		return millisecondsDuration(*siteConfig.FirstByteTimeoutMS)
	}
	return millisecondsDuration(global)
}

func apiKeyModelGatewayConfig(configs map[string]store.APIKeyModelGatewayConfig, modelKey string) (store.APIKeyModelGatewayConfig, bool) {
	modelKey = strings.TrimSpace(modelKey)
	if modelKey == "" {
		return store.APIKeyModelGatewayConfig{}, false
	}
	if value, ok := configs[modelKey]; ok {
		return value, true
	}
	normalized := catalog.NormalizeModelKey(modelKey)
	for key, value := range configs {
		if catalog.NormalizeModelKey(key) == normalized {
			return value, true
		}
	}
	return store.APIKeyModelGatewayConfig{}, false
}

func millisecondsDuration(value int) time.Duration {
	if value <= 0 {
		return 0
	}
	return time.Duration(value) * time.Millisecond
}

func (h Handler) sameSiteCredentialRetryLimit(ctx context.Context, candidateSiteID uuid.UUID) int {
	global := config.ReadGeneralConfig(h.confFile).Gateway.MaxSameSiteCredentialRetries
	if global < 0 {
		global = 0
	}
	siteConfig, _, _, err := h.siteGatewayConfig(ctx, candidateSiteID)
	if err == nil && siteConfig != nil && siteConfig.MaxSameSiteCredentialRetries != nil {
		return clampSameSiteCredentialRetries(*siteConfig.MaxSameSiteCredentialRetries)
	}
	return clampSameSiteCredentialRetries(global)
}

func (h Handler) sameSiteCredentialRetryLimitForConfig(siteConfig *site.GatewayConfig) int {
	global := config.ReadGeneralConfig(h.confFile).Gateway.MaxSameSiteCredentialRetries
	if siteConfig != nil && siteConfig.MaxSameSiteCredentialRetries != nil {
		return clampSameSiteCredentialRetries(*siteConfig.MaxSameSiteCredentialRetries)
	}
	return clampSameSiteCredentialRetries(global)
}

func clampSameSiteCredentialRetries(value int) int {
	if value < 0 {
		return 0
	}
	if value > maxSameSiteCredentialRetriesLimit {
		return maxSameSiteCredentialRetriesLimit
	}
	return value
}

func sameSiteCredentialRetryable(result gatewayAttemptResult) bool {
	if result.success || result.responseStarted {
		return false
	}
	if result.errorType == "upstream_http_error" {
		return result.upstreamStatusCode >= http.StatusInternalServerError || result.statusCode >= http.StatusInternalServerError
	}
	switch result.errorType {
	case "upstream_timeout", "upstream_transport_error", "upstream_first_byte_timeout",
		"upstream_response_read_failed", "upstream_response_transform_failed", "upstream_stream_preoutput_too_large",
		"upstream_stream_error", "upstream_stream_failed", "upstream_stream_incomplete", "upstream_stream_eof",
		"upstream_stream_read_failed", "upstream_stream_empty", "upstream_response_failed":
		return true
	default:
		return result.upstreamStatusCode >= http.StatusInternalServerError || result.statusCode >= http.StatusInternalServerError
	}
}

func estimateGatewayInputTokens(request gatewayRequest) int64 {
	if len(request.Payload) == 0 {
		return 1
	}
	payload := make(map[string]any, len(request.Payload))
	for key, value := range request.Payload {
		switch key {
		case "max_tokens", "max_completion_tokens", "max_output_tokens", "stream":
			continue
		default:
			payload[key] = value
		}
	}
	estimate := int64(math.Ceil(estimateGatewayValueTokens(payload)))
	if estimate < 1 {
		return 1
	}
	return estimate
}

// This is intentionally a provider-neutral estimate. Counting semantic text
// and JSON structure avoids the large UTF-8 byte-length bias of dividing a
// serialized request by four, especially for CJK input and tool schemas.
func estimateGatewayValueTokens(value any) float64 {
	switch item := value.(type) {
	case string:
		return estimateGatewayTextTokens(item)
	case map[string]any:
		tokens := 1.0
		for key, nested := range item {
			tokens += estimateGatewayTextTokens(key) + 1 + estimateGatewayValueTokens(nested)
		}
		return tokens
	case []any:
		tokens := 1.0
		for _, nested := range item {
			tokens += estimateGatewayValueTokens(nested) + 0.25
		}
		return tokens
	case nil:
		return 1
	case bool, float32, float64, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return 1
	default:
		return estimateGatewayTextTokens(strings.TrimSpace(anyString(value)))
	}
}

func estimateGatewayTextTokens(text string) float64 {
	if text == "" {
		return 1
	}
	asciiBytes := 0
	tokens := 0.0
	for _, r := range text {
		if r < utf8.RuneSelf {
			asciiBytes++
			continue
		}
		if unicode.Is(unicode.Han, r) || unicode.In(r, unicode.Hiragana, unicode.Katakana) {
			tokens += 1.5
		} else {
			tokens += 1.25
		}
	}
	if asciiBytes > 0 {
		tokens += float64((asciiBytes + 3) / 4)
	}
	return tokens + 0.5
}
