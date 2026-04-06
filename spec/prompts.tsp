import "@typespec/http";
import "@typespec/rest";

using TypeSpec.Http;
using TypeSpec.Rest;

namespace SuzuhaAdmin;

model PromptFile {
  name: string;
  content: string;
  updated_at: string;
}

model UpdatePromptRequest {
  content: string;
}

@route("/api/prompts")
@tag("Prompts")
interface Prompts {
  @get op list(): PromptFile[];
  @route("{name}") @get op get(@path name: string): PromptFile;
  @route("{name}") @put op update(@path name: string, @body body: UpdatePromptRequest): {
    ok: boolean;
    reloaded: boolean;
  };
}
