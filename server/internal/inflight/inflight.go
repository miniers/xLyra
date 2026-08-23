package inflight

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

const defaultTerminalRetention = 3 * time.Second

type Phase string

const (
	PhaseAccepted   Phase = "accepted"
	PhaseRouted     Phase = "routed"
	PhaseResponding Phase = "responding"
	PhaseCompleted  Phase = "completed"
	PhaseFailed     Phase = "failed"
	PhaseCancelled  Phase = "cancelled"
)

type Request struct {
	RequestID          string    `json:"request_id"`
	APIKeyID           string    `json:"api_key_id"`
	APIKeyName         string    `json:"api_key_name"`
	ModelKey           string    `json:"model_key"`
	ModelProvider      string    `json:"model_provider"`
	SiteID             string    `json:"upstream_site_id,omitempty"`
	SiteName           string    `json:"upstream_site_name,omitempty"`
	SiteType           string    `json:"upstream_site_type,omitempty"`
	Attempt            int       `json:"attempt"`
	Stream             bool      `json:"stream"`
	Phase              Phase     `json:"phase"`
	StartedAt          time.Time `json:"started_at"`
	UpdatedAt          time.Time `json:"updated_at"`
	InputTokens        int64     `json:"input_tokens"`
	OutputTokens       int64     `json:"output_tokens"`
	FirstByteLatencyMS int64     `json:"first_byte_latency_ms,omitempty"`
	CanCancel          bool      `json:"can_cancel"`
	TokensEstimated    bool      `json:"tokens_estimated"`
}

type requestControl struct {
	cancel func() bool
}

type Route struct {
	SiteID   string
	SiteName string
	SiteType string
	Attempt  int
}

type UsageTotal struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	TotalTokens int64  `json:"total_tokens"`
}

type Event struct {
	Sequence        uint64      `json:"sequence"`
	Type            string      `json:"type"`
	Request         *Request    `json:"request,omitempty"`
	RequestID       string      `json:"request_id,omitempty"`
	Tokens          int64       `json:"tokens,omitempty"`
	TotalTokens     int64       `json:"total_tokens,omitempty"`
	DownstreamUsage *UsageTotal `json:"downstream_usage,omitempty"`
	UpstreamUsage   *UsageTotal `json:"upstream_usage,omitempty"`
}

type Snapshot struct {
	Sequence        uint64       `json:"sequence"`
	Requests        []Request    `json:"requests"`
	TotalTokens     int64        `json:"total_tokens"`
	DownstreamUsage []UsageTotal `json:"downstream_usage"`
	UpstreamUsage   []UsageTotal `json:"upstream_usage"`
}

type Registry struct {
	mu                sync.RWMutex
	requests          map[string]Request
	controls          map[string]requestControl
	subscribers       map[uint64]chan Event
	nextID            uint64
	sequence          uint64
	totalTokens       int64
	downstreamUsage   map[string]UsageTotal
	upstreamUsage     map[string]UsageTotal
	terminalRetention time.Duration
}

func NewRegistry() *Registry {
	return &Registry{
		requests:          map[string]Request{},
		controls:          map[string]requestControl{},
		subscribers:       map[uint64]chan Event{},
		downstreamUsage:   map[string]UsageTotal{},
		upstreamUsage:     map[string]UsageTotal{},
		terminalRetention: defaultTerminalRetention,
	}
}

func (r *Registry) Start(request Request) bool {
	if r == nil || request.RequestID == "" {
		return false
	}
	now := time.Now()
	request.Phase = PhaseAccepted
	if request.InputTokens > 0 && !request.TokensEstimated {
		request.TokensEstimated = true
	}
	if request.StartedAt.IsZero() {
		request.StartedAt = now
	}
	request.UpdatedAt = now

	r.mu.Lock()
	if r.requests == nil {
		r.requests = map[string]Request{}
	}
	if r.controls == nil {
		r.controls = map[string]requestControl{}
	}
	if r.subscribers == nil {
		r.subscribers = map[uint64]chan Event{}
	}
	if current, ok := r.requests[request.RequestID]; ok && !isTerminalPhase(current.Phase) {
		r.mu.Unlock()
		return false
	}
	delete(r.controls, request.RequestID)
	r.requests[request.RequestID] = request
	event := r.newEventLocked("upsert", &request, "")
	r.mu.Unlock()
	r.publish(event)
	return true
}

