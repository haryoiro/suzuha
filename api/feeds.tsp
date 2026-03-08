import "@typespec/http";
import "@typespec/rest";

using TypeSpec.Http;
using TypeSpec.Rest;

namespace SuzuhaAdmin;

model Feed {
  id: int32;
  name: string;
  url: string;
  channel_id: string;
  enabled: boolean;
  last_fetched_at?: string;
  created_at: string;
  updated_at: string;
}

model FeedItem {
  id: int32;
  feed_id: int32;
  title: string;
  url: string;
  content?: string;
  published_at?: string;
  created_at: string;
}

model FeedStats {
  total: int32;
  enabled: int32;
}

model CreateFeedRequest {
  name: string;
  url: string;
  channel_id: string;
}

model UpdateFeedRequest {
  name?: string;
  url?: string;
  channel_id?: string;
  enabled?: boolean;
}

@route("/api/feeds")
@tag("RSS Feeds")
interface Feeds {
  @get op list(): PaginatedList<Feed>;
  @post op create(@body body: CreateFeedRequest): {
    @statusCode statusCode: 201;
    @body body: { data: Feed };
  };
  @route("{id}") @get op get(@path id: int32): { data: Feed };
  @route("{id}") @put op update(@path id: int32, @body body: UpdateFeedRequest): { data: Feed };
  @route("{id}") @delete op delete(@path id: int32): { @statusCode statusCode: 204 };
  @route("stats") @get op stats(): FeedStats;
  @route("{id}/items") @get op items(@path id: int32, @query offset?: int32, @query limit?: int32): PaginatedList<FeedItem>;
}
