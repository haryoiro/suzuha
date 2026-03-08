import "@typespec/http";
import "@typespec/rest";

using TypeSpec.Http;
using TypeSpec.Rest;

namespace SuzuhaAdmin;

// Conversation logs
model ConversationSummary {
  channel_id: string;
  messages: int32;
  turns: int32;
  first: string;
  last: string;
}

@route("/api/conversation-logs")
@tag("Logs")
interface ConversationLogs {
  @get op list(): { data: ConversationSummary[] };
  /** Export as JSONL. */
  @route("export") @get op export(
    @query channel_id?: string,
    @query since?: string,
    @query until?: string,
  ): string;
}

// Boredom
model BoredomStatus {
  boredom: float64;
  last_interaction?: string;
  last_channel: string;
  last_posted_at?: string;
  post_threshold: float64;
}

@route("/api/boredom")
@tag("Agent")
interface Boredom {
  @get op get(): BoredomStatus;
}
