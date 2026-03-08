import "@typespec/http";
import "@typespec/rest";

using TypeSpec.Http;
using TypeSpec.Rest;

namespace SuzuhaAdmin;

model Preference {
  id: int32;
  category: string;
  topic: string;
  stance: "liked" | "disliked" | "curious" | "undecided";
  confidence: float64;
  reasoning: string;
  encounters: int32;
  shared: boolean;
  last_evaluated_at?: string;
  created_at: string;
  updated_at: string;
}

model PreferenceStats {
  total: int32;
  liked: int32;
  disliked: int32;
  curious: int32;
  undecided: int32;
}

model UpdatePreferenceRequest {
  stance?: "liked" | "disliked" | "curious" | "undecided";
  confidence?: float64;
  reasoning?: string;
}

@route("/api/preferences")
@tag("Preferences")
interface Preferences {
  @get op list(@query stance?: string): PaginatedList<Preference>;
  @route("stats") @get op stats(): PreferenceStats;
  @route("{id}") @put op update(@path id: int32, @body body: UpdatePreferenceRequest): OkResponse;
  @route("{id}") @delete op delete(@path id: int32): { @statusCode statusCode: 204 };
}