func (r *Registry) Route(requestID string, route Route) {
	r.update(requestID, func(request *Request) {
		request.SiteID = route.SiteID
		request.SiteName = route.SiteName
		request.SiteType = route.SiteType
		request.Attempt = route.Attempt
		request.Phase = PhaseRouted
	})
}

func (r *Registry) Model(requestID string, modelKey string, provider string) {
	r.update(requestID, func(request *Request) {
		request.ModelKey = modelKey
		request.ModelProvider = provider
	})
}

func (r *Registry) Responding(requestID string) {
	r.update(requestID, func(request *Request) {
		request.Phase = PhaseResponding
	})
}

func (r *Registry) Finish(requestID string, phase Phase) {
	if r == nil || requestID == "" {
		return
	}
	if !isTerminalPhase(phase) {
		phase = PhaseFailed
	}

	r.mu.Lock()
	request, ok := r.requests[requestID]
	if !ok || isTerminalPhase(request.Phase) {
		r.mu.Unlock()
		return
	}
	request.Phase = phase
	request.CanCancel = false
	request.UpdatedAt = time.Now()
	r.requests[requestID] = request
	delete(r.controls, requestID)
	event := r.newEventLocked("upsert", &request, "")
	r.mu.Unlock()
	r.publish(event)

	retention := r.terminalRetention
	if retention <= 0 {
		retention = defaultTerminalRetention
	}
	time.AfterFunc(retention, func() {
		r.remove(requestID, request.UpdatedAt)
	})
}

// AttachControl registers the live request controls after the request has
// been accepted. The callbacks are deliberately kept outside the public
// snapshot so the registry never serializes executable state.
func (r *Registry) AttachControl(requestID string, cancel func() bool) {
	if r == nil || requestID == "" {
		return
	}
	r.mu.Lock()
	request, ok := r.requests[requestID]
	if !ok || isTerminalPhase(request.Phase) {
		r.mu.Unlock()
		return
	}
	if r.controls == nil {
		r.controls = map[string]requestControl{}
	}
	r.controls[requestID] = requestControl{cancel: cancel}
	request.CanCancel = cancel != nil
	request.UpdatedAt = time.Now()
	r.requests[requestID] = request
	event := r.newEventLocked("upsert", &request, "")
	r.mu.Unlock()
	r.publish(event)
}

func (r *Registry) DetachControl(requestID string) {
	if r == nil || requestID == "" {
		return
	}
	r.mu.Lock()
	delete(r.controls, requestID)
	if request, ok := r.requests[requestID]; ok && !isTerminalPhase(request.Phase) {
		request.CanCancel = false
		request.UpdatedAt = time.Now()
		r.requests[requestID] = request
		event := r.newEventLocked("upsert", &request, "")
		r.mu.Unlock()
		r.publish(event)
		return
	}
	r.mu.Unlock()
}

func (r *Registry) CancelRequest(requestID string) bool {
	if r == nil || requestID == "" {
		return false
	}
	r.mu.Lock()
	request, ok := r.requests[requestID]
	control := r.controls[requestID]
	if !ok || isTerminalPhase(request.Phase) || control.cancel == nil {
		r.mu.Unlock()
		return false
	}
	request.Phase = PhaseCancelled
	request.CanCancel = false
	request.UpdatedAt = time.Now()
	r.requests[requestID] = request
	delete(r.controls, requestID)
	event := r.newEventLocked("upsert", &request, "")
	retention := r.terminalRetention
	if retention <= 0 {
		retention = defaultTerminalRetention
	}
	finishedAt := request.UpdatedAt
	r.mu.Unlock()
	r.publish(event)

	// Publish the terminal state before invoking the callback. The callback
	// stops the execution context, while the terminal registry state prevents
	// a late execution update from making the request visible again.
	control.cancel()
	time.AfterFunc(retention, func() {
		r.remove(requestID, finishedAt)
	})
	return true
}

func (r *Registry) SetFirstByteLatency(requestID string, latencyMS int64) {
	if r == nil || requestID == "" || latencyMS <= 0 {
		return
	}
	r.update(requestID, func(request *Request) {
		if request.FirstByteLatencyMS == 0 {
			request.FirstByteLatencyMS = latencyMS
		}
	})
}

func (r *Registry) SetTokenUsage(requestID string, inputTokens int64, outputTokens int64, estimated bool) {
	if r == nil || requestID == "" {
		return
	}
	if inputTokens < 0 {
		inputTokens = 0
	}
	if outputTokens < 0 {
		outputTokens = 0
	}
	r.update(requestID, func(request *Request) {
		if estimated {
			if inputTokens > 0 {
				request.InputTokens = inputTokens
			}
			if outputTokens > 0 {
				request.OutputTokens = outputTokens
			}
			return
		}
		request.InputTokens = inputTokens
		request.OutputTokens = outputTokens
		request.TokensEstimated = false
	})
}

