import "@typespec/http";
import "@typespec/rest";

using TypeSpec.Http;
using TypeSpec.Rest;

namespace SuzuhaAdmin;

model DiaryEntry {
  id: string;
  kind: string;
  content: string;
  period_start: string;
  period_end: string;
  created_at: string;
}

@route("/api/diary")
@tag("Diary")
interface Diary {
  @get op list(
    @query kind?: string,
    @query offset?: int32,
    @query limit?: int32,
  ): PaginatedList<DiaryEntry>;
}
