import "@typespec/http";
import "@typespec/rest";

using TypeSpec.Http;
using TypeSpec.Rest;

namespace SuzuhaAdmin;

model Platform {
  platform: string;
  platform_user_id: string;
  platform_name: string;
}

model User {
  id: string;
  display_name: string;
  role: "owner" | "member" | "guest";
  is_bot: boolean;
  closeness: float64;
  trust: float64;
  interest: float64;
  metadata?: Record<unknown>;
  created_at: string;
  updated_at: string;
  platforms: Platform[];
}

model UpdateUserRequest {
  display_name?: string;
  role?: "owner" | "member" | "guest";
  is_bot?: boolean;
}

model AffinityEvent {
  id: string;
  user_id: string;
  delta: float64;
  axis: "closeness" | "trust" | "interest";
  reason: string;
  created_at: string;
}

model UserGuild {
  guild_id: string;
  guild_name: string;
  channel_id: string;
  channel_name: string;
  last_seen_at: string;
}

model UserMemory {
  id: string;
  content: string;
  created_at: string;
  updated_at: string;
}

@route("/api/users")
@tag("Users")
interface Users {
  @get op list(@query offset?: int32, @query limit?: int32): PaginatedList<User>;
  @route("{id}") @get op get(@path id: string): { data: User };
  @route("{id}") @put op update(@path id: string, @body body: UpdateUserRequest): OkResponse;
  @route("{id}/affinity") @get op affinity(@path id: string, @query limit?: int32): { data: AffinityEvent[] };
  @route("{id}/guilds") @get op guilds(@path id: string): { data: UserGuild[] };
  @route("{id}/memories") @get op memories(@path id: string, @query limit?: int32): { data: UserMemory[] };
}
