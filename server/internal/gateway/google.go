package gateway

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
	"xlyra/server/internal/store"
)

const googleGatewayUserAgent = "xLyra/1.0 (Gemini-API)"

func isGoogleSite(siteType string) bool {
	return strings.EqualFold(strings.TrimSpace(siteType), "google_gemini")
}

func applyGoogleGatewayHeaders(req *http.Request, apiKey string) {
	req.Header.Set("x-goog-api-key", strings.TrimSpace(apiKey))
	req.Header.Set("User-Agent", googleGatewayUserAgent)
	req.Header.Set("Accept", "application/json")
	req.Header.Del("Authorization")
}

func classifyGoogleUpstreamError(statusCode int, body []byte, now time.Time) (codexUpstreamError, bool) {
	if statusCode != http.StatusTooManyRequests && statusCode < 500 {
		return codexUpstreamError{}, false
	}
	errorMsg := googleErrorMessage(body, "Google API error")
	if statusCode == http.StatusTooManyRequests {
		duration := googleRetryAfter(body)
		lowerMsg := strings.ToLower(errorMsg)
		isDailyLimit := strings.Contains(lowerMsg, "per day") ||
			strings.Contains(lowerMsg, "per_day") ||
			strings.Contains(lowerMsg, "requests per day") ||
			googleIsDailyQuotaDetails(body)

		cooldownReason := "google_rate_limited"
		errorType := "google_rate_limited"
		if isDailyLimit {
			cooldownReason = store.CooldownReasonCredentialRPDLimitReached
			errorType = "google_daily_rate_limited"
			duration = googleDailyResetDuration(now)
		} else {
			if duration <= 0 {
				duration = 60 * time.Second
			}
		}

		retryAfterSeconds := int64(duration.Seconds())
		if retryAfterSeconds <= 0 {
			retryAfterSeconds = 1
		}

		return codexUpstreamError{
			StatusCode:        statusCode,
			ErrorType:         errorType,
			ErrorMessage:      errorMsg,
			CooldownReason:    cooldownReason,
			CooldownScope:     "credential",
			CooldownDuration:  duration,
			RetryAfterSeconds: retryAfterSeconds,
		}, true
	}
	return codexUpstreamError{
		StatusCode:   statusCode,
		ErrorType:    "google_upstream_error",
		ErrorMessage: errorMsg,
	}, true
}

func googleIsDailyQuotaDetails(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	var payload struct {
		Error struct {
			Details []struct {
				Violations []struct {
					Description string `json:"description"`
				} `json:"violations"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return false
	}
	for _, detail := range payload.Error.Details {
		for _, v := range detail.Violations {
			desc := strings.ToLower(v.Description)
			if strings.Contains(desc, "per day") || strings.Contains(desc, "per_day") || strings.Contains(desc, "daily") {
				return true
			}
		}
	}
	return false
}

func googleDailyResetDuration(now time.Time) time.Duration {
	if now.IsZero() {
		now = time.Now()
	}
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		loc = time.UTC
	}
	nowInLoc := now.In(loc)
	nextMidnight := time.Date(nowInLoc.Year(), nowInLoc.Month(), nowInLoc.Day()+1, 0, 0, 5, 0, loc)
	duration := nextMidnight.Sub(now)
	if duration <= 5*time.Minute {
		duration = 24 * time.Hour
	}
	return duration
}

func googleErrorMessage(body []byte, fallback string) string {
	if len(body) == 0 {
		return fallback
	}
	var payload struct {
		Error struct {
			Message string `json:"message"`
			Status  string `json:"status"`
			Code    int    `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return fallback
	}
	if strings.TrimSpace(payload.Error.Message) != "" {
		return payload.Error.Message
	}
	if strings.TrimSpace(payload.Error.Status) != "" {
		return payload.Error.Status
	}
	return fallback
}

func googleRetryAfter(body []byte) time.Duration {
	if len(body) == 0 {
		return 0
	}
	var payload struct {
		Error struct {
			Details []struct {
				RetryDelay string `json:"retryDelay"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return 0
	}
	for _, detail := range payload.Error.Details {
		if strings.TrimSpace(detail.RetryDelay) != "" {
			if d, err := time.ParseDuration(detail.RetryDelay); err == nil && d > 0 {
				return d
			}
		}
	}
	return 0
}
