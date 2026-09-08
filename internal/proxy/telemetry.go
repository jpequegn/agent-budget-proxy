package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"io"
	"net"
	"net/url"
	"sync"
	"time"
)

// WaitCollector is an opt-in demo startup check, never an authorization gate.
func WaitCollector(ctx context.Context, endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("collector endpoint must be an HTTP(S) URL")
	}
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(u.Hostname(), port))
		if err == nil {
			conn.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("collector not ready: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

type localExporter struct {
	mu     sync.Mutex
	writer io.Writer
	count  int
}

func (l *localExporter) ExportSpans(_ context.Context, spans []sdk.ReadOnlySpan) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, s := range spans {
		if l.count >= 10000 {
			return nil
		}
		l.count++
		attrs := map[string]any{}
		for _, a := range s.Attributes() {
			attrs[string(a.Key)] = a.Value.AsInterface()
		}
		if err := json.NewEncoder(l.writer).Encode(map[string]any{"name": s.Name(), "trace_id": s.SpanContext().TraceID().String(), "span_id": s.SpanContext().SpanID().String(), "parent_id": s.Parent().SpanID().String(), "duration_ms": s.EndTime().Sub(s.StartTime()).Milliseconds(), "attributes": attrs}); err != nil {
			return err
		}
	}
	return nil
}
func (l *localExporter) Shutdown(context.Context) error { return nil }
func Telemetry(ctx context.Context, endpoint string, writer io.Writer) (*sdk.TracerProvider, error) {
	options := []sdk.TracerProviderOption{sdk.WithSyncer(&localExporter{writer: writer})}
	if endpoint != "" {
		exporter, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(endpoint), otlptracehttp.WithTimeout(time.Second), otlptracehttp.WithRetry(otlptracehttp.RetryConfig{Enabled: false}))
		if err != nil {
			return nil, err
		}
		options = append(options, sdk.WithBatcher(exporter, sdk.WithMaxQueueSize(256), sdk.WithBatchTimeout(100*time.Millisecond), sdk.WithExportTimeout(time.Second)))
	}
	return sdk.NewTracerProvider(options...), nil
}
func (e *Engine) tracer() trace.Tracer {
	if e.Tracer != nil {
		return e.Tracer
	}
	return trace.NewNoopTracerProvider().Tracer("agent-budget-proxy")
}
func actionAttributes(a Action) []attribute.KeyValue {
	return []attribute.KeyValue{attribute.String("agent.id", a.Agent), attribute.String("session.id", a.Session), attribute.String("run.id", a.RunID), attribute.String("action.id", a.ID), attribute.String("decision.rule", a.Decision.Rule), attribute.String("action.state", a.State)}
}
