import "@typespec/http";
import "@typespec/rest";
import "@typespec/openapi";

using TypeSpec.Http;
using TypeSpec.Rest;
using TypeSpec.OpenAPI;

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
  @get
  @extension("x-ogen-operation-group", "Tools")
  op list(): ToolsListResponse;

  /**
   * ツールの enable/disable を切り替える。DB に永続化する。
   */
  @route("{name}/enabled")
  @put
  @extension("x-ogen-operation-group", "Tools")
  op setEnabled(@path name: string, @body req: SetToolEnabledRequest): OkResponse;

  /**
   * ツールを手動実行する。リクエストボディがそのまま入力 JSON。
   */
  @route("{name}/execute")
  @post
  @extension("x-ogen-operation-group", "Tools")
  op execute(@path name: string, @body input: Record<unknown>): ToolExecuteResponse;
}
