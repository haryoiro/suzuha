package admin

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/haryoiro/suzuha/internal/admin/api"
)

func (h *AdminHandler) LocationUserLocation(ctx context.Context, params api.LocationUserLocationParams) (*api.LocationUserLocationOK, error) {
	locs, err := h.locStore.QueryLatestByUserID(ctx, params.UserId)
	if err != nil {
		h.logger.Error("ユーザー位置情報の取得に失敗", "user_id", params.UserId, "error", err.Error())
		return nil, fmt.Errorf("internal error")
	}

	data := make([]api.UserLocation, len(locs))
	for i, l := range locs {
		ul := api.UserLocation{
			DeviceID:  l.DeviceID,
			UserID:    l.UserID,
			Timestamp: l.Timestamp.Format(time.RFC3339),
			Latitude:  l.Latitude,
			Longitude: l.Longitude,
		}
		if l.Altitude != 0 {
			ul.Altitude = api.NewOptFloat64(l.Altitude)
		}
		if l.Speed != 0 {
			ul.Speed = api.NewOptFloat64(l.Speed)
		}
		if l.HorizontalAccuracy != 0 {
			ul.Accuracy = api.NewOptFloat64(l.HorizontalAccuracy)
		}
		if l.PlaceName != "" {
			ul.PlaceName = api.NewOptString(l.PlaceName)
		}
		data[i] = ul
	}
	return &api.LocationUserLocationOK{Data: data}, nil
}

func (h *AdminHandler) LocationListDevices(ctx context.Context) (*api.LocationListDevicesOK, error) {
	devices, err := h.locStore.ListDevices(ctx)
	if err != nil {
		h.logger.Error("位置情報デバイス一覧の取得に失敗", "error", err.Error())
		return nil, fmt.Errorf("internal error")
	}

	data := make([]api.DeviceMapping, len(devices))
	for i, d := range devices {
		data[i] = api.DeviceMapping{
			ID:        int32(i + 1),
			DeviceID:  d.DeviceID,
			OwnerName: d.OwnerName,
			UserID:    d.UserID,
			CreatedAt: d.CreatedAt,
		}
	}
	return &api.LocationListDevicesOK{Data: data}, nil
}

func (h *AdminHandler) LocationUpdateDevice(ctx context.Context, req *api.UpdateDeviceRequest, params api.LocationUpdateDeviceParams) (*api.OkResponse, error) {
	deviceID := fmt.Sprintf("%d", params.ID)
	ownerName := req.OwnerName.Or("")
	userID := req.UserID.Or("")

	if err := h.locStore.UpsertDevice(ctx, deviceID, ownerName, userID); err != nil {
		h.logger.Error("位置情報デバイスの登録・更新に失敗", "error", err.Error())
		return nil, fmt.Errorf("internal error")
	}

	h.notifyAgentReload(ctx, "/internal/reload-location-settings")
	return &api.OkResponse{Ok: true}, nil
}

func (h *AdminHandler) LocationDeleteDevice(ctx context.Context, params api.LocationDeleteDeviceParams) error {
	deviceID := fmt.Sprintf("%d", params.ID)
	if err := h.locStore.DeleteDevice(ctx, deviceID); err != nil {
		h.logger.Error("位置情報デバイスの削除に失敗", "error", err.Error())
		return fmt.Errorf("internal error")
	}
	h.notifyAgentReload(ctx, "/internal/reload-location-settings")
	return nil
}

func (h *AdminHandler) LocationListPlaces(ctx context.Context) (*api.LocationListPlacesOK, error) {
	places, err := h.locStore.ListPlaces(ctx)
	if err != nil {
		h.logger.Error("場所一覧の取得に失敗", "error", err.Error())
		return nil, fmt.Errorf("internal error")
	}

	data := make([]api.Place, len(places))
	for i, p := range places {
		pid, _ := strconv.Atoi(p.ID)
		data[i] = api.Place{
			ID:        int32(pid),
			Name:      p.Name,
			Latitude:  p.Latitude,
			Longitude: p.Longitude,
			RadiusM:   p.RadiusM,
			CreatedAt: p.CreatedAt,
		}
	}
	return &api.LocationListPlacesOK{Data: data}, nil
}

func (h *AdminHandler) LocationCreatePlace(ctx context.Context, req *api.CreatePlaceRequest) (*api.OkResponse, error) {
	radiusM := req.RadiusM.Or(50)
	p := Place{
		Name:      req.Name,
		Latitude:  req.Latitude,
		Longitude: req.Longitude,
		RadiusM:   radiusM,
	}
	if err := h.locStore.CreatePlace(ctx, p); err != nil {
		h.logger.Error("場所の作成に失敗", "error", err.Error())
		return nil, fmt.Errorf("internal error")
	}
	h.notifyAgentReload(ctx, "/internal/reload-location-settings")
	return &api.OkResponse{Ok: true}, nil
}

func (h *AdminHandler) LocationUpdatePlace(ctx context.Context, req *api.UpdatePlaceRequest, params api.LocationUpdatePlaceParams) (*api.OkResponse, error) {
	p := Place{
		ID:        fmt.Sprintf("%d", params.ID),
		Name:      req.Name.Or(""),
		Latitude:  req.Latitude.Or(0),
		Longitude: req.Longitude.Or(0),
		RadiusM:   req.RadiusM.Or(0),
	}
	if err := h.locStore.UpdatePlace(ctx, p); err != nil {
		h.logger.Error("場所の更新に失敗", "error", err.Error())
		return nil, fmt.Errorf("internal error")
	}
	h.notifyAgentReload(ctx, "/internal/reload-location-settings")
	return &api.OkResponse{Ok: true}, nil
}

func (h *AdminHandler) LocationDeletePlace(ctx context.Context, params api.LocationDeletePlaceParams) error {
	if err := h.locStore.DeletePlace(ctx, fmt.Sprintf("%d", params.ID)); err != nil {
		h.logger.Error("場所の削除に失敗", "error", err.Error())
		return fmt.Errorf("internal error")
	}
	h.notifyAgentReload(ctx, "/internal/reload-location-settings")
	return nil
}
