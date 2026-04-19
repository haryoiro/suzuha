package admin

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/haryoiro/suzuha/internal/api/admin/gen"
)

func (h *AdminHandler) LocationUserLocation(ctx context.Context, params gen.LocationUserLocationParams) (*gen.LocationUserLocationOK, error) {
	locs, err := h.locStore.QueryLatestByUserID(ctx, params.UserId)
	if err != nil {
		return nil, fmt.Errorf("internal error")
	}

	data := make([]gen.UserLocation, len(locs))
	for i, l := range locs {
		ul := gen.UserLocation{
			Timestamp: l.Location.Timestamp.Format(time.RFC3339),
			Latitude:  l.Location.Latitude,
			Longitude: l.Location.Longitude,
		}
		if l.Device != nil {
			ul.DeviceID = l.Device.DeviceID
			ul.UserID = l.Device.UserID
		}
		if l.Location.Altitude != 0 {
			ul.Altitude = gen.NewOptFloat64(l.Location.Altitude)
		}
		if l.Location.Speed != 0 {
			ul.Speed = gen.NewOptFloat64(l.Location.Speed)
		}
		if l.Location.HorizontalAccuracy != 0 {
			ul.Accuracy = gen.NewOptFloat64(l.Location.HorizontalAccuracy)
		}
		if l.PlaceName != "" {
			ul.PlaceName = gen.NewOptString(l.PlaceName)
		}
		data[i] = ul
	}
	return &gen.LocationUserLocationOK{Data: data}, nil
}

func (h *AdminHandler) LocationListDevices(ctx context.Context) (*gen.LocationListDevicesOK, error) {
	devices, err := h.locStore.ListDevices(ctx)
	if err != nil {
		return nil, fmt.Errorf("internal error")
	}

	data := make([]gen.DeviceMapping, len(devices))
	for i, d := range devices {
		data[i] = gen.DeviceMapping{
			ID:        int32(i + 1),
			DeviceID:  d.DeviceID,
			OwnerName: d.OwnerName,
			UserID:    d.UserID,
			CreatedAt: d.CreatedAt,
		}
	}
	return &gen.LocationListDevicesOK{Data: data}, nil
}

func (h *AdminHandler) LocationUpdateDevice(ctx context.Context, req *gen.UpdateDeviceRequest, params gen.LocationUpdateDeviceParams) (*gen.OkResponse, error) {
	deviceID := fmt.Sprintf("%d", params.ID)
	ownerName := req.OwnerName.Or("")
	userID := req.UserID.Or("")

	if err := h.locStore.UpsertDevice(ctx, deviceID, ownerName, userID); err != nil {
		return nil, fmt.Errorf("internal error")
	}

	h.notifyAgentReload(ctx, "/internal/reload-location-settings")
	return &gen.OkResponse{Ok: true}, nil
}

func (h *AdminHandler) LocationDeleteDevice(ctx context.Context, params gen.LocationDeleteDeviceParams) error {
	deviceID := fmt.Sprintf("%d", params.ID)
	if err := h.locStore.DeleteDevice(ctx, deviceID); err != nil {
		return fmt.Errorf("internal error")
	}
	h.notifyAgentReload(ctx, "/internal/reload-location-settings")
	return nil
}

func (h *AdminHandler) LocationListPlaces(ctx context.Context) (*gen.LocationListPlacesOK, error) {
	places, err := h.locStore.ListPlaces(ctx)
	if err != nil {
		return nil, fmt.Errorf("internal error")
	}

	data := make([]gen.Place, len(places))
	for i, p := range places {
		pid, err := strconv.Atoi(p.ID)
		if err != nil {
			return nil, fmt.Errorf("parsing place ID %q: %w", p.ID, err)
		}
		data[i] = gen.Place{
			ID:        int32(pid),
			Name:      p.Name,
			Latitude:  p.Latitude,
			Longitude: p.Longitude,
			RadiusM:   p.RadiusM,
			CreatedAt: p.CreatedAt,
		}
	}
	return &gen.LocationListPlacesOK{Data: data}, nil
}

func (h *AdminHandler) LocationCreatePlace(ctx context.Context, req *gen.CreatePlaceRequest) (*gen.OkResponse, error) {
	radiusM := req.RadiusM.Or(50)
	p := Place{
		Name:      req.Name,
		Latitude:  req.Latitude,
		Longitude: req.Longitude,
		RadiusM:   radiusM,
	}
	if err := h.locStore.CreatePlace(ctx, p); err != nil {
		return nil, fmt.Errorf("internal error")
	}
	h.notifyAgentReload(ctx, "/internal/reload-location-settings")
	return &gen.OkResponse{Ok: true}, nil
}

func (h *AdminHandler) LocationUpdatePlace(ctx context.Context, req *gen.UpdatePlaceRequest, params gen.LocationUpdatePlaceParams) (*gen.OkResponse, error) {
	p := Place{
		ID:        fmt.Sprintf("%d", params.ID),
		Name:      req.Name.Or(""),
		Latitude:  req.Latitude.Or(0),
		Longitude: req.Longitude.Or(0),
		RadiusM:   req.RadiusM.Or(0),
	}
	if err := h.locStore.UpdatePlace(ctx, p); err != nil {
		return nil, fmt.Errorf("internal error")
	}
	h.notifyAgentReload(ctx, "/internal/reload-location-settings")
	return &gen.OkResponse{Ok: true}, nil
}

func (h *AdminHandler) LocationDeletePlace(ctx context.Context, params gen.LocationDeletePlaceParams) error {
	if err := h.locStore.DeletePlace(ctx, fmt.Sprintf("%d", params.ID)); err != nil {
		return fmt.Errorf("internal error")
	}
	h.notifyAgentReload(ctx, "/internal/reload-location-settings")
	return nil
}
