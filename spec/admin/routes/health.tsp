import "@typespec/http";
import "@typespec/rest";

using TypeSpec.Http;
using TypeSpec.Rest;

namespace SuzuhaAdmin;

@route("/api/health")
@tag("Health")
interface Health {
  @get op check(): { status: string };
}
