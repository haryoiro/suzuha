import "@typespec/http";
import "@typespec/rest";

using TypeSpec.Http;
using TypeSpec.Rest;

namespace SuzuhaAdmin;

model Channel {
  channel_id: string;
  channel_name: string;
  guild_id: string;
  guild_name: string;
}

model ChannelSetting {
  channel_id: string;
  channel_name: string;
  guild_id: string;
  guild_name: string;
  user_count: int32;
  mode: "active" | "listen" | "disabled";
  home: boolean;
  last_user_message_at?: string;
  settings_updated_at?: string;
}

model UpdateChannelSettingRequest {
  mode?: "active" | "listen" | "disabled";
  home?: boolean;
  guild_id?: string;
}

@route("/api/channels")
@tag("Channels")
interface Channels {
  @get op list(): { data: Channel[] };
}

@route("/api/channel-settings")
@tag("Channels")
interface ChannelSettings {
  @get op list(@query guild_id?: string): { data: ChannelSetting[] };
  @route("{channelId}") @put op update(
    @path channelId: string,
    @body body: UpdateChannelSettingRequest,
  ): OkResponse;
  @route("{channelId}") @delete op delete(@path channelId: string): { @statusCode statusCode: 204 };
}
