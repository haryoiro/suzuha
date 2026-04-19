import "@typespec/http";
import "@typespec/rest";
import "@typespec/openapi";
import "@typespec/openapi3";

import "./models/common.tsp";

import "./routes/health.tsp";
import "./routes/memories.tsp";
import "./routes/users.tsp";
import "./routes/guilds.tsp";
import "./routes/channels.tsp";
import "./routes/actions.tsp";
import "./routes/location.tsp";
import "./routes/prompts.tsp";
import "./routes/tools.tsp";
import "./routes/logs.tsp";
import "./routes/agent.tsp";
import "./routes/diary.tsp";
import "./routes/scheduler.tsp";
import "./routes/device.tsp";
import "./routes/voicevox.tsp";
import "./routes/streams.tsp";

using TypeSpec.Http;
using TypeSpec.Rest;
using TypeSpec.OpenAPI;

@service(#{ title: "Suzuha Admin API" })
@info(#{ version: "1.0.0" })
@server("http://localhost:8080", "Admin API")
namespace SuzuhaAdmin;