func (r *Registry) UpdateTokenEstimate(requestID string, inputTokens int64, outputTokens int64) {
	if r == nil || requestID == "" {
		return
	}
	if inputTokens <= 0 && outputTokens <= 0 {
		return
	}
	r.update(requestID, func(request *Request) {
		if inputTokens > 0 {
			request.InputTokens = inputTokens
		}
		if outputTokens > request.OutputTokens {
			request.OutputTokens = outputTokens
		}
		request.TokensEstimated = true
	})
}

func (r *Registry) ResetOutputTokenEstimate(requestID string) {
	if r == nil || requestID == "" {
		return
	}
	r.update(requestID, func(request *Request) {
		request.OutputTokens = 0
		request.TokensEstimated = true
	})
}

func (r *Registry) SetExactOutputTokens(requestID string, outputTokens int64) {
	if r == nil || requestID == "" || outputTokens <= 0 {
		return
	}
	r.update(requestID, func(request *Request) {
		request.OutputTokens = outputTokens
	})
}

func (r *Registry) AddTokens(requestID string, tokens int64) {
	if r == nil || tokens <= 0 {
		return
	}
	r.mu.Lock()
	r.totalTokens += tokens
	request, ok := r.requests[requestID]
	var downstreamUsage *UsageTotal
	var upstreamUsage *UsageTotal
	if ok && request.APIKeyID != "" {
		if r.downstreamUsage == nil {
			r.downstreamUsage = map[string]UsageTotal{}
		}
		usage := r.downstreamUsage[request.APIKeyID]
		usage.ID = request.APIKeyID
		usage.Name = request.APIKeyName
		usage.TotalTokens += tokens
		r.downstreamUsage[request.APIKeyID] = usage
		current := usage
		downstreamUsage = &current
	}
	if ok && request.SiteID != "" {
		if r.upstreamUsage == nil {
			r.upstreamUsage = map[string]UsageTotal{}
		}
		usage := r.upstreamUsage[request.SiteID]
		usage.ID = request.SiteID
		usage.Name = request.SiteName
		usage.TotalTokens += tokens
		r.upstreamUsage[request.SiteID] = usage
		current := usage
		upstreamUsage = &current
	}
	event := r.newEventLocked("usage", nil, requestID)
	event.Tokens = tokens
	event.TotalTokens = r.totalTokens
	event.DownstreamUsage = downstreamUsage
	event.UpstreamUsage = upstreamUsage
	r.mu.Unlock()
	r.publish(event)
}

func (r *Registry) Snapshot() Snapshot {
	if r == nil {
		return Snapshot{Requests: []Request{}}
	}
	r.mu.RLock()
	requests := make([]Request, 0, len(r.requests))
	for _, request := range r.requests {
		requests = append(requests, request)
	}
	sequence := r.sequence
	totalTokens := r.totalTokens
	downstreamUsage := usageTotals(r.downstreamUsage)
	upstreamUsage := usageTotals(r.upstreamUsage)
	r.mu.RUnlock()

	sort.Slice(requests, func(i, j int) bool {
		if requests[i].StartedAt.Equal(requests[j].StartedAt) {
			return requests[i].RequestID < requests[j].RequestID
		}
		return requests[i].StartedAt.Before(requests[j].StartedAt)
	})
	return Snapshot{Sequence: sequence, Requests: requests, TotalTokens: totalTokens, DownstreamUsage: downstreamUsage, UpstreamUsage: upstreamUsage}
}

func (r *Registry) Request(requestID string) (Request, bool) {
	if r == nil || requestID == "" {
		return Request{}, false
	}
	r.mu.RLock()
	request, ok := r.requests[requestID]
	r.mu.RUnlock()
	return request, ok
}

func usageTotals(items map[string]UsageTotal) []UsageTotal {
	totals := make([]UsageTotal, 0, len(items))
	for _, item := range items {
		totals = append(totals, item)
	}
	sort.Slice(totals, func(i, j int) bool {
		if totals[i].TotalTokens != totals[j].TotalTokens {
			return totals[i].TotalTokens > totals[j].TotalTokens
		}
		return totals[i].Name < totals[j].Name
	})
	return totals
}

