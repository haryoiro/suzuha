import "@typespec/http";
import "@typespec/rest";

using TypeSpec.Http;
using TypeSpec.Rest;

namespace SuzuhaAdmin;

// Agent operations (proxy to agent)
@route("/api/agent")
@tag("Agent")
interface Agent {
  @route("compact") @post op compact(): unknown;
}

// Identity (proxy to agent)
@route("/api/identity")
@tag("Agent")
interface Identity {
  @get op get(): unknown;
}

// Context (proxy to agent)
@route("/api/context")
@tag("Agent")
interface Context {
  @get op get(): unknown;
}

// Forget / Deduplication
model ForgetGroup {
  type: string;
  members: Memory[];
  avg_distance: float64;
}

model ForgetDeleteRequest {
  delete_ids: string[];
}

model ForgetMergeRequest {
  delete_ids: string[];
  merged_content: string;
  type: string;
}

@route("/api/forget")
@tag("Forget")
interface Forget {
  @route("groups") @get op groups(@query threshold?: float64): PaginatedList<ForgetGroup>;
  @route("delete") @post op delete(@body body: ForgetDeleteRequest): { deleted: int32 };
  @route("merge") @post op merge(@body body: ForgetMergeRequest): { deleted: int32; merged: boolean };
  @route("status") @get op status(): unknown;
  @route("run") @post op run(): unknown;
}
