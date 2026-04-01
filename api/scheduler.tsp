import "@typespec/http";
import "@typespec/rest";

using TypeSpec.Http;
using TypeSpec.Rest;

namespace SuzuhaAdmin;

// Scheduler (proxy to agent)
@route("/api/scheduler")
@tag("Scheduler")
interface Scheduler {
  @route("jobs") @get op jobs(): unknown;
  @route("trigger/{task}") @post op trigger(@path task: string, @body body?: unknown): unknown;
}
