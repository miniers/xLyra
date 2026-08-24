package router

import (
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"

	"xlyra/server/internal/store"
)

func TestBuildCandidateExplorationInitialCycle(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	config := store.RoutingExplorationConfig{Enabled: true, NewTrialsPerSite: 3, IdleAfterHours: 24, IdleTrialsPerSite: 2}
	item := buildCandidateExploration(config, store.RouteExplorationState{}, 0, nil, now)
	if item.Status != "pending" || item.Mode != store.RouteExplorationCycleInitial || item.Target != 3 || item.Remaining != 3 {
		t.Fatalf("initial exploration = %#v", item)
	}

	state := store.RouteExplorationState{
		ID:               uuid.New(),
		CanonicalModelID: uuid.New(),
		SiteModelID:      uuid.New(),
		CycleKind:        store.RouteExplorationCycleInitial,
		CycleKey:         "initial",
		Attempts:         2,
		Target:           3,
	}
	item = buildCandidateExploration(config, state, 2, nil, now)
	if item.Status != "in_progress" || item.Attempts != 2 || item.Remaining != 1 {
		t.Fatalf("in-progress exploration = %#v", item)
	}
}

func TestBuildCandidateExplorationIdleCycleContinuesAfterTrialUpdatesLastUsed(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	old := now.Add(-48 * time.Hour)
	config := store.RoutingExplorationConfig{Enabled: true, NewTrialsPerSite: 3, IdleAfterHours: 24, IdleTrialsPerSite: 2}
	state := store.RouteExplorationState{
		CycleKind: store.RouteExplorationCycleIdle,
		CycleKey:  "idle:" + old.Format(time.RFC3339Nano),
		Attempts:  1,
		Target:    2,
	}
	// The latest health snapshot is now recent, but the unfinished idle cycle
	// must still consume its second configured trial.
	item := buildCandidateExploration(config, state, 20, &now, now)
	if item.Mode != store.RouteExplorationCycleIdle || item.Status != "in_progress" || item.Remaining != 1 {
		t.Fatalf("idle exploration = %#v", item)
	}
}

func TestBuildCandidateExplorationResetCycle(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	resetAt := now.Add(-time.Minute)
	config := store.RoutingExplorationConfig{Enabled: true, NewTrialsPerSite: 4, IdleAfterHours: 24, IdleTrialsPerSite: 2, ResetAt: &resetAt}
	item := buildCandidateExploration(config, store.RouteExplorationState{Attempts: 4, Target: 4, CycleKind: store.RouteExplorationCycleInitial, CycleKey: "initial"}, 20, nil, now)
	if item.Mode != store.RouteExplorationCycleReset || item.Status != "pending" || item.Remaining != 4 {
		t.Fatalf("reset exploration = %#v", item)
	}
}

func TestBuildCandidateExplorationManualResetCycleRestartsRecentSite(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	config := store.RoutingExplorationConfig{Enabled: true, NewTrialsPerSite: 4, IdleAfterHours: 24, IdleTrialsPerSite: 2}
	state := store.RouteExplorationState{
		CycleKind: store.RouteExplorationCycleInitial,
		CycleKey:  "manual_reset:2026-08-23T11:00:00Z",
		Attempts:  0,
		Target:    1,
	}
	lastUsedAt := now.Add(-time.Hour)

	item := buildCandidateExploration(config, state, 20, &lastUsedAt, now)
	if item.Status != "pending" || item.Mode != store.RouteExplorationCycleInitial || item.Target != 4 || item.Remaining != 4 {
		t.Fatalf("manual reset exploration = %#v", item)
	}
}

func TestExplorationCandidateOrderingKeepsActiveSiteUntilCycleCompletes(t *testing.T) {
	old := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	recent := old.Add(time.Minute)
	items := []Candidate{
		{Score: 100, Exploration: CandidateExploration{Status: "pending", Target: 5, Remaining: 5}},
		{Score: 1, Exploration: CandidateExploration{Status: "in_progress", Attempts: 1, Target: 5, Remaining: 4, LastAttemptAt: &old}},
		{Score: 90, Exploration: CandidateExploration{Status: "pending", Target: 5, Remaining: 5}},
	}
	order := []int{0, 1, 2}
	sort.SliceStable(order, func(i, j int) bool {
		return explorationCandidateComesBefore(items[order[i]], items[order[j]])
	})
	if got, want := order, []int{1, 0, 2}; !equalInts(got, want) {
		t.Fatalf("exploration order = %v, want %v", got, want)
	}

	moreAdvanced := items[1]
	moreAdvanced.Exploration.Attempts = 2
	moreAdvanced.Exploration.Remaining = 3
	moreAdvanced.Exploration.LastAttemptAt = &recent
	if !explorationCandidateComesBefore(moreAdvanced, items[1]) {
		t.Fatal("more advanced active cycle should be preferred")
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for index := range a {
		if a[index] != b[index] {
			return false
		}
	}
	return true
}
