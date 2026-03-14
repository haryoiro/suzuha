import "@typespec/http";
import "@typespec/rest";
import "@typespec/openapi";
import "@typespec/openapi3";

import "./memories.tsp";
import "./users.tsp";
import "./guilds.tsp";
import "./channels.tsp";
import "./actions.tsp";
import "./feeds.tsp";
import "./location.tsp";
import "./prompts.tsp";
import "./tools.tsp";
import "./logs.tsp";
import "./agent.tsp";

using TypeSpec.Http;
using TypeSpec.Rest;
using TypeSpec.OpenAPI;

@service(#{ title: "Suzuha Admin API" })
@info(#{ version: "1.0.0" })
@server("http://localhost:8080", "Admin API")
namespace SuzuhaAdmin;

// ── Common models ──

model OkResponse {
  ok: boolean;
}

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

// ── Health ──

@route("/api/health")
@tag("Health")
interface Health {
  @get op check(): { status: string };
}
