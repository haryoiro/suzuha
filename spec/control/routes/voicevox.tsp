import "@typespec/http";
import "@typespec/rest";

using TypeSpec.Http;
using TypeSpec.Rest;

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
  @route("speakers") @get op speakers(): Record<unknown>[];

  /**
   * 現在設定されている話者 ID を返す。
   */
  @route("speaker") @get op getSpeaker(): VoicevoxSpeaker;

  /**
   * 話者 ID を変更する (runtime + config mutation)。
   */
  @route("speaker") @put op setSpeaker(@body req: SetSpeakerRequest): OkResponse;
}