func (r *Registry) Subscribe() (<-chan Event, func()) {
	if r == nil {
		closed := make(chan Event)
		close(closed)
		return closed, func() {}
	}
	r.mu.Lock()
	if r.subscribers == nil {
		r.subscribers = map[uint64]chan Event{}
	}
	id := r.nextID
	r.nextID++
	events := make(chan Event, 128)
	r.subscribers[id] = events
	r.mu.Unlock()

	return events, func() {
		r.mu.Lock()
		if current, ok := r.subscribers[id]; ok {
			delete(r.subscribers, id)
			close(current)
		}
		r.mu.Unlock()
	}
}

func (r *Registry) update(requestID string, change func(*Request)) {
	if r == nil || requestID == "" {
		return
	}
	r.mu.Lock()
	request, ok := r.requests[requestID]
	if !ok || isTerminalPhase(request.Phase) {
		r.mu.Unlock()
		return
	}
	change(&request)
	request.UpdatedAt = time.Now()
	r.requests[requestID] = request
	event := r.newEventLocked("upsert", &request, "")
	r.mu.Unlock()
	r.publish(event)
}

func (r *Registry) remove(requestID string, finishedAt time.Time) {
	r.mu.Lock()
	request, ok := r.requests[requestID]
	if !ok || !request.UpdatedAt.Equal(finishedAt) || !isTerminalPhase(request.Phase) {
		r.mu.Unlock()
		return
	}
	delete(r.requests, requestID)
	event := r.newEventLocked("remove", nil, requestID)
	r.mu.Unlock()
	r.publish(event)
}

func (r *Registry) newEventLocked(eventType string, request *Request, requestID string) Event {
	r.sequence++
	if request != nil {
		requestID = request.RequestID
	}
	return Event{Sequence: r.sequence, Type: eventType, Request: request, RequestID: requestID}
}

func (r *Registry) publish(event Event) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, events := range r.subscribers {
		select {
		case events <- event:
		default:
		}
	}
}

func isTerminalPhase(phase Phase) bool {
	return phase == PhaseCompleted || phase == PhaseFailed || phase == PhaseCancelled
}

var current atomic.Int64
var defaultRegistry = NewRegistry()

func Enter() func() {
	current.Add(1)
	var once sync.Once
	return func() {
		once.Do(func() {
			current.Add(-1)
		})
	}
}

func Count() int64 {
	return current.Load()
}

func Start(request Request) bool {
	return defaultRegistry.Start(request)
}

func AttachControl(requestID string, cancel func() bool) {
	defaultRegistry.AttachControl(requestID, cancel)
}

func CancelRequest(requestID string) bool {
	return defaultRegistry.CancelRequest(requestID)
}

func SetFirstByteLatency(requestID string, latencyMS int64) {
	defaultRegistry.SetFirstByteLatency(requestID, latencyMS)
}

func SetTokenUsage(requestID string, inputTokens int64, outputTokens int64, estimated bool) {
	defaultRegistry.SetTokenUsage(requestID, inputTokens, outputTokens, estimated)
}

func UpdateTokenEstimate(requestID string, inputTokens int64, outputTokens int64) {
	defaultRegistry.UpdateTokenEstimate(requestID, inputTokens, outputTokens)
}

func SetExactOutputTokens(requestID string, outputTokens int64) {
	defaultRegistry.SetExactOutputTokens(requestID, outputTokens)
}

func RouteRequest(requestID string, route Route) {
	defaultRegistry.Route(requestID, route)
}

func SetModel(requestID string, modelKey string, provider string) {
	defaultRegistry.Model(requestID, modelKey, provider)
}

func MarkResponding(requestID string) {
	defaultRegistry.Responding(requestID)
}

func Finish(requestID string, phase Phase) {
	defaultRegistry.Finish(requestID, phase)
}

func AddTokens(requestID string, tokens int64) {
	defaultRegistry.AddTokens(requestID, tokens)
}

func CurrentSnapshot() Snapshot {
	return defaultRegistry.Snapshot()
}

func CurrentRequest(requestID string) (Request, bool) {
	return defaultRegistry.Request(requestID)
}

func IsActive(requestID string) bool {
	request, ok := defaultRegistry.Request(requestID)
	return ok && !isTerminalPhase(request.Phase)
}

func ResetOutputTokenEstimate(requestID string) {
	defaultRegistry.ResetOutputTokenEstimate(requestID)
}

func Subscribe() (<-chan Event, func()) {
	return defaultRegistry.Subscribe()
}
