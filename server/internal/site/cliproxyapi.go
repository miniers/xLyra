package site

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"xlyra/server/internal/store"
)

const (
	cliProxyAPIManagementURLMetaKey = "cliproxyapi_management_url"
	cliProxyAPISyncMetaKey          = "cliproxyapi_sync"
	cliProxyAPIAuthFilesPath        = "/auth-files"
	cliProxyAPIMaxResponseBytes     = 4 << 20
	cliProxyAPIMaxSignalEntries     = 64
	cliProxyAPIMaxSignalKeyLength   = 128
	cliProxyAPIMaxSignalValueLength = 512
	cliProxyAPIRecentRequestLimit   = 20
)

var (
	ErrCLIProxyAPIManagementKeyMissing = errors.New("cli proxy api management key is not configured")
	ErrCLIProxyAPIManagementURLInvalid = errors.New("cli proxy api management url is invalid")
	ErrCLIProxyAPIInvalidResponse      = errors.New("cli proxy api returned an invalid response")
)

// CLIProxyAPIConfigPayload is safe to expose from the admin API. The
// management key itself is deliberately absent; only its configured state is
// returned.
type CLIProxyAPIConfigPayload struct {
	ManagementURL           string                   `json:"management_url,omitempty"`
	ManagementKeyConfigured bool                     `json:"management_key_configured"`
	Sync                    *CLIProxyAPISyncSnapshot `json:"sync,omitempty"`
}

type CLIProxyAPISyncSnapshot struct {
	Status        string               `json:"status"`
	FetchedAt     *time.Time           `json:"fetched_at,omitempty"`
	LastSuccessAt *time.Time           `json:"last_success_at,omitempty"`
	LastError     string               `json:"last_error,omitempty"`
	AccountCount  int                  `json:"account_count"`
	Accounts      []CLIProxyAPIAccount `json:"accounts"`
}

type CLIProxyAPIAccount struct {
	AuthIndex           string                                 `json:"auth_index"`
	Provider            string                                 `json:"provider,omitempty"`
	Name                string                                 `json:"name,omitempty"`
	Label               string                                 `json:"label,omitempty"`
	Email               string                                 `json:"email,omitempty"`
	Account             string                                 `json:"account,omitempty"`
	AccountType         string                                 `json:"account_type,omitempty"`
	PlanType            string                                 `json:"plan_type,omitempty"`
	Status              string                                 `json:"status,omitempty"`
	StatusMessage       string                                 `json:"status_message,omitempty"`
	Disabled            bool                                   `json:"disabled"`
	Unavailable         bool                                   `json:"unavailable"`
	Success             int64                                  `json:"success"`
	Failed              int64                                  `json:"failed"`
	RecentRequests      []CLIProxyAPIRecentRequestBucket       `json:"recent_requests,omitempty"`
	RecentRequestsTotal *int64                                 `json:"recent_requests_total,omitempty"`
	LastRefresh         string                                 `json:"last_refresh,omitempty"`
	NextRetryAfter      string                                 `json:"next_retry_after,omitempty"`
	Quota               *CLIProxyAPIQuotaObservation           `json:"quota,omitempty"`
	ModelQuotas         map[string]CLIProxyAPIQuotaObservation `json:"model_quotas,omitempty"`
}

type CLIProxyAPIRecentRequestBucket struct {
	Time    string `json:"time"`
	Success int64  `json:"success"`
	Failed  int64  `json:"failed"`
}

// UnmarshalJSON keeps persisted snapshots readable after the upstream changed
// recent_requests from an object to its native array of time buckets.
func (a *CLIProxyAPIAccount) UnmarshalJSON(data []byte) error {
	type accountAlias CLIProxyAPIAccount
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	var alias accountAlias
	withoutHistory := make(map[string]json.RawMessage, len(fields))
	for key, value := range fields {
		if key != "recent_requests" {
			withoutHistory[key] = value
		}
	}
	encoded, err := json.Marshal(withoutHistory)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(encoded, &alias); err != nil {
		return err
	}
	*a = CLIProxyAPIAccount(alias)
	a.RecentRequests, a.RecentRequestsTotal = normalizeCLIProxyAPIRecentRequests(fields["recent_requests"])
	return nil
}

type CLIProxyAPIQuotaObservation struct {
	ObservedAt string            `json:"observed_at,omitempty"`
	Signals    map[string]string `json:"signals,omitempty"`
}

type CLIProxyAPISyncBatchItem struct {
	SiteID uuid.UUID
	Status string
	Err    error
}

type CLIProxyAPIHTTPError struct {
	StatusCode int
}

