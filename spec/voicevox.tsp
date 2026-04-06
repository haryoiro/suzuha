import "@typespec/http";
import "@typespec/rest";

using TypeSpec.Http;
using TypeSpec.Rest;

namespace SuzuhaAdmin;

// Voicevox TTS (proxy to agent)
@route("/api/voicevox")
@tag("Voicevox")
interface Voicevox {
  @route("speakers") @get op speakers(): unknown;
  @route("speaker") @get op currentSpeaker(): unknown;
  @route("speaker") @put op setSpeaker(@body body: unknown): unknown;
}
