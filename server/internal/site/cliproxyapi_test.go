package site

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"xlyra/server/internal/store"
)

func TestCLIProxyAPIManagementURL(t *testing.T) {
	item := store.Site{BaseURL: "https://proxy.example.test/v1"}
	got, err := CLIProxyAPIManagementURL(item)
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://proxy.example.test/v0/management/auth-files" {
		t.Fatalf("management URL = %q", got)
	}

	item.Meta = store.JSON(`{"cliproxyapi_management_url":"https://admin.example.test/v0/management/auth-files?secret=hidden"}`)
	got, err = CLIProxyAPIManagementURL(item)
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://admin.example.test/v0/management/auth-files" {
		t.Fatalf("normalized management URL = %q", got)
	}

	item.BaseURL = "file:///tmp/proxy"
	item.Meta = nil
	if _, err := CLIProxyAPIManagementURL(item); !errors.Is(err, ErrCLIProxyAPIManagementURLInvalid) {
		t.Fatalf("invalid URL error = %v", err)
	}
}

func TestFetchCLIProxyAPIAccountsNormalizesSafeFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer management-secret" {
			t.Fatalf("authorization header = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"files":[{"auth_index":"7","provider":"codex","label":"team","email":"a@example.test","account":{"id":"do-not-copy"},"path":"/secret.json","id_token":"secret","success":4,"failed":1,"recent_requests":[{"time":"10:00-10:10","success":2,"failed":0},{"time":"10:10-10:20","success":1,"failed":1}],"quota":{"observed_at":"2026-01-01T00:00:00Z","signals":{"Used-Percent":"12","Reset-After-Seconds":"90","token":"do-not-copy"}},"model_quotas":{"gpt-5":{"signals":{"Window-Minutes":"300"}}}}]}`))
	}))
	defer server.Close()

	accounts, err := fetchCLIProxyAPIAccounts(context.Background(), server.Client(), server.URL+"/auth-files", "management-secret")
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 || accounts[0].AuthIndex != "7" || accounts[0].Account != "" {
		t.Fatalf("accounts = %#v", accounts)
	}
	if accounts[0].Quota == nil || accounts[0].Quota.Signals["Used-Percent"] != "12" {
		t.Fatalf("quota = %#v", accounts[0].Quota)
	}
	if len(accounts[0].RecentRequests) != 2 || accounts[0].RecentRequests[1].Failed != 1 || accounts[0].RecentRequestsTotal == nil || *accounts[0].RecentRequestsTotal != 4 {
		t.Fatalf("recent requests = %#v total=%v", accounts[0].RecentRequests, accounts[0].RecentRequestsTotal)
	}
	if _, ok := accounts[0].Quota.Signals["token"]; ok {
		t.Fatal("sensitive quota signal was retained")
	}
	encoded, _ := json.Marshal(accounts[0])
	if strings.Contains(string(encoded), "secret.json") || strings.Contains(string(encoded), "id_token") || strings.Contains(string(encoded), "do-not-copy") {
		t.Fatalf("unexpected encoded account: %s", encoded)
	}
}

func TestCLIProxyAPIAccountUnmarshalSupportsLegacyRecentRequests(t *testing.T) {
	var account CLIProxyAPIAccount
	if err := json.Unmarshal([]byte(`{"auth_index":"legacy","recent_requests":{"success":3,"failed":2}}`), &account); err != nil {
		t.Fatal(err)
	}
	if len(account.RecentRequests) != 0 || account.RecentRequestsTotal == nil || *account.RecentRequestsTotal != 5 {
		t.Fatalf("legacy recent requests = %#v total=%v", account.RecentRequests, account.RecentRequestsTotal)
	}
}

func TestNormalizeCLIProxyAPIRecentRequestsLimitsBuckets(t *testing.T) {
	items := make([]map[string]any, 25)
	for index := range items {
		items[index] = map[string]any{"time": index, "success": index, "failed": 0}
	}
	raw, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	buckets, total := normalizeCLIProxyAPIRecentRequests(raw)
	if len(buckets) != cliProxyAPIRecentRequestLimit || buckets[0].Time != "5" || total == nil || *total != 290 {
		t.Fatalf("buckets = %#v total = %v", buckets, total)
	}
}

func TestFetchCLIProxyAPIAccountsRejectsMissingFiles(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"unexpected":true}`))
	}))
	defer server.Close()

	_, err := fetchCLIProxyAPIAccounts(context.Background(), server.Client(), server.URL, "key")
	if !errors.Is(err, ErrCLIProxyAPIInvalidResponse) {
		t.Fatalf("error = %v", err)
	}
}

func TestFetchCLIProxyAPIAccountsReportsHTTPStatus(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	_, err := fetchCLIProxyAPIAccounts(context.Background(), server.Client(), server.URL, "key")
	var statusErr *CLIProxyAPIHTTPError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusNotFound {
		t.Fatalf("error = %v", err)
	}
}

func TestCLIProxyAPIConfigDoesNotExposeManagementKey(t *testing.T) {
	item := store.Site{
		SiteType:                          "cliproxyapi",
		BaseURL:                           "https://proxy.example.test",
		EncryptedCLIProxyAPIManagementKey: "encrypted-value",
	}
	payload := CLIProxyAPIConfig(item)
	if !payload.ManagementKeyConfigured || payload.ManagementURL == "" {
		t.Fatalf("payload = %#v", payload)
	}
	if payload.Sync == nil || payload.Sync.Status != "never" {
		t.Fatalf("unexpected initial snapshot: %#v", payload.Sync)
	}

	item.EncryptedCLIProxyAPIManagementKey = ""
	item.Meta = store.JSON(`{"cliproxyapi_sync":{"status":"ok","account_count":1,"accounts":[{"auth_index":"1","disabled":false,"unavailable":false}]}}`)
	payload = CLIProxyAPIConfig(item)
	if payload.Sync == nil || payload.Sync.Status != "unconfigured" || payload.Sync.AccountCount != 1 {
		t.Fatalf("cleared key should mark retained snapshot unconfigured: %#v", payload.Sync)
	}
}

func TestCLIProxyAPIConfigIncludesUnconfiguredCLIProxyAPISite(t *testing.T) {
	payload := CLIProxyAPIConfig(store.Site{
		SiteType: "cliproxyapi",
		BaseURL:  "https://proxy.example.test/v1",
	})
	if payload.ManagementKeyConfigured || payload.ManagementURL != "https://proxy.example.test/v0/management" {
		t.Fatalf("unconfigured payload = %#v", payload)
	}
	if payload.Sync == nil || payload.Sync.Status != "unconfigured" || payload.Sync.AccountCount != 0 {
		t.Fatalf("unconfigured sync = %#v", payload.Sync)
	}
}
