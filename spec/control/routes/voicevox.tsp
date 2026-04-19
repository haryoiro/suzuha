import "@typespec/http";
import "@typespec/rest";
import "@typespec/openapi";

using TypeSpec.Http;
using TypeSpec.Rest;
using TypeSpec.OpenAPI;

namespace SuzuhaControl;

model VoicevoxSpeaker {
  speaker_id: int32;
}

model SetSpeakerRequest {
  speaker_id: int32;
}

@route("/internal/voicevox")
@tag("Voicevox")
interface Voicevox {
  /**
   * VOICEVOX engine の /speakers をプロキシして話者一覧を返す。
   * レスポンスは engine そのままの JSON。
   */
  @route("speakers")
  @get
  @extension("x-ogen-operation-group", "Voicevox")
  op speakers(): Record<unknown>[];

  /**
   * 現在設定されている話者 ID を返す。
   */
  @route("speaker")
  @get
  @extension("x-ogen-operation-group", "Voicevox")
  op getSpeaker(): VoicevoxSpeaker;

  /**
   * 話者 ID を変更する (runtime + config mutation)。
   */
  @route("speaker")
  @put
  @extension("x-ogen-operation-group", "Voicevox")
  op setSpeaker(@body req: SetSpeakerRequest): OkResponse;
}
