package gateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type requestControlTestBody struct {
	once   sync.Once
	closed chan struct{}
}

func (b *requestControlTestBody) Read([]byte) (int, error) { return 0, io.EOF }

func (b *requestControlTestBody) Close() error {
	b.once.Do(func() { close(b.closed) })
	return nil
}

func TestFirstByteTrackingWriterMarksCommittedHeaders(t *testing.T) {
	t.Parallel()

	committed := false
	writer := firstByteTrackingWriter{
		httpResponseWriter: httptest.NewRecorder(),
		onCommit: func() {
			committed = true
		},
	}
	writer.WriteHeader(http.StatusOK)
	if !committed {
		t.Fatal("WriteHeader did not mark the downstream response as committed")
	}
}

func TestMonitorGatewayFirstByteTimeoutHonorsConcurrentOutputFlag(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	var outputStarted atomic.Bool
	outputStarted.Store(true)
	monitorGatewayFirstByteTimeout(ctx, 5*time.Millisecond, cancel, outputStarted.Load)

	time.Sleep(25 * time.Millisecond)
	if cause := context.Cause(ctx); cause != nil {
		t.Fatalf("timeout cancelled after output started: %v", cause)
	}
}

func TestGatewayRequestControlCancelsContextAndClosesResponse(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancelCause(context.Background())
	control := newGatewayRequestControl(cancel)
	body := &requestControlTestBody{closed: make(chan struct{})}
	control.SetResponse(&http.Response{Body: body})

	if !control.Cancel() {
		t.Fatal("first cancellation was not accepted")
	}
	if cause := context.Cause(ctx); !errors.Is(cause, context.Canceled) {
		t.Fatalf("context cause = %v, want context.Canceled", cause)
	}
	select {
	case <-body.closed:
	case <-time.After(time.Second):
		t.Fatal("active upstream response body was not closed")
	}
	if control.Cancel() {
		t.Fatal("second cancellation should be ignored")
	}
}

func TestGatewayRequestControlClosesResponseAttachedAfterCancellation(t *testing.T) {
	t.Parallel()

	control := newGatewayRequestControl(nil)
	if !control.Cancel() {
		t.Fatal("cancellation was not accepted")
	}
	body := &requestControlTestBody{closed: make(chan struct{})}
	control.SetResponse(&http.Response{Body: body})
	select {
	case <-body.closed:
	case <-time.After(time.Second):
		t.Fatal("response attached after cancellation was not closed")
	}
}
