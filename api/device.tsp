import "@typespec/http";
import "@typespec/rest";

using TypeSpec.Http;
using TypeSpec.Rest;

namespace SuzuhaAdmin;

// Device control (proxy to agent)
// Note: GET /api/device/frame (binary) and GET /api/device/detections (SSE)
// are registered as manual routes in server.go.
@route("/api/device")
@tag("Device")
interface Device {
  @route("vision") @get op visionGet(): unknown;
  @route("vision") @put op visionSet(@body body: unknown): unknown;
  @route("servo") @post op servo(@body body: unknown): unknown;
  @route("volume") @put op volume(@body body: unknown): unknown;
  @route("tracker") @get op trackerGet(): unknown;
  @route("tracker") @put op trackerSet(@body body: unknown): unknown;
}
