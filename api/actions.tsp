import "@typespec/http";
import "@typespec/rest";

using TypeSpec.Http;
using TypeSpec.Rest;

namespace SuzuhaAdmin;

model ScheduledAction {
  id: string;
  channel_id: string;
  content: string;
  mode: "direct" | "prompt";
  scheduled_at: string;
  cron_expr?: string;
  random_minutes: int32;
  created_by?: string;
  status: "pending" | "executed" | "cancelled" | "failed";
  retry_count: int32;
  executed_at?: string;
  created_at: string;
}

model CreateActionRequest {
  channel_id: string;
  content: string;
  mode?: "direct" | "prompt";
  scheduled_at?: string;
  cron_expr?: string;
}

model UpdateActionRequest {
  channel_id?: string;
  content?: string;
  mode?: "direct" | "prompt";
  scheduled_at?: string;
  cron_expr?: string;
  status?: string;
}

@route("/api/scheduled-actions")
@tag("Scheduled Actions")
interface ScheduledActions {
  @get op list(@query status?: string, @query limit?: int32): { data: ScheduledAction[] };
  @post op create(@body body: CreateActionRequest): {
    @statusCode statusCode: 201;
    @body body: { data: { id: string } };
  };
  @route("{id}") @put op update(@path id: string, @body body: UpdateActionRequest): OkResponse;
  @route("{id}") @delete op delete(@path id: string): OkResponse;
}
