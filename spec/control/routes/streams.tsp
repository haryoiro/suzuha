import "@typespec/http";
import "@typespec/rest";
import "@typespec/openapi";

using TypeSpec.Http;
using TypeSpec.Rest;
using TypeSpec.OpenAPI;

namespace SuzuhaControl;

/**
 * SSE / binary / stream 系 endpoint。
 * ogen の x-ogen-raw-response: true で RawHandler interface に生成し、
 * サーバー実装は http.ResponseWriter を受け取って直接書き込む。
 * Handler 本体は gen.RawHandler として扱われ、通常の gen.Handler とは分離。
 */

model SSEBody {
  @header contentType: "text/event-stream";

  @body body: string;
}

model JPEGBody {
  @header contentType: "image/jpeg";

  @body body: bytes;
}

/**
 * SSE / binary / stream 系 endpoint。
 * `@extension("x-ogen-raw-response", true)` を operation に付けると、
 * post-process スクリプトが media-type レベルに移し替えて ogen の
 * RawHandler interface 生成をトリガーする。
 */
@route("/internal")
@tag("Raw")
interface RawStreams {
  /** 内部ログの SSE ストリーム。observe.RingBuffer を tail する。 */
  @route("logs")
  @get
  @extension("x-ogen-operation-group", "Raw")
  @extension("x-ogen-raw-response", true)
  op logs(): SSEBody;

  /** YOLO 検出結果の SSE ストリーム。vision.Feature.Frames() から配信。 */
  @route("device/detections")
  @get
  @extension("x-ogen-operation-group", "Raw")
  @extension("x-ogen-raw-response", true)
  op deviceDetections(): SSEBody;

  /** デバイスカメラの JPEG フレーム (binary response)。 */
  @route("device/frame")
  @get
  @extension("x-ogen-operation-group", "Raw")
  @extension("x-ogen-raw-response", true)
  op deviceFrame(): JPEGBody;
}
