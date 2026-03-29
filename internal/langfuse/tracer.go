package langfuse

import (
	"context"
	"fmt"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// Config holds Langfuse connection parameters.
type Config struct {
	Endpoint  string
	PublicKey string
	SecretKey string
}

// TracerProvider wraps the OTel SDK TracerProvider for Langfuse export.
type TracerProvider struct {
	tp *sdktrace.TracerProvider
}

// NewTracerProvider creates a TracerProvider that exports spans to Langfuse
// via the REST ingestion API.
func NewTracerProvider(ctx context.Context, cfg Config) (*TracerProvider, error) {
	exporter, err := newExporter(cfg)
	if err != nil {
		return nil, fmt.Errorf("langfuse: exporter: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
	)

	return &TracerProvider{tp: tp}, nil
}

// Tracer returns a named tracer from this provider.
func (p *TracerProvider) Tracer(name string) trace.Tracer {
	return p.tp.Tracer(name)
}

// Shutdown flushes pending spans and shuts down the exporter.
func (p *TracerProvider) Shutdown(ctx context.Context) error {
	return p.tp.Shutdown(ctx)
}