func (e *CLIProxyAPIHTTPError) Error() string {
	if e == nil {
		return "cli proxy api management request failed"
	}
	return fmt.Sprintf("cli proxy api management returned status %d", e.StatusCode)
}

func CLIProxyAPIConfigured(item store.Site) bool {
	if !supportsCLIProxyAPIManagement(item.SiteType) {
		return false
	}
	if strings.TrimSpace(item.EncryptedCLIProxyAPIManagementKey) != "" {
		return true
	}
	meta := siteMetaMap(item)
	if strings.TrimSpace(stringFromMeta(meta, cliProxyAPIManagementURLMetaKey)) != "" {
		return true
	}
	_, ok := meta[cliProxyAPISyncMetaKey]
	return ok
}

func supportsCLIProxyAPIManagement(siteType string) bool {
	normalized := strings.ToLower(strings.TrimSpace(siteType))
	return normalized == "cliproxyapi" || normalized == "openai"
}

func CLIProxyAPIConfig(item store.Site) CLIProxyAPIConfigPayload {
	payload := CLIProxyAPIConfigPayload{
		ManagementKeyConfigured: strings.TrimSpace(item.EncryptedCLIProxyAPIManagementKey) != "",
	}
	if !supportsCLIProxyAPIManagement(item.SiteType) {
		return payload
	}

	if managementURL, err := cliProxyAPIManagementBaseURL(item); err == nil {
		payload.ManagementURL = managementURL
	}
	meta := siteMetaMap(item)
	if raw, ok := meta[cliProxyAPISyncMetaKey]; ok {
		if encoded, err := json.Marshal(raw); err == nil {
			var snapshot CLIProxyAPISyncSnapshot
			if json.Unmarshal(encoded, &snapshot) == nil {
				if !payload.ManagementKeyConfigured {
					snapshot.Status = "unconfigured"
				}
				if snapshot.Accounts == nil {
					snapshot.Accounts = []CLIProxyAPIAccount{}
				}
				if snapshot.AccountCount == 0 {
					snapshot.AccountCount = len(snapshot.Accounts)
				}
				payload.Sync = &snapshot
			}
		}
	}
	if payload.Sync == nil {
		status := "unconfigured"
		if payload.ManagementKeyConfigured {
			status = "never"
		}
		payload.Sync = &CLIProxyAPISyncSnapshot{
			Status:       status,
			Accounts:     []CLIProxyAPIAccount{},
			AccountCount: 0,
		}
	}
	return payload
}

func CLIProxyAPIManagementURL(item store.Site) (string, error) {
	base, err := cliProxyAPIManagementBaseURL(item)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(base, "/") + cliProxyAPIAuthFilesPath, nil
}

