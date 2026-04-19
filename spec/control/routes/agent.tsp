import "@typespec/http";
import "@typespec/rest";
import "@typespec/openapi";

using TypeSpec.Http;
using TypeSpec.Rest;
using TypeSpec.OpenAPI;

namespace SuzuhaControl;

model Identity {
  bot_platform_id: string;
  bot_user_id?: string;
  bot_name?: string;
}

model ContextMessage {
  role: string;
  content: string;
  channel?: string;
  channel_name?: string;
  timestamp?: string;
}

model AgentContext {
  messages: ContextMessage[];
  count: int32;
  estimated_tokens: int32;
  usage_ratio: float64;
  max_tokens: int32;
  background?: ContextMessage[];
  foreground?: ContextMessage[];
}

model GatewayStatusItem {
  name: string;
  state: string;
  started_at?: string;
  error?: string;
}

@route("/internal")
@tag("Agent")
interface AgentOps {
  /**
   * 現在の bot identity (platform ID と内部 user レコード) を返す。
   */
  @route("identity")
  @get
  @extension("x-ogen-operation-group", "Agent")
  op identity(): Identity;

  /**
   * 現在の会話コンテキスト (system + messages + 直近 background/foreground) を返す。
   */
  @route("context")
  @get
  @extension("x-ogen-operation-group", "Agent")
  op getContext(): AgentContext;

  /**
   * Gateway 管理下の全ソース (Discord/CLI/Device 等) の状態を返す。
   */
  @route("gateway/status")
  @get
  @extension("x-ogen-operation-group", "Agent")
  op gatewayStatus(): GatewayStatusItem[];
}
