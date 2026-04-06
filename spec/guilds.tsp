import "@typespec/http";
import "@typespec/rest";

using TypeSpec.Http;
using TypeSpec.Rest;

namespace SuzuhaAdmin;

model Guild {
  id: string;
  name: string;
  updated_at: string;
  member_count: int32;
  channel_count: int32;
}

model GuildChannel {
  channel_id: string;
  channel_name: string;
  user_count: int32;
  last_seen_at: string;
  last_user_message_at?: string;
}

@route("/api/guilds")
@tag("Guilds")
interface Guilds {
  @get op list(): { data: Guild[] };
  @route("{id}/channels") @get op channels(@path id: string): { data: GuildChannel[] };
}
