import "@typespec/http";
import "@typespec/rest";

using TypeSpec.Http;
using TypeSpec.Rest;

namespace SuzuhaAdmin;

// Metrics
model MetricEntry {
  name: string;
  help: string;
  type: "counter" | "histogram" | "gauge";
  value?: float64;
  labels?: Record<string>;
  buckets?: MetricBucket[];
  sum?: float64;
  count?: float64;
}

model MetricBucket {
  le: float64;
  count: int64;
}

@route("/api/metrics")
@tag("Metrics")
interface Metrics {
  @route("json") @get op json(): { metrics: MetricEntry[] };
}

// Tools (proxy to agent)
@route("/api/tools")
@tag("Tools")
interface Tools {
  @get op list(): unknown;
  @route("{name}/enabled") @put op setEnabled(@path name: string, @body body: unknown): unknown;
}

// LLM (proxy to agent)
@route("/api/llm")
@tag("LLM")
interface LLM {
  @get op get(): unknown;
  @put op update(@body body: unknown): unknown;
}
