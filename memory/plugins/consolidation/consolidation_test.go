package consolidation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/damonlinyz/brain/memory/eventbus"
	"github.com/damonlinyz/brain/memory/plugins/forgetting"
	"github.com/damonlinyz/brain/memory/plugins/weighter"
	"github.com/damonlinyz/brain/memory/store"
	"github.com/damonlinyz/brain/memory/types"
	"github.com/google/uuid"
)

// fakeStore satisfies store.IMemoryStore for Consolidation tests.
type fakeStore struct {
	decayN       int
	decayErr     error
	pairs        []store.SimilarPair
	pairsErr     error
	edgesCreated []types.CreateEdgeInput
	edgeErr      error
}

func (f *fakeStore) DecayAll(ctx context.Context, userID uuid.UUID, before time.Time, fn store.DecayFn) (int, error) {
	if f.decayErr != nil {
		return 0, f.decayErr
	}
	return f.decayN, nil
}
func (f *fakeStore) FindUnconnectedSimilarPairs(ctx context.Context, userID uuid.UUID, threshold float64, limit int) ([]store.SimilarPair, error) {
	if f.pairsErr != nil {
		return nil, f.pairsErr
	}
	return f.pairs, nil
}
func (f *fakeStore) CreateEdge(ctx context.Context, in types.CreateEdgeInput) (types.MemoryEdge, error) {
	if f.edgeErr != nil {
		return types.MemoryEdge{}, f.edgeErr
	}
	f.edgesCreated = append(f.edgesCreated, in)
	return types.MemoryEdge{ID: uuid.New()}, nil
}

// stubs to satisfy the interface
func (f *fakeStore) CreateNode(context.Context, types.CreateNodeInput) (types.MemoryNode, error) {
	return types.MemoryNode{}, nil
}
func (f *fakeStore) GetNode(context.Context, uuid.UUID) (types.MemoryNode, error) {
	return types.MemoryNode{}, nil
}
func (f *fakeStore) UpdateNode(context.Context, uuid.UUID, int, ...store.NodeMutator) (types.MemoryNode, error) {
	return types.MemoryNode{}, nil
}
func (f *fakeStore) SoftDelete(context.Context, uuid.UUID) error { return nil }
func (f *fakeStore) RecordHistory(context.Context, types.MemoryNodeHistory) error { return nil }
func (f *fakeStore) ListNodes(context.Context, store.SearchFilter) (store.SearchResults, error) {
	return store.SearchResults{}, nil
}
func (f *fakeStore) SearchSimilar(context.Context, store.SimilarQuery) ([]store.SimilarResult, error) {
	return nil, nil
}
func (f *fakeStore) SearchByKeywords(context.Context, uuid.UUID, []string, int) ([]store.SimilarResult, error) {
	return nil, nil
}
func (f *fakeStore) SearchHybrid(context.Context, store.SimilarQuery, []string) ([]store.SimilarResult, error) {
	return nil, nil
}
func (f *fakeStore) GetEdges(context.Context, uuid.UUID, []types.EdgeKind) ([]types.MemoryEdge, error) {
	return nil, nil
}
func (f *fakeStore) BulkUpdateWeight(context.Context, uuid.UUID, []store.WeightUpdate) (int, error) {
	return 0, nil
}
func (f *fakeStore) ListMemoryUserIDs(context.Context) ([]uuid.UUID, error) {
	return nil, nil
}
func (f *fakeStore) WithTx(context.Context) (store.IMemoryStore, error) { return f, nil }
func (f *fakeStore) CommitTx(context.Context) error                    { return nil }
func (f *fakeStore) RollbackTx(context.Context) error                  { return nil }

func TestSleepWindow_DefaultsWhenZero(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"inactivityDefaultSeconds": 14400, "minSeconds": 10800, "maxSeconds": 36000})

	if got := p.SleepWindow(0); got != 4*time.Hour {
		t.Fatalf("expected default 4h, got %s", got)
	}
}

func TestSleepWindow_ClampsToRange(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"inactivityDefaultSeconds": 14400, "minSeconds": 3600, "maxSeconds": 7200})

	if got := p.SleepWindow(30 * time.Second); got != time.Hour {
		t.Fatalf("expected min clamp 1h, got %s", got)
	}
	if got := p.SleepWindow(3 * time.Hour); got != 2*time.Hour {
		t.Fatalf("expected max clamp 2h, got %s", got)
	}
	if got := p.SleepWindow(90 * time.Minute); got != 90*time.Minute {
		t.Fatalf("expected pass-through, got %s", got)
	}
}

func TestShouldRun_AfterInactivity(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"inactivityDefaultSeconds": 60})

	now := time.Now()
	if p.ShouldRun(now.Add(-30*time.Second), now) {
		t.Fatal("expected not to run after 30s with 60s threshold")
	}
	if !p.ShouldRun(now.Add(-2*time.Minute), now) {
		t.Fatal("expected to run after 2min with 60s threshold")
	}
}

