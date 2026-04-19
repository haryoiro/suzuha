import "@typespec/http";
import "@typespec/rest";
import "@typespec/openapi";
import "@typespec/openapi3";

import "./routes/runtime.tsp";
import "./routes/agent.tsp";

using TypeSpec.Http;
using TypeSpec.Rest;
using TypeSpec.OpenAPI;

@service(#{ title: "Suzuha Control API" })
@info(#{ version: "1.0.0" })
@server("http://localhost:9090", "Internal control plane")
namespace SuzuhaControl;
