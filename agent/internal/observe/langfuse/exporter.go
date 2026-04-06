package langfuse

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// exporter implements sdktrace.SpanExporter, sending spans to Langfuse's
// REST ingestion API (/api/public/ingestion).
type exporter struct {
	endpoint string
	pubKey   string
	secKey   string
	client   *http.Client
}

func newExporter(cfg Config) (*exporter, error) {
	return &exporter{
		endpoint: cfg.Endpoint + "/api/public/ingestion",
		pubKey:   cfg.PublicKey,
		secKey:   cfg.SecretKey,
		client:   &http.Client{Timeout: 10 * time.Second},
	}, nil
}

func (e *exporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	if len(spans) == 0 {
		return nil
	}

	var batch []map[string]any

	for _, s := range spans {
		traceID := s.SpanContext().TraceID().String()
		spanID := s.SpanContext().SpanID().String()
		name := s.Name()
		start := s.StartTime()
		end := s.EndTime()

		attrs := make(map[string]any)
		for _, kv := range s.Attributes() {
			attrs[string(kv.Key)] = attrValue(kv.Value)
		}

		parentSpanID := ""
		if s.Parent().IsValid() {
			parentSpanID = s.Parent().SpanID().String()
		}

		// Map to Langfuse types based on span name prefix.
		// Extract known fields from attrs for Langfuse native fields,
		// keeping the rest as metadata.
		metadata := make(map[string]any)
		for k, v := range attrs {
			metadata[k] = v
		}

		switch {
		case name == "pipeline.turn":
			// Root span → Langfuse trace
			traceBody := map[string]any{
				"id":       traceID,
				"name":     name,
				"metadata": metadata,
			}
			if userID, ok := attrs["suzuha.user_id"]; ok {
				traceBody["userId"] = userID
			}
			if input, ok := attrs["suzuha.input"]; ok {
				traceBody["input"] = input
			}
			if output, ok := attrs["suzuha.output"]; ok {
				traceBody["output"] = output
			}
			if ch, ok := attrs["suzuha.channel"]; ok {
				traceBody["sessionId"] = ch
			}
			// Tag with source (discord/device/web) for filtering.
			var tags []string
			if src, ok := attrs["suzuha.source"]; ok {
				if s, isStr := src.(string); isStr && s != "" {
					tags = append(tags, s)
				}
			}
			if len(tags) > 0 {
				traceBody["tags"] = tags
			}
			batch = append(batch, map[string]any{
				"id":        spanID,
				"type":      "trace-create",
				"timestamp": start.Format(time.RFC3339Nano),
				"body":      traceBody,
			})
		case isLLMSpan(name):
			// LLM call → Langfuse generation
			gen := map[string]any{
				"id":        spanID,
				"traceId":   traceID,
				"name":      name,
				"startTime": start.Format(time.RFC3339Nano),
				"endTime":   end.Format(time.RFC3339Nano),
				"metadata":  metadata,
			}
			if parentSpanID != "" {
				gen["parentObservationId"] = parentSpanID
			}
			if model, ok := attrs["gen_ai.request.model"]; ok {
				gen["model"] = model
			}
			// Input: full messages sent to LLM.
			if input, ok := attrs["gen_ai.input"]; ok {
				// Parse JSON string back to structured data.
				var parsed any
				if str, isStr := input.(string); isStr {
					if json.Unmarshal([]byte(str), &parsed) == nil {
						gen["input"] = parsed
					} else {
						gen["input"] = input
					}
				} else {
					gen["input"] = input
				}
				delete(metadata, "gen_ai.input")
			}
			// Output: LLM response.
			if output, ok := attrs["gen_ai.output"]; ok {
				var parsed any
				if str, isStr := output.(string); isStr {
					if json.Unmarshal([]byte(str), &parsed) == nil {
						gen["output"] = parsed
					} else {
						gen["output"] = output
					}
				} else {
					gen["output"] = output
				}
				delete(metadata, "gen_ai.output")
			}
			if pt, ok := attrs["gen_ai.usage.prompt_tokens"]; ok {
				if ct, ok2 := attrs["gen_ai.usage.completion_tokens"]; ok2 {
					gen["usage"] = map[string]any{
						"promptTokens":     pt,
						"completionTokens": ct,
					}
				}
			}
			batch = append(batch, map[string]any{
				"id":        spanID,
				"type":      "generation-create",
				"timestamp": start.Format(time.RFC3339Nano),
				"body":      gen,
			})
		default:
			// Other spans → Langfuse span
			sp := map[string]any{
				"id":        spanID,
				"traceId":   traceID,
				"name":      name,
				"startTime": start.Format(time.RFC3339Nano),
				"endTime":   end.Format(time.RFC3339Nano),
				"metadata":  metadata,
			}
			if parentSpanID != "" {
				sp["parentObservationId"] = parentSpanID
			}
			// Tool spans: map input/output.
			if input, ok := attrs["tool.input"]; ok {
				sp["input"] = input
				delete(metadata, "tool.input")
			}
			if output, ok := attrs["tool.output"]; ok {
				sp["output"] = output
				delete(metadata, "tool.output")
			}
			batch = append(batch, map[string]any{
				"id":        spanID,
				"type":      "span-create",
				"timestamp": start.Format(time.RFC3339Nano),
				"body":      sp,
			})
		}
	}

	payload, err := json.Marshal(map[string]any{"batch": batch})
	if err != nil {
		return fmt.Errorf("langfuse: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("langfuse: request: %w", err)
	}
	req.SetBasicAuth(e.pubKey, e.secKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return fmt.Errorf("langfuse: send: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		log.Printf("langfuse: HTTP %d: %s", resp.StatusCode, string(body))
		return fmt.Errorf("langfuse: HTTP %d", resp.StatusCode)
	}
	// Log payload size and any errors in response.
	log.Printf("langfuse: HTTP %d, payload=%d bytes, spans=%d, resp=%s",
		resp.StatusCode, len(payload), len(spans), string(body))
	return nil
}

func (e *exporter) Shutdown(ctx context.Context) error { return nil }

func isLLMSpan(name string) bool {
	return name == "llm.complete"
}

func attrValue(v attribute.Value) any {
	switch v.Type() {
	case attribute.BOOL:
		return v.AsBool()
	case attribute.INT64:
		return v.AsInt64()
	case attribute.FLOAT64:
		return v.AsFloat64()
	case attribute.STRING:
		return v.AsString()
	default:
		return v.Emit()
	}
}