func cliProxyAPIManagementBaseURL(item store.Site) (string, error) {
	meta := siteMetaMap(item)
	explicit := strings.TrimSpace(stringFromMeta(meta, cliProxyAPIManagementURLMetaKey))
	if explicit != "" {
		return normalizeCLIProxyAPIManagementBaseURL(explicit)
	}

	parsed, err := url.Parse(strings.TrimSpace(item.BaseURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return "", ErrCLIProxyAPIManagementURLInvalid
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.RawPath = ""
	path := strings.TrimRight(parsed.Path, "/")
	if strings.HasSuffix(path, "/v1") {
		path = strings.TrimSuffix(path, "/v1")
	}
	parsed.Path = strings.TrimRight(path, "/") + "/v0/management"
	return strings.TrimRight(parsed.String(), "/"), nil
}

func normalizeCLIProxyAPIManagementBaseURL(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return "", ErrCLIProxyAPIManagementURLInvalid
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.RawPath = ""
	path := strings.TrimRight(parsed.Path, "/")
	path = strings.TrimSuffix(path, cliProxyAPIAuthFilesPath)
	parsed.Path = strings.TrimRight(path, "/")
	return strings.TrimRight(parsed.String(), "/"), nil
}

func normalizeCLIProxyAPIManagementURL(value string) (string, error) {
	base, err := normalizeCLIProxyAPIManagementBaseURL(value)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(base, "/") + cliProxyAPIAuthFilesPath, nil
}

func (s *Service) SyncCLIProxyAPI(ctx context.Context, siteID uuid.UUID) (store.Site, CLIProxyAPISyncSnapshot, error) {
	if s == nil || s.db == nil {
		return store.Site{}, CLIProxyAPISyncSnapshot{}, fmt.Errorf("site store is not initialized")
	}
	if s.siteLocks != nil {
		defer s.siteLocks.lock(siteID)()
	}

	item, err := s.Get(ctx, siteID)
	if err != nil {
		return store.Site{}, CLIProxyAPISyncSnapshot{}, err
	}
	if strings.TrimSpace(item.EncryptedCLIProxyAPIManagementKey) == "" {
		return item, CLIProxyAPISyncSnapshot{}, ErrCLIProxyAPIManagementKeyMissing
	}
	managementKey, err := s.credentials.Decrypt(item.EncryptedCLIProxyAPIManagementKey)
	if err != nil {
		return s.recordCLIProxyAPISyncError(ctx, item, fmt.Errorf("decrypt cli proxy api management key: %w", err))
	}
	endpoint, err := CLIProxyAPIManagementURL(item)
	if err != nil {
		return s.recordCLIProxyAPISyncError(ctx, item, err)
	}
	client, err := s.httpClientForSite(ctx, item, false)
	if err != nil {
		return s.recordCLIProxyAPISyncError(ctx, item, fmt.Errorf("create cli proxy api client: %w", err))
	}

	requestCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	accounts, fetchErr := fetchCLIProxyAPIAccounts(requestCtx, client, endpoint, managementKey)
	if fetchErr != nil {
		return s.recordCLIProxyAPISyncError(ctx, item, fetchErr)
	}

	now := time.Now().UTC()
	snapshot := CLIProxyAPISyncSnapshot{
		Status:        "ok",
		FetchedAt:     &now,
		LastSuccessAt: &now,
		AccountCount:  len(accounts),
		Accounts:      accounts,
	}
	updated, err := s.saveCLIProxyAPISnapshot(ctx, item, snapshot)
	if err != nil {
		return item, snapshot, err
	}
	return updated, snapshot, nil
}

func (s *Service) recordCLIProxyAPISyncError(ctx context.Context, item store.Site, requestErr error) (store.Site, CLIProxyAPISyncSnapshot, error) {
	snapshot := previousCLIProxyAPISnapshot(item)
	now := time.Now().UTC()
	snapshot.Status = "error"
	snapshot.FetchedAt = &now
	snapshot.LastError = requestErr.Error()
	snapshot.AccountCount = len(snapshot.Accounts)
	updated, saveErr := s.saveCLIProxyAPISnapshot(ctx, item, snapshot)
	if saveErr != nil {
		return item, snapshot, fmt.Errorf("save cli proxy api sync failure: %w (request: %v)", saveErr, requestErr)
	}
	return updated, snapshot, requestErr
}

func (s *Service) SyncConfiguredCLIProxyAPIs(ctx context.Context) ([]CLIProxyAPISyncBatchItem, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("site store is not initialized")
	}
	sites, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	results := make([]CLIProxyAPISyncBatchItem, 0)
	for _, item := range sites {
		if !CLIProxyAPIConfigured(item) || strings.TrimSpace(item.EncryptedCLIProxyAPIManagementKey) == "" {
			continue
		}
		updated, snapshot, syncErr := s.SyncCLIProxyAPI(ctx, item.ID)
		status := snapshot.Status
		if status == "" {
			status = "error"
		}
		if updated.ID == uuid.Nil {
			updated = item
		}
		results = append(results, CLIProxyAPISyncBatchItem{SiteID: updated.ID, Status: status, Err: syncErr})
	}
	return results, nil
}

func (s *Service) saveCLIProxyAPISnapshot(ctx context.Context, item store.Site, snapshot CLIProxyAPISyncSnapshot) (store.Site, error) {
	meta := siteMetaMap(item)
	if snapshot.Accounts == nil {
		snapshot.Accounts = []CLIProxyAPIAccount{}
	}
	if snapshot.AccountCount == 0 {
		snapshot.AccountCount = len(snapshot.Accounts)
	}
	meta[cliProxyAPISyncMetaKey] = snapshot
	return store.NewSiteRepository(s.db.DB()).UpdateMeta(ctx, item.ID, store.JSON(jsonBytes(meta)))
}

func previousCLIProxyAPISnapshot(item store.Site) CLIProxyAPISyncSnapshot {
	meta := siteMetaMap(item)
	raw, ok := meta[cliProxyAPISyncMetaKey]
	if !ok {
		return CLIProxyAPISyncSnapshot{Accounts: []CLIProxyAPIAccount{}}
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return CLIProxyAPISyncSnapshot{Accounts: []CLIProxyAPIAccount{}}
	}
	var snapshot CLIProxyAPISyncSnapshot
	if err := json.Unmarshal(encoded, &snapshot); err != nil {
		return CLIProxyAPISyncSnapshot{Accounts: []CLIProxyAPIAccount{}}
	}
	if snapshot.Accounts == nil {
		snapshot.Accounts = []CLIProxyAPIAccount{}
	}
	return snapshot
}

func fetchCLIProxyAPIAccounts(ctx context.Context, client *http.Client, endpoint string, managementKey string) ([]CLIProxyAPIAccount, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create cli proxy api management request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+managementKey)
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("call cli proxy api management: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, &CLIProxyAPIHTTPError{StatusCode: response.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, cliProxyAPIMaxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read cli proxy api management response: %w", err)
	}
	if len(body) > cliProxyAPIMaxResponseBytes {
		return nil, fmt.Errorf("%w: response is too large", ErrCLIProxyAPIInvalidResponse)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("%w: decode response: %v", ErrCLIProxyAPIInvalidResponse, err)
	}
	rawFiles, ok := envelope["files"]
	if !ok || len(rawFiles) == 0 {
		return nil, fmt.Errorf("%w: files field is missing", ErrCLIProxyAPIInvalidResponse)
	}
	var files []json.RawMessage
	if err := json.Unmarshal(rawFiles, &files); err != nil || files == nil {
		return nil, fmt.Errorf("%w: files field must be an array", ErrCLIProxyAPIInvalidResponse)
	}
	accounts := make([]CLIProxyAPIAccount, 0, len(files))
	for index, raw := range files {
		account, ok := normalizeCLIProxyAPIAccount(raw, index)
		if ok {
			accounts = append(accounts, account)
		}
	}
	return accounts, nil
}

