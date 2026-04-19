import "@typespec/http";

using TypeSpec.Http;

namespace SuzuhaAdmin;

/** 成功レスポンス（単純な肯定応答）。 */
model OkResponse {
  ok: boolean;
}

/** エラーレスポンス。 */
model ErrorResponse {
  error: string;
}

/** Paginated list wrapper. */
model PaginatedList<T> {
  data: T[];
  total: int32;
}

/** Standard pagination query. */
model PaginationQuery {
  @query offset?: int32;
  @query limit?: int32;
}
