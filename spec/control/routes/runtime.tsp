import "@typespec/http";
import "@typespec/rest";

using TypeSpec.Http;
using TypeSpec.Rest;

namespace SuzuhaControl;

model OkResponse {
  ok: boolean;
}

@route("/internal")
@tag("Runtime")
interface Runtime {
  /**
   * チャンネル設定 (channel_settings) を DB から再読み込みする。
   */
  @route("reload-channel-settings") @post op reloadChannelSettings(): OkResponse;
}