func normalizeCLIProxyAPIAccount(raw json.RawMessage, index int) (CLIProxyAPIAccount, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return CLIProxyAPIAccount{}, false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return CLIProxyAPIAccount{}, false
	}
	provider := rawString(fields["provider"])
	if provider == "" {
		provider = rawString(fields["type"])
	}
	account := CLIProxyAPIAccount{
		AuthIndex:      rawString(fields["auth_index"]),
		Provider:       provider,
		Name:           rawString(fields["name"]),
		Label:          rawString(fields["label"]),
		Email:          rawString(fields["email"]),
		Account:        rawString(fields["account"]),
		AccountType:    rawString(fields["account_type"]),
		PlanType:       rawString(fields["plan_type"]),
		Status:         rawString(fields["status"]),
		StatusMessage:  rawString(fields["status_message"]),
		Disabled:       rawBool(fields["disabled"]),
		Unavailable:    rawBool(fields["unavailable"]),
		Success:        rawInt64(fields["success"]),
		Failed:         rawInt64(fields["failed"]),
		LastRefresh:    rawString(fields["last_refresh"]),
		NextRetryAfter: rawString(fields["next_retry_after"]),
	}
	account.RecentRequests, account.RecentRequestsTotal = normalizeCLIProxyAPIRecentRequests(fields["recent_requests"])
	if strings.TrimSpace(account.AuthIndex) == "" {
		account.AuthIndex = strings.TrimSpace(account.Name)
	}
	if strings.TrimSpace(account.AuthIndex) == "" {
		account.AuthIndex = "entry-" + strconv.Itoa(index)
	}
	if account.PlanType == "" {
		account.PlanType = nestedRawString(fields["id_token"], "plan_type", "plan")
	}
	account.Quota = normalizeCLIProxyAPIQuota(fields["quota"])
	account.ModelQuotas = normalizeCLIProxyAPIModelQuotas(fields["model_quotas"])
	return account, true
}

func nestedRawString(raw json.RawMessage, keys ...string) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var values map[string]json.RawMessage
	if json.Unmarshal(raw, &values) != nil {
		return ""
	}
	for _, key := range keys {
		if value := rawString(values[key]); value != "" {
			return value
		}
	}
	return ""
}

