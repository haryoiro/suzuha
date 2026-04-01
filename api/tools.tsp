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
  @route("{name}/execute") @post op execute(@path name: string, @body body: unknown): unknown;
}

// LLM (proxy to agent)
model LLMPreset {
  name: string;
  provider: string;
  @encodedName("application/json", "model") llmModel: string;
  api_key?: string;
  api_base: string;
  max_tokens: int32;
  capabilities: string[];
  source?: string;
}

@route("/api/llm")
@tag("LLM")
interface LLM {
  @get op get(): unknown;
  @put op update(@body body: unknown): unknown;

  @route("presets") @get op presetsList(): LLMPreset[];
  @route("presets") @post op presetsCreate(@body body: LLMPreset): OkResponse;
  @route("presets/{name}") @put op presetsUpdate(@path name: string, @body body: LLMPreset): OkResponse;
  @route("presets/{name}") @delete op presetsDelete(@path name: string): OkResponse;

  @route("assignments") @get op assignmentsList(): unknown;
  @route("assignments/{role}") @put op assignmentsUpdate(@path role: string, @body body: { preset: string }): OkResponse;
}
