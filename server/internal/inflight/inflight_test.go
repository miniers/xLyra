package inflight

import (
	"testing"
	"time"
)

func TestRegistryPublishesLifecycleAndRemovesTerminalRequest(t *testing.T) {
	registry := NewRegistry()
	registry.terminalRetention = 5 * time.Millisecond
	events, unsubscribe := registry.Subscribe()
	defer unsubscribe()

	registry.Start(Request{RequestID: "req-1", APIKeyName: "Client", ModelKey: "gpt-test", ModelProvider: "openai", Stream: true})
	assertEvent(t, events, "upsert", "req-1", PhaseAccepted)

	registry.Route("req-1", Route{SiteID: "site-1", SiteName: "Primary", SiteType: "openai", Attempt: 1})
	event := assertEvent(t, events, "upsert", "req-1", PhaseRouted)
	if event.Request == nil || event.Request.SiteName != "Primary" || event.Request.Attempt != 1 {
		t.Fatalf("route event = %#v", event)
	}

	registry.Responding("req-1")
	assertEvent(t, events, "upsert", "req-1", PhaseResponding)
	registry.Finish("req-1", PhaseCompleted)
	assertEvent(t, events, "upsert", "req-1", PhaseCompleted)

	time.Sleep(registry.terminalRetention + 20*time.Millisecond)
	assertEvent(t, events, "remove", "req-1", "")
	if got := registry.Snapshot().Requests; len(got) != 0 {
		t.Fatalf("snapshot after terminal retention = %#v", got)
	}
}

func TestRegistryPreservesProvidedRequestStartTime(t *testing.T) {
	registry := NewRegistry()
	startedAt := time.Date(2026, 8, 17, 8, 0, 0, 123456789, time.UTC)

	registry.Start(Request{RequestID: "req-start", StartedAt: startedAt})

	requests := registry.Snapshot().Requests
	if len(requests) != 1 || !requests[0].StartedAt.Equal(startedAt) {
		t.Fatalf("request start time = %#v, want %s", requests, startedAt)
	}
}

func TestRegistryDoesNotOverwriteActiveRequestID(t *testing.T) {
	registry := NewRegistry()
	registry.Start(Request{RequestID: "req-duplicate", APIKeyName: "first"})
	registry.Start(Request{RequestID: "req-duplicate", APIKeyName: "second"})

	request, ok := registry.Request("req-duplicate")
	if !ok || request.APIKeyName != "first" {
		t.Fatalf("active request after duplicate start = %#v, ok=%v", request, ok)
	}
}

func TestRegistryFinishIsIdempotentAndSnapshotIsSorted(t *testing.T) {
	registry := NewRegistry()
	registry.Start(Request{RequestID: "earlier"})
	time.Sleep(time.Millisecond)
	registry.Start(Request{RequestID: "later"})
	registry.Finish("earlier", PhaseFailed)
	registry.Finish("earlier", PhaseCompleted)

	requests := registry.Snapshot().Requests
	if len(requests) != 2 || requests[0].RequestID != "earlier" || requests[1].RequestID != "later" {
		t.Fatalf("sorted snapshot = %#v", requests)
	}
	if requests[0].Phase != PhaseFailed {
		t.Fatalf("terminal phase changed after duplicate finish = %q", requests[0].Phase)
	}
}

func TestRegistryAccumulatesAndPublishesTokens(t *testing.T) {
	registry := NewRegistry()
	events, unsubscribe := registry.Subscribe()
	defer unsubscribe()
	registry.Start(Request{RequestID: "req-usage", APIKeyID: "key-1", APIKeyName: "Client key"})
	assertEvent(t, events, "upsert", "req-usage", PhaseAccepted)
	registry.Route("req-usage", Route{SiteID: "site-1", SiteName: "Primary site", Attempt: 1})
	assertEvent(t, events, "upsert", "req-usage", PhaseRouted)

	registry.AddTokens("req-usage", 1200)
	event := assertEvent(t, events, "usage", "req-usage", "")
	if event.Tokens != 1200 || event.TotalTokens != 1200 {
		t.Fatalf("usage event tokens = %d, total_tokens = %d", event.Tokens, event.TotalTokens)
	}
	if event.DownstreamUsage == nil || event.DownstreamUsage.ID != "key-1" || event.DownstreamUsage.TotalTokens != 1200 {
		t.Fatalf("downstream usage event = %#v", event.DownstreamUsage)
	}
	if event.UpstreamUsage == nil || event.UpstreamUsage.ID != "site-1" || event.UpstreamUsage.TotalTokens != 1200 {
		t.Fatalf("upstream usage event = %#v", event.UpstreamUsage)
	}

	registry.AddTokens("req-usage", 300)
	event = assertEvent(t, events, "usage", "req-usage", "")
	if event.Tokens != 300 || event.TotalTokens != 1500 {
		t.Fatalf("usage event tokens = %d, total_tokens = %d", event.Tokens, event.TotalTokens)
	}
	if snapshot := registry.Snapshot(); snapshot.TotalTokens != 1500 || len(snapshot.DownstreamUsage) != 1 || snapshot.DownstreamUsage[0].TotalTokens != 1500 || len(snapshot.UpstreamUsage) != 1 || snapshot.UpstreamUsage[0].TotalTokens != 1500 {
		t.Fatalf("snapshot usage = %#v", snapshot)
	}
}