func normalizeCLIProxyAPIQuota(raw json.RawMessage) *CLIProxyAPIQuotaObservation {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var payload struct {
		ObservedAt json.RawMessage            `json:"observed_at"`
		Signals    map[string]json.RawMessage `json:"signals"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return nil
	}
	signalValues := make(map[string]string, len(payload.Signals))
	for key, value := range payload.Signals {
		if isSensitiveCLIProxyAPISignalKey(key) {
			continue
		}
		if normalized := rawString(value); normalized != "" {
			signalValues[key] = normalized
		}
	}
	signals := boundedCLIProxyAPISignals(signalValues)
	observedAt := rawString(payload.ObservedAt)
	if observedAt == "" && len(signals) == 0 {
		return nil
	}
	return &CLIProxyAPIQuotaObservation{ObservedAt: observedAt, Signals: signals}
}

func normalizeCLIProxyAPIModelQuotas(raw json.RawMessage) map[string]CLIProxyAPIQuotaObservation {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var values map[string]json.RawMessage
	if json.Unmarshal(raw, &values) != nil {
		return nil
	}
	result := make(map[string]CLIProxyAPIQuotaObservation, minInt(len(values), cliProxyAPIMaxSignalEntries))
	for model, value := range values {
		if len(result) >= cliProxyAPIMaxSignalEntries {
			break
		}
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		quota := normalizeCLIProxyAPIQuota(value)
		if quota == nil {
			continue
		}
		result[model] = *quota
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func boundedCLIProxyAPISignals(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	result := make(map[string]string, minInt(len(values), cliProxyAPIMaxSignalEntries))
	for key, value := range values {
		key = strings.TrimSpace(key)
		if key == "" || len(result) >= cliProxyAPIMaxSignalEntries || strings.ContainsAny(key, "\r\n\x00") {
			continue
		}
		if len(key) > cliProxyAPIMaxSignalKeyLength {
			key = key[:cliProxyAPIMaxSignalKeyLength]
		}
		if isSensitiveCLIProxyAPISignalKey(key) {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) > cliProxyAPIMaxSignalValueLength || strings.ContainsAny(value, "\r\n\x00") {
			if strings.ContainsAny(value, "\r\n\x00") {
				continue
			}
			value = value[:cliProxyAPIMaxSignalValueLength]
		}
		result[key] = value
	}
	return result
}

func isSensitiveCLIProxyAPISignalKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	return strings.Contains(normalized, "token") || strings.Contains(normalized, "secret") ||
		strings.Contains(normalized, "password") || strings.Contains(normalized, "id_token") ||
		normalized == "path" || strings.HasSuffix(normalized, "_path") || strings.HasSuffix(normalized, "-path") ||
		normalized == "authorization" || normalized == "cookie" || normalized == "set-cookie"
}

func rawString(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return strings.TrimSpace(value)
	}
	var number json.Number
	if json.Unmarshal(raw, &number) == nil {
		return strings.TrimSpace(number.String())
	}
	var boolean bool
	if json.Unmarshal(raw, &boolean) == nil {
		return strconv.FormatBool(boolean)
	}
	return ""
}

func rawBool(raw json.RawMessage) bool {
	var value bool
	_ = json.Unmarshal(raw, &value)
	return value
}

func rawInt64(raw json.RawMessage) int64 {
	if len(raw) == 0 {
		return 0
	}
	var value int64
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	parsed, _ := strconv.ParseInt(rawString(raw), 10, 64)
	return parsed
}

func normalizeCLIProxyAPIRecentRequests(raw json.RawMessage) ([]CLIProxyAPIRecentRequestBucket, *int64) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var values []json.RawMessage
	if json.Unmarshal(raw, &values) == nil && values != nil {
		buckets := make([]CLIProxyAPIRecentRequestBucket, 0, minInt(len(values), cliProxyAPIRecentRequestLimit))
		var total int64
		start := 0
		if len(values) > cliProxyAPIRecentRequestLimit {
			start = len(values) - cliProxyAPIRecentRequestLimit
		}
		for _, value := range values[start:] {
			var fields map[string]json.RawMessage
			if json.Unmarshal(value, &fields) != nil || fields == nil {
				continue
			}
			success := nonNegativeInt64(rawInt64(fields["success"]))
			failed := nonNegativeInt64(rawInt64(fields["failed"]))
			buckets = append(buckets, CLIProxyAPIRecentRequestBucket{
				Time:    rawString(fields["time"]),
				Success: success,
				Failed:  failed,
			})
			total += success + failed
		}
		return buckets, int64Pointer(total)
	}

	// Older xLyra snapshots stored a map of counters. Preserve only a neutral
	// total so the UI never invents a success/failure ratio for that format.
	var legacy map[string]json.RawMessage
	if json.Unmarshal(raw, &legacy) != nil || legacy == nil {
		return nil, nil
	}
	var total int64
	for _, value := range legacy {
		total += nonNegativeInt64(rawInt64(value))
	}
	return nil, int64Pointer(total)
}

func nonNegativeInt64(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}

func int64Pointer(value int64) *int64 {
	return &value
}

func stringFromMeta(meta map[string]any, key string) string {
	value, _ := meta[key].(string)
	return strings.TrimSpace(value)
}

func minInt(left int, right int) int {
	if left < right {
		return left
	}
	return right
}
