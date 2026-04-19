import "@typespec/http";
import "@typespec/rest";
import "@typespec/openapi";

using TypeSpec.Http;
using TypeSpec.Rest;
using TypeSpec.OpenAPI;

namespace SuzuhaControl;

model OkResponse {
  ok: boolean;
}

model CompactResponse {
  ok: boolean;
  message_count: int32;
}

model ReloadPromptResponse {
  ok: boolean;
  length: int32;
}

@route("/internal")
@tag("Runtime")
interface Runtime {
  /**
   * チャンネル設定 (channel_settings) を DB から再読み込みする。
   */
  @route("reload-channel-settings")
  @post
  @extension("x-ogen-operation-group", "Runtime")
  op reloadChannelSettings(): OkResponse;

  /**
   * 会話コンテキストを強制的に圧縮する。
   */
  @route("compact")
  @post
  @extension("x-ogen-operation-group", "Runtime")
  op compact(): CompactResponse;

  /**
   * ディスク上の IDENTITY.md / SOUL.md から system prompt を再構築する。
   */
  @route("reload-prompt")
  @post
  @extension("x-ogen-operation-group", "Runtime")
  op reloadPrompt(): ReloadPromptResponse;
}
