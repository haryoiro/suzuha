package admin

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/haryoiro/suzuha/internal/admin/api"
	"github.com/haryoiro/suzuha/internal/feature/location"
)

func (h *AdminHandler) locStore() *location.Store {
	return location.NewStore(h.db)
}

func (h *AdminHandler) LocationUserLocation(ctx context.Context, params api.LocationUserLocationParams) (*api.LocationUserLocationOK, error) {
	locs, err := h.locStore().QueryLatestByUserID(ctx, params.UserId)
	if err != nil {
		return nil, fmt.Errorf("internal error")
	}

	data := make([]api.UserLocation, len(locs))
	for i, l := range locs {
		ul := api.UserLocation{
			Timestamp: l.Location.Timestamp.Format(time.RFC3339),
			Latitude:  l.Location.Latitude,
			Longitude: l.Location.Longitude,
		}
		if l.Device != nil {
			ul.DeviceID = l.Device.DeviceID
			ul.UserID = l.Device.UserID
		}
		if l.Location.Altitude != 0 {
			ul.Altitude = api.NewOptFloat64(l.Location.Altitude)
		}
		if l.Location.Speed != 0 {
			ul.Speed = api.NewOptFloat64(l.Location.Speed)
		}
		if l.Location.HorizontalAccuracy != 0 {
			ul.Accuracy = api.NewOptFloat64(l.Location.HorizontalAccuracy)
		}
		if l.PlaceName != "" {
			ul.PlaceName = api.NewOptString(l.PlaceName)
		}
		data[i] = ul
	}
	return &api.LocationUserLocationOK{Data: data}, nil
}

func (h *AdminHandler) LocationListDevices(ctx context.Context) (*api.LocationListDevicesOK, error) {
	devices, err := h.locStore().ListDevices(ctx)
	if err != nil {
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

	if err := h.locStore().UpsertDevice(ctx, deviceID, ownerName, userID); err != nil {
		return nil, fmt.Errorf("internal error")
	}

	h.notifyAgentReload(ctx, "/internal/reload-location-settings")
	return &api.OkResponse{Ok: true}, nil
}

func (h *AdminHandler) LocationDeleteDevice(ctx context.Context, params api.LocationDeleteDeviceParams) error {
	deviceID := fmt.Sprintf("%d", params.ID)
	if err := h.locStore().DeleteDevice(ctx, deviceID); err != nil {
		return fmt.Errorf("internal error")
	}
	h.notifyAgentReload(ctx, "/internal/reload-location-settings")
	return nil
}

func (h *AdminHandler) LocationListPlaces(ctx context.Context) (*api.LocationListPlacesOK, error) {
	places, err := h.locStore().ListPlaces(ctx)
	if err != nil {
		return nil, fmt.Errorf("internal error")
	}

	data := make([]api.Place, len(places))
	for i, p := range places {
		pid, err := strconv.Atoi(p.ID)
		if err != nil {
			return nil, fmt.Errorf("parsing place ID %q: %w", p.ID, err)
		}
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
	p := location.Place{
		Name:      req.Name,
		Latitude:  req.Latitude,
		Longitude: req.Longitude,
		RadiusM:   radiusM,
	}
	if err := h.locStore().CreatePlace(ctx, p); err != nil {
		return nil, fmt.Errorf("internal error")
	}
	h.notifyAgentReload(ctx, "/internal/reload-location-settings")
	return &api.OkResponse{Ok: true}, nil
}

func (h *AdminHandler) LocationUpdatePlace(ctx context.Context, req *api.UpdatePlaceRequest, params api.LocationUpdatePlaceParams) (*api.OkResponse, error) {
	p := location.Place{
		ID:        fmt.Sprintf("%d", params.ID),
		Name:      req.Name.Or(""),
		Latitude:  req.Latitude.Or(0),
		Longitude: req.Longitude.Or(0),
		RadiusM:   req.RadiusM.Or(0),
	}
	if err := h.locStore().UpdatePlace(ctx, p); err != nil {
		return nil, fmt.Errorf("internal error")
	}
	h.notifyAgentReload(ctx, "/internal/reload-location-settings")
	return &api.OkResponse{Ok: true}, nil
}

func (h *AdminHandler) LocationDeletePlace(ctx context.Context, params api.LocationDeletePlaceParams) error {
	if err := h.locStore().DeletePlace(ctx, fmt.Sprintf("%d", params.ID)); err != nil {
		return fmt.Errorf("internal error")
	}
	h.notifyAgentReload(ctx, "/internal/reload-location-settings")
	return nil
}
