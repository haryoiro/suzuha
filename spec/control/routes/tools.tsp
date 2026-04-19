import "@typespec/http";
import "@typespec/rest";

using TypeSpec.Http;
using TypeSpec.Rest;

namespace SuzuhaControl;

model ToolInfo {
  name: string;
  description: string;
  input_schema: Record<unknown>;
  enabled: boolean;
}

model ToolsListResponse {
  data: ToolInfo[];
}

model SetToolEnabledRequest {
  enabled: boolean;
}

model ToolExecuteResponse {
  ok: boolean;
  output: string;
  is_error: boolean;
}

@route("/internal/tools")
@tag("Tools")
interface Tools {
  /**
   * 登録済みツール一覧を返す (enabled/disabled 情報付き)。
   */
  @get op list(): ToolsListResponse;

  /**
   * ツールの enable/disable を切り替える。DB に永続化する。
   */
  @route("{name}/enabled") @put op setEnabled(@path name: string, @body req: SetToolEnabledRequest): OkResponse;

  /**
   * ツールを手動実行する。リクエストボディがそのまま入力 JSON。
   */
  @route("{name}/execute") @post op execute(@path name: string, @body input: Record<unknown>): ToolExecuteResponse;
}
