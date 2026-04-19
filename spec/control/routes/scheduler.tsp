import "@typespec/http";
import "@typespec/rest";

using TypeSpec.Http;
using TypeSpec.Rest;

namespace SuzuhaControl;

model SchedulerJob {
  name: string;
  task: string;
  cron: string;
  config?: Record<unknown>;
  prev: string;
  next: string;
}

model SchedulerJobsResponse {
  data: SchedulerJob[];
}

model TriggerRequest {
  config?: Record<unknown>;
}

model TriggerResponse {
  ok: boolean;
  error?: string;
}

@route("/internal")
@tag("Scheduler")
interface Scheduler {
  /**
   * 登録済み scheduler ジョブとその次回/前回実行時刻を返す。
   */
  @route("scheduler/jobs") @get op jobs(): SchedulerJobsResponse;

  /**
   * 指定タスクを即時実行する。config 未指定時は設定済みジョブの default を使う。
   */
  @route("trigger/{task}") @post op trigger(@path task: string, @body req: TriggerRequest): TriggerResponse;
}
