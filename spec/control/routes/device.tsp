import "@typespec/http";
import "@typespec/rest";
import "@typespec/openapi";

using TypeSpec.Http;
using TypeSpec.Rest;
using TypeSpec.OpenAPI;

namespace SuzuhaControl;

// Note: GET /internal/device/frame (binary) と
// GET /internal/device/detections (SSE) はストリーミングのため
// 手動ルートで raw http.Handler として提供する。

model VisionStatus {
  enabled: boolean;
}

model SetVisionRequest {
  enabled: boolean;
}

model ServoRequest {
  pan: int32;
  tilt: int32;
}

model ServoResponse {
  ok: boolean;
  pan: int32;
  tilt: int32;
}

model VolumeRequest {
  /** 0-100 */
  level: int32;
}

model VolumeResponse {
  ok: boolean;
  level: int32;
}

model TrackerStatus {
  enabled: boolean;
  config: Record<unknown>;
}

/**
 * トラッカー設定のパッチ。全フィールド optional。
 * enabled と target_label は独自 setter、残りは ApplyPartial 経由で更新される。
 */
model TrackerPatch {
  enabled?: boolean;
  target_label?: string;
  dead_zone?: float64;
  smoothing_alpha?: float64;
  proportional_gain?: float64;
  max_deg_per_frame?: float64;
  invert_pan?: boolean;
  invert_tilt?: boolean;
}

@route("/internal/device")
@tag("Device")
interface Device {
  /**
   * 視界変化検出 (ChangeDetector) の状態を返す。
   */
  @route("vision")
  @get
  @extension("x-ogen-operation-group", "Device")
  op getVision(): VisionStatus;

  /**
   * 視界変化検出の ON/OFF を切り替える。
   */
  @route("vision")
  @put
  @extension("x-ogen-operation-group", "Device")
  op setVision(@body req: SetVisionRequest): OkResponse;

  /**
   * ESP32 のサーボ (pan/tilt) を指定角度に動かす。
   */
  @route("servo")
  @post
  @extension("x-ogen-operation-group", "Device")
  op servo(@body req: ServoRequest): ServoResponse;

  /**
   * ESP32 のスピーカー音量 (0-100) を設定する。
   */
  @route("volume")
  @put
  @extension("x-ogen-operation-group", "Device")
  op volume(@body req: VolumeRequest): VolumeResponse;

  /**
   * オブジェクト追尾トラッカーの状態と設定を返す。
   */
  @route("tracker")
  @get
  @extension("x-ogen-operation-group", "Device")
  op getTracker(): TrackerStatus;

  /**
   * トラッカー設定をパッチ適用する。指定されたフィールドのみ更新。
   */
  @route("tracker")
  @put
  @extension("x-ogen-operation-group", "Device")
  op patchTracker(@body req: TrackerPatch): OkResponse;
}
