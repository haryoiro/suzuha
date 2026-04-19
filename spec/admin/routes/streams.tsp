import "@typespec/http";
import "@typespec/rest";
import "@typespec/openapi";

using TypeSpec.Http;
using TypeSpec.Rest;
using TypeSpec.OpenAPI;

namespace SuzuhaAdmin;

/**
 * SSE / binary / multipart 系 endpoint。
 * x-ogen-raw-response: true で RawHandler として生成される。
 * 既存の AdminHandler.{proxy,serve,upload,search}* メソッドを
 * RequestMiddleware 経由で呼び出す。
 */

model AdminSSEBody {
  @header contentType: "text/event-stream";

  @body body: string;
}

model AdminJPEGBody {
  @header contentType: "image/jpeg";

  @body body: bytes;
}

model AdminBinaryBody {
  @header contentType: "application/octet-stream";

  @body body: bytes;
}

model AdminJSONBody {
  @header contentType: "application/json";

  @body body: string;
}

@route("/api")
@tag("Raw")
interface AdminRawStreams {
  /** 内部ログの SSE ストリーム (agent の /internal/logs をプロキシ)。 */
  @route("logs/stream")
  @get
  @extension("x-ogen-operation-group", "Raw")
  @extension("x-ogen-raw-response", true)
  op logsStream(): AdminSSEBody;

  /** デバイスカメラの JPEG フレーム (agent をプロキシ)。 */
  @route("device/frame")
  @get
  @extension("x-ogen-operation-group", "Raw")
  @extension("x-ogen-raw-response", true)
  op deviceFrame(): AdminJPEGBody;

  /** YOLO 検出結果の SSE ストリーム (agent をプロキシ)。 */
  @route("device/detections")
  @get
  @extension("x-ogen-operation-group", "Raw")
  @extension("x-ogen-raw-response", true)
  op deviceDetections(): AdminSSEBody;

  /** メディアを取得 (パス以下の media を配信、Content-Type は動的)。 */
  @route("media/{path}")
  @get
  @extension("x-ogen-operation-group", "Raw")
  @extension("x-ogen-raw-response", true)
  op serveMedia(@path path: string): AdminBinaryBody;

  /** メモリに画像をアップロード (multipart/form-data)。 */
  @route("memories/{id}/media")
  @post
  @extension("x-ogen-operation-group", "Raw")
  @extension("x-ogen-raw-response", true)
  op uploadMedia(@path id: string): AdminJSONBody;

  /** 画像類似検索 (multipart/form-data で画像を受け取る)。 */
  @route("memories/search-image")
  @post
  @extension("x-ogen-operation-group", "Raw")
  @extension("x-ogen-raw-response", true)
  op searchByImage(): AdminJSONBody;
}
