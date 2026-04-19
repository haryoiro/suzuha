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

// LLM Provider/Model/Role (3-layer architecture)
model LLMProvider {
  name: string;
  type: string;
  api_key?: string;
  api_base: string;
  source?: string;
}

model LLMModel {
  provider_name: string;
  model_id: string;
  capabilities: string[];
  max_context: int32;
  source?: string;
}

model LLMRoleAssignment {
  role: string;
  provider_name: string;
  model_id: string;
}

model LLMRoleUpdateReq {
  provider: string;
  @encodedName("application/json", "model") llmModel: string;
}

@route("/api/llm")
@tag("LLM")
interface LLM {
  @get op get(): unknown;

  @route("providers") @get op providersList(): LLMProvider[];

  @route("models") @get op modelsList(@query provider?: string): LLMModel[];
  @route("models") @post op modelsCreate(@body body: LLMModel): OkResponse;
  @route("models/refresh") @post op modelsRefresh(): unknown;

  @route("roles") @get op rolesList(): LLMRoleAssignment[];
  @route("roles/{role}") @put op rolesUpdate(@path role: string, @body body: LLMRoleUpdateReq): OkResponse;
}