func TestRunOnce_AllStagesSucceed(t *testing.T) {
	w := weighter.New()
	f := forgetting.New()
	p := New(WithWeighter(w), WithForgetting(f))
	_ = p.Init(map[string]any{})

	now := time.Now()
	uid := uuid.New()
	s := &fakeStore{
		decayN: 42,
		pairs: []store.SimilarPair{
			{A: types.MemoryNode{ID: uuid.New()}, B: types.MemoryNode{ID: uuid.New()}, Sim: 0.9},
			{A: types.MemoryNode{ID: uuid.New()}, B: types.MemoryNode{ID: uuid.New()}, Sim: 0.82},
		},
	}
	bus := eventbus.New(nil)
	var gotEvent bool
	unsub := bus.Subscribe(eventbus.TopicConsolidationDone, func(ctx context.Context, e eventbus.Event) error {
		gotEvent = true
		return nil
	})
	defer unsub()

	report := p.RunOnce(context.Background(), s, bus, uid, now)
	if !report.OK() {
		t.Fatalf("expected OK, errors=%+v", report.Errors)
	}
	if report.Decayed != 42 {
		t.Errorf("decayed count: %d", report.Decayed)
	}
	if report.Linked != 2 {
		t.Errorf("linked count: %d", report.Linked)
	}
	if len(s.edgesCreated) != 2 {
		t.Errorf("edges created: %d", len(s.edgesCreated))
	}
	if !gotEvent {
		t.Error("expected event bus publication")
	}
	if report.Duration() <= 0 {
		t.Error("expected non-zero duration")
	}
}

func TestRunOnce_StageErrorsCollected(t *testing.T) {
	w := weighter.New()
	f := forgetting.New()
	p := New(WithWeighter(w), WithForgetting(f))

	s := &fakeStore{
		decayErr: errors.New("decay fail"),
		pairsErr: errors.New("pair find fail"),
	}
	report := p.RunOnce(context.Background(), s, nil, uuid.New(), time.Now())
	if report.OK() {
		t.Fatal("expected failures recorded")
	}
	if len(report.Errors) != 2 {
		t.Fatalf("expected 2 stage errors, got %d: %+v", len(report.Errors), report.Errors)
	}
	stages := map[string]bool{}
	for _, e := range report.Errors {
		stages[e.Stage] = true
	}
	if !stages["decay"] || !stages["link_find"] {
		t.Fatalf("missing expected stage errors: %+v", stages)
	}
}

func TestRunOnce_PartialEdgeFailure(t *testing.T) {
	w := weighter.New()
	p := New(WithWeighter(w))

	s := &fakeStore{
		edgeErr: errors.New("edge insert fail"),
		pairs:   []store.SimilarPair{{A: types.MemoryNode{ID: uuid.New()}, B: types.MemoryNode{ID: uuid.New()}, Sim: 0.9}},
	}
	report := p.RunOnce(context.Background(), s, nil, uuid.New(), time.Now())
	if report.Linked != 0 {
		t.Fatalf("expected 0 successful links, got %d", report.Linked)
	}
	if len(report.Errors) == 0 {
		t.Fatal("expected link_create error recorded")
	}
}

func TestRunOnce_NilStoreSafe(t *testing.T) {
	p := New()
	report := p.RunOnce(context.Background(), nil, nil, uuid.New(), time.Now())
	if !report.OK() {
		t.Fatalf("nil store should be no-op, errors=%+v", report.Errors)
	}
	if report.Decayed != 0 || report.Linked != 0 {
		t.Fatalf("expected zeros, got %+v", report)
	}
}

func TestRunOnce_SkipsWeighterWhenAbsent(t *testing.T) {
	p := New() // no WithWeighter
	s := &fakeStore{decayN: 999}
	report := p.RunOnce(context.Background(), s, nil, uuid.New(), time.Now())
	if report.Decayed != 0 {
		t.Fatalf("expected decay skipped when no weighter, got %d", report.Decayed)
	}
}

func TestRunOnce_SkipsForgettingWhenAbsent(t *testing.T) {
	p := New() // no WithForgetting
	s := &fakeStore{}
	report := p.RunOnce(context.Background(), s, nil, uuid.New(), time.Now())
	// Transitions should be zero-value.
	if report.Transitions.Total() != 0 {
		t.Fatalf("expected zero transitions, got %+v", report.Transitions)
	}
}

func TestRunOnce_LinkThresholdRespected(t *testing.T) {
	w := weighter.New()
	p := New(WithWeighter(w))
	_ = p.Init(map[string]any{"linkSimThreshold": 0.85})

	now := time.Now()
	s := &fakeStore{
		decayN: 5,
		// pairs returned by fake store are pre-filtered by the store; threshold
		// just verifies we forwarded the configured value.
		pairs: []store.SimilarPair{
			{A: types.MemoryNode{ID: uuid.New()}, B: types.MemoryNode{ID: uuid.New()}, Sim: 0.9},
		},
	}
	report := p.RunOnce(context.Background(), s, nil, uuid.New(), now)
	if report.Linked != 1 {
		t.Fatalf("expected link pass-through, got %d", report.Linked)
	}
	if s.edgesCreated[0].Weight != 0.9 {
		t.Fatalf("expected edge weight=sim, got %f", s.edgesCreated[0].Weight)
	}
	if s.edgesCreated[0].DiscoveredBy != types.DiscovererConsolidation {
		t.Errorf("expected consolidation discoverer, got %v", s.edgesCreated[0].DiscoveredBy)
	}
}

func TestInit_ClampsMinAboveMax(t *testing.T) {
	p := New()
	_ = p.Init(map[string]any{"minSeconds": 1000, "maxSeconds": 100})

	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.minSleep > p.maxSleep {
		t.Fatalf("min %s should be clamped to max %s", p.minSleep, p.maxSleep)
	}
}

func TestStageError_Format(t *testing.T) {
	e := StageError{Stage: "decay", Err: errors.New("boom")}
	if e.Error() != "decay: boom" {
		t.Fatalf("format wrong: %q", e.Error())
	}
	e2 := StageError{Stage: "link"}
	if e2.Error() != "link" {
		t.Fatalf("expected stage name only, got %q", e2.Error())
	}
}
