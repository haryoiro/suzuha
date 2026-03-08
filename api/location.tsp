import "@typespec/http";
import "@typespec/rest";

using TypeSpec.Http;
using TypeSpec.Rest;

namespace SuzuhaAdmin;

model UserLocation {
  user_id: string;
  device_id: string;
  latitude: float64;
  longitude: float64;
  altitude?: float64;
  speed?: float64;
  accuracy?: float64;
  place_name?: string;
  timestamp: string;
}

model DeviceMapping {
  id: int32;
  device_id: string;
  owner_name: string;
  user_id: string;
  created_at: string;
}

model UpdateDeviceRequest {
  owner_name?: string;
  user_id?: string;
}

model Place {
  id: int32;
  name: string;
  latitude: float64;
  longitude: float64;
  radius_m: float64;
  created_at: string;
}

model CreatePlaceRequest {
  name: string;
  latitude: float64;
  longitude: float64;
  radius_m?: float64;
}

model UpdatePlaceRequest {
  name?: string;
  latitude?: float64;
  longitude?: float64;
  radius_m?: float64;
}

@route("/api/location")
@tag("Location")
interface Location {
  @route("{userId}") @get op userLocation(@path userId: string): { data: UserLocation[] };

  @route("devices") @get op listDevices(): { data: DeviceMapping[] };
  @route("devices/{id}") @put op updateDevice(@path id: int32, @body body: UpdateDeviceRequest): OkResponse;
  @route("devices/{id}") @delete op deleteDevice(@path id: int32): { @statusCode statusCode: 204 };

  @route("places") @get op listPlaces(): { data: Place[] };
  @route("places") @post op createPlace(@body body: CreatePlaceRequest): OkResponse;
  @route("places/{id}") @put op updatePlace(@path id: int32, @body body: UpdatePlaceRequest): OkResponse;
  @route("places/{id}") @delete op deletePlace(@path id: int32): { @statusCode statusCode: 204 };
}
