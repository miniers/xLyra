package site

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"xlyra/server/internal/adapter"
	"xlyra/server/internal/store"
)

func TestProbeSub2APIBillingUsesResolvedRateAndKeepsEffectiveRateAsHint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/sub2api/billing" {
			t.Errorf("billing probe path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Errorf("billing probe authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"resolved_rate_multiplier":1.25,"effective_rate_multiplier":1.75}`))
	}))
	defer server.Close()

	probe := probeSub2APIBilling(context.Background(), server.Client(), server.URL, "sk-test")
	if probe.Status != "ok" || probe.ResolvedRateMultiplier == nil || *probe.ResolvedRateMultiplier != 1.25 {
		t.Fatalf("billing probe = %+v, want resolved multiplier 1.25", probe)
	}
	if probe.EffectiveRateMultiplier == nil || *probe.EffectiveRateMultiplier != 1.75 {
		t.Fatalf("billing probe effective multiplier = %#v, want 1.75", probe.EffectiveRateMultiplier)
	}
}

func TestNewAPIKeyCostMultiplierProbeRequiresExplicitGroupRatio(t *testing.T) {
	t.Parallel()

	snapshot := adapter.PricingSnapshot{Raw: map[string]any{
		"group_ratio": map[string]any{"vip": 0.75},
	}}
	okProbe := newAPIKeyCostMultiplierProbe(snapshot, "vip")
	if okProbe.Status != "ok" || okProbe.ResolvedRateMultiplier == nil || *okProbe.ResolvedRateMultiplier != 0.75 {
		t.Fatalf("group multiplier probe = %+v, want vip ratio", okProbe)
	}

	for name, group := range map[string]string{
		"missing group":    "",
		"missing ratio":    "default",
		"synthesized only": "vip",
	} {
		group := group
		t.Run(name, func(t *testing.T) {
			probe := newAPIKeyCostMultiplierProbe(adapter.PricingSnapshot{
				Groups: []adapter.PricingGroup{{GroupName: "vip", Ratio: 0.75}},
				Raw:    map[string]any{"group_ratio": map[string]any{"vip": 0.75}},
			}, group)
			if name == "synthesized only" {
				probe = newAPIKeyCostMultiplierProbe(adapter.PricingSnapshot{
					Groups: []adapter.PricingGroup{{GroupName: "vip", Ratio: 0.75}},
				}, group)
			}
			if probe.Status != "incomplete" || probe.ResolvedRateMultiplier != nil {
				t.Fatalf("incomplete group multiplier probe = %+v", probe)
			}
		})
	}
}

func TestAPIKeyCostMultiplierHistoryRecordsOnlyValueChanges(t *testing.T) {
	t.Parallel()

	firstAt := time.Date(2026, 9, 2, 8, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	secondAt := firstAt.Add(time.Hour)
	meta := map[string]any{}
	appendAPIKeyCostMultiplierChange(meta, 1, 1, "manual", firstAt)
	appendAPIKeyCostMultiplierChange(meta, 1, 1.25, "sub2api", firstAt)
	appendAPIKeyCostMultiplierChange(meta, 1.25, 1.5, "newapi", secondAt)

	history := APIKeyCostMultiplierHistory(meta)
	if len(history) != 2 {
		t.Fatalf("multiplier history length = %d, want 2", len(history))
	}
	if history[0].From != 1 || history[0].To != 1.25 || history[0].Source != "sub2api" || !history[0].ChangedAt.Equal(firstAt.UTC()) {
		t.Fatalf("first multiplier history item = %+v", history[0])
	}
	if history[1].From != 1.25 || history[1].To != 1.5 || history[1].Source != "newapi" {
		t.Fatalf("second multiplier history item = %+v", history[1])
	}
}

func TestUpdateAPIKeyMultiplierProbeProtectsManualValueAndUsesCAS(t *testing.T) {
	for _, test := range []struct {
		name       string
		source     string
		wantValue  bool
		wantManual bool
	}{
		{name: "manual", source: upstreamCostMultiplierSourceManual, wantManual: true},
		{name: "automatic", source: upstreamCostMultiplierSourceAuto, wantValue: true},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			credentialID := uuid.New()
			expectedUpdatedAt := time.Date(2026, 9, 2, 8, 0, 0, 0, time.UTC)
			credential := store.SiteCredential{
				ID:                     credentialID,
				SiteID:                 uuid.New(),
				UpdatedAt:              expectedUpdatedAt,
				UpstreamCostMultiplier: 2,
				Meta:                   siteJSONMeta(t, map[string]any{upstreamCostMultiplierSourceMetaKey: test.source}),
			}
			var updates []map[string]any
			service := siteServiceWithCallbacks(t, siteGormCallbacks{
				query: func(tx *gorm.DB) {
					destination, ok := tx.Statement.Dest.(*store.SiteCredential)
					if !ok {
						tx.AddError(gorm.ErrInvalidData)
						return
					}
					*destination = credential
					tx.RowsAffected = 1
					tx.Statement.RowsAffected = 1
				},
				update: func(tx *gorm.DB) {
					if destination, ok := tx.Statement.Dest.(map[string]any); ok {
						updates = append(updates, destination)
					}
					tx.RowsAffected = 1
					tx.Statement.RowsAffected = 1
				},
			})

			meta := map[string]any{
				upstreamCostMultiplierSourceMetaKey: test.source,
				upstreamCostMultiplierProbeMetaKey: map[string]any{
					"status":                   "ok",
					"resolved_rate_multiplier": 2.5,
				},
			}
			value := 3.5
			_, err := service.updateAPIKeyMultiplierProbe(t.Context(), credential, meta, APIKeyCostMultiplierProbe{
				Status:                 "ok",
				Source:                 QuotaProbeTypeSub2API,
				ResolvedRateMultiplier: &value,
				ProbedAt:               expectedUpdatedAt.Add(time.Minute),
			})
			if err != nil {
				t.Fatalf("update multiplier probe: %v", err)
			}
			if len(updates) != 1 {
				t.Fatalf("probe updates = %d, want 1", len(updates))
			}
			_, hasValueUpdate := updates[0]["upstream_cost_multiplier"]
			if hasValueUpdate != test.wantValue {
				t.Fatalf("upstream value update = %v, want %v: %#v", hasValueUpdate, test.wantValue, updates[0])
			}
			if test.wantManual {
				if _, ok := updates[0]["upstream_cost_multiplier"]; ok {
					t.Fatal("manual source must protect the current multiplier")
				}
			}
			storedMeta, ok := updates[0]["meta"].(store.JSON)
			if !ok {
				t.Fatalf("updated meta type = %T", updates[0]["meta"])
			}
			decodedMeta := map[string]any{}
			if err := json.Unmarshal(storedMeta, &decodedMeta); err != nil {
				t.Fatalf("decode updated meta: %v", err)
			}
			if history := APIKeyCostMultiplierHistory(decodedMeta); len(history) != 1 || history[0].From != 2.5 || history[0].To != 3.5 {
				t.Fatalf("updated multiplier history = %+v", history)
			}
		})
	}
}

func TestUpdateSiteCredentialProbeStateRejectsStaleUpdatedAt(t *testing.T) {
	db := siteGormWithCallbacks(t, siteGormCallbacks{
		update: func(tx *gorm.DB) {
			tx.RowsAffected = 0
			tx.Statement.RowsAffected = 0
		},
	})
	repo := store.NewSiteCredentialRepository(db)
	_, err := repo.UpdateProbeState(context.Background(), uuid.New(), store.UpdateSiteCredentialProbeParams{
		ExpectedUpdatedAt: time.Now().UTC(),
		Meta:              store.JSON(`{}`),
	})
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("stale probe update error = %v, want record-not-found", err)
	}
}

func TestAPIKeyCostMultiplierProbePayloadDoesNotExposeUnknownFields(t *testing.T) {
	t.Parallel()

	meta := map[string]any{upstreamCostMultiplierProbeMetaKey: map[string]any{
		"status": "error",
		"source": "sub2api",
		"error":  "safe error",
		"secret": "do-not-return",
	}}
	payload, ok := APIKeyCostMultiplierProbePayload(meta)
	if !ok {
		t.Fatal("expected probe payload")
	}
	if _, exists := payload["secret"]; exists {
		t.Fatalf("probe payload exposed unknown field: %#v", payload)
	}
}