func TestRegistryTracksLiveCancelControl(t *testing.T) {
	registry := NewRegistry()
	registry.Start(Request{RequestID: "req-control"})

	cancelled := false
	registry.AttachControl("req-control", func() bool {
		cancelled = true
		return true
	})

	if !registry.CancelRequest("req-control") || !cancelled {
		t.Fatal("cancel control was not invoked")
	}

	registry.Finish("req-control", PhaseCompleted)
	if registry.CancelRequest("req-control") {
		t.Fatal("terminal request should not expose cancel control")
	}
}

func TestRegistryCancelMarksRequestTerminalBeforeExecutionStops(t *testing.T) {
	registry := NewRegistry()
	registry.terminalRetention = time.Hour
	events, unsubscribe := registry.Subscribe()
	defer unsubscribe()

	registry.Start(Request{RequestID: "req-cancel-terminal"})
	assertEvent(t, events, "upsert", "req-cancel-terminal", PhaseAccepted)
	stopped := false
	registry.AttachControl("req-cancel-terminal", func() bool {
		stopped = true
		return true
	})
	assertEvent(t, events, "upsert", "req-cancel-terminal", PhaseAccepted)

	if !registry.CancelRequest("req-cancel-terminal") {
		t.Fatal("cancel request was not accepted")
	}
	if !stopped {
		t.Fatal("cancel callback was not invoked")
	}
	cancelled := assertEvent(t, events, "upsert", "req-cancel-terminal", PhaseCancelled)
	if cancelled.Request == nil || cancelled.Request.CanCancel {
		t.Fatalf("cancelled event = %#v, want no live control", cancelled)
	}
	if request, ok := registry.Request("req-cancel-terminal"); !ok || request.Phase != PhaseCancelled {
		t.Fatalf("request after cancellation = %#v, ok=%v", request, ok)
	}
	registry.Finish("req-cancel-terminal", PhaseCompleted)
	if request, ok := registry.Request("req-cancel-terminal"); !ok || request.Phase != PhaseCancelled {
		t.Fatalf("late finish changed cancelled request = %#v, ok=%v", request, ok)
	}
	if registry.CancelRequest("req-cancel-terminal") {
		t.Fatal("terminal request should not accept a second cancellation")
	}
}

func TestRegistryReplacesEstimatedTokenUsageWithActualUsage(t *testing.T) {
	registry := NewRegistry()
	registry.Start(Request{RequestID: "req-token", InputTokens: 100, TokensEstimated: true})
	registry.SetTokenUsage("req-token", 12, 7, false)

	request, ok := registry.Request("req-token")
	if !ok || request.InputTokens != 12 || request.OutputTokens != 7 || request.TokensEstimated {
		t.Fatalf("actual token usage = %#v", request)
	}
}

func TestRegistrySetsExactOutputBelowLiveEstimate(t *testing.T) {
	registry := NewRegistry()
	registry.Start(Request{RequestID: "req-output-exact", InputTokens: 100, OutputTokens: 50, TokensEstimated: true})
	registry.SetExactOutputTokens("req-output-exact", 7)

	request, ok := registry.Request("req-output-exact")
	if !ok || request.InputTokens != 100 || request.OutputTokens != 7 || !request.TokensEstimated {
		t.Fatalf("exact output usage = %#v, want input=100 output=7 and estimated input", request)
	}
}

func TestRegistryResetsOutputEstimateForRetry(t *testing.T) {
	registry := NewRegistry()
	registry.Start(Request{RequestID: "req-output-retry", InputTokens: 100, OutputTokens: 50, TokensEstimated: true})

	registry.ResetOutputTokenEstimate("req-output-retry")
	request, ok := registry.Request("req-output-retry")
	if !ok || request.InputTokens != 100 || request.OutputTokens != 0 || !request.TokensEstimated {
		t.Fatalf("reset output estimate = %#v, want input=100 output=0 estimated=true", request)
	}
	registry.UpdateTokenEstimate("req-output-retry", 0, 7)
	request, ok = registry.Request("req-output-retry")
	if !ok || request.OutputTokens != 7 {
		t.Fatalf("retry output estimate = %#v, want 7", request)
	}
}

func TestRegistryTracksFirstByteLatencyOnce(t *testing.T) {
	registry := NewRegistry()
	registry.Start(Request{RequestID: "req-first-byte"})

	registry.SetFirstByteLatency("req-first-byte", 42)
	registry.SetFirstByteLatency("req-first-byte", 84)

	request, ok := registry.Request("req-first-byte")
	if !ok || request.FirstByteLatencyMS != 42 {
		t.Fatalf("first byte latency = %#v, want 42ms", request)
	}
}

func assertEvent(t *testing.T, events <-chan Event, eventType string, requestID string, phase Phase) Event {
	t.Helper()
	select {
	case event := <-events:
		if event.Type != eventType || event.RequestID != requestID {
			t.Fatalf("event = %#v, want type=%q request_id=%q", event, eventType, requestID)
		}
		if phase != "" && (event.Request == nil || event.Request.Phase != phase) {
			t.Fatalf("event phase = %#v, want %q", event.Request, phase)
		}
		return event
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s event", eventType)
		return Event{}
	}
}
