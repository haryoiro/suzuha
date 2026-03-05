package location

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/haryoiro/suzuha/internal/tool"
)

var jst = time.FixedZone("JST", 9*60*60)

// GetLocation returns the latest location for a user, including matched place name.
type GetLocation struct {
	store *Store
}

// NewGetLocationTool creates the get_location tool.
func NewGetLocationTool(store *Store) *GetLocation {
	return &GetLocation{store: store}
}

func (t *GetLocation) Name() string { return "get_location" }
func (t *GetLocation) Description() string {
	return "ユーザーIDまたはデバイスIDから現在の位置情報を取得する。登録済みの場所（自宅・職場など）にいるかどうかも返す。"
}

func (t *GetLocation) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"user_id":   {"type": "string", "description": "ユーザーID。指定するとそのユーザーに紐づくデバイスの位置を返す。"},
			"device_id": {"type": "string", "description": "デバイスID。直接指定する場合。"}
		}
	}`)
}

type getLocationInput struct {
	UserID   string `json:"user_id"`
	DeviceID string `json:"device_id"`
}

func (t *GetLocation) Execute(ctx context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in getLocationInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("入力が不正です: " + err.Error()), nil
	}

	if in.UserID == "" && in.DeviceID == "" {
		return tool.ErrorResult("user_id または device_id のいずれかを指定してください。"), nil
	}

	var results []UserLocation

	if in.DeviceID != "" {
		// Direct device lookup from cache.
		loc := t.store.Latest(in.DeviceID)
		if loc == nil {
			return tool.TextResult("デバイス " + in.DeviceID + " の位置情報が見つかりません。"), nil
		}
		dm := t.store.DeviceMappingFor(in.DeviceID)
		placeName := t.store.NearestPlace(loc.Latitude, loc.Longitude)
		results = append(results, UserLocation{Location: loc, Device: dm, PlaceName: placeName})
	} else {
		// User ID lookup from cache.
		results = t.store.LatestByUserID(in.UserID)
	}

	if len(results) == 0 {
		return tool.TextResult("該当ユーザーの位置情報が見つかりません。"), nil
	}

	var sb strings.Builder
	for _, ul := range results {
		owner := ""
		if ul.Device != nil {
			owner = ul.Device.OwnerName
		}
		fmt.Fprintf(&sb, "[%s] %s: Lat=%.6f Lon=%.6f (更新: %s)",
			ul.Location.DeviceID, owner,
			ul.Location.Latitude, ul.Location.Longitude,
			ul.Location.Timestamp.In(jst).Format("2006-01-02 15:04"))
		if ul.PlaceName != "" {
			fmt.Fprintf(&sb, " → 現在地: %s", ul.PlaceName)
		}
		if ul.Location.BatteryLevel > 0 {
			fmt.Fprintf(&sb, " バッテリー: %.0f%%", ul.Location.BatteryLevel*100)
		}
		sb.WriteString("\n")
	}
	return tool.TextResult(sb.String()), nil
}

var _ tool.Tool = (*GetLocation)(nil)

// GetLocationHistory allows the LLM to query past locations.
type GetLocationHistory struct {
	store *Store
}

// NewGetLocationHistoryTool creates the location history tool.
func NewGetLocationHistoryTool(store *Store) *GetLocationHistory {
	return &GetLocationHistory{store: store}
}

func (t *GetLocationHistory) Name() string { return "get_location_history" }
func (t *GetLocationHistory) Description() string {
	return "直近の位置情報履歴を取得する。ユーザーの過去の移動パターンを確認できる。"
}

func (t *GetLocationHistory) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"device_id": {"type": "string", "description": "デバイスID。省略するとすべてのデバイスを検索する。"},
			"hours_ago": {"type": "number", "description": "何時間前までの履歴を取得するか。デフォルト24。"},
			"limit":     {"type": "integer", "description": "最大取得件数。デフォルト20。"}
		}
	}`)
}

type locationHistoryInput struct {
	DeviceID string  `json:"device_id"`
	HoursAgo float64 `json:"hours_ago"`
	Limit    int     `json:"limit"`
}

func (t *GetLocationHistory) Execute(ctx context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in locationHistoryInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("入力が不正です: " + err.Error()), nil
	}

	if in.HoursAgo <= 0 {
		in.HoursAgo = 24
	}
	if in.Limit <= 0 {
		in.Limit = 20
	}

	now := time.Now()
	since := now.Add(-time.Duration(in.HoursAgo * float64(time.Hour)))

	var locs []Location
	var err error
	if in.DeviceID != "" {
		locs, err = t.store.History(ctx, in.DeviceID, since, now, in.Limit)
	} else {
		locs, err = t.store.HistoryAll(ctx, since, now, in.Limit)
	}
	if err != nil {
		return tool.ErrorResult("クエリに失敗しました: " + err.Error()), nil
	}

	if len(locs) == 0 {
		return tool.TextResult("位置情報の履歴が見つかりませんでした。"), nil
	}

	// Deduplicate: skip entries that are within 30m of the previous entry
	// and have the same place, keeping only the first and last of a cluster.
	type entry struct {
		loc       Location
		placeName string
		owner     string
	}
	var entries []entry
	for _, loc := range locs {
		entries = append(entries, entry{
			loc:       loc,
			placeName: t.store.NearestPlace(loc.Latitude, loc.Longitude),
			owner:     t.store.OwnerName(loc.DeviceID),
		})
	}

	var filtered []entry
	for i, e := range entries {
		if i == 0 {
			filtered = append(filtered, e)
			continue
		}
		prev := filtered[len(filtered)-1]
		dist := haversineM(e.loc.Latitude, e.loc.Longitude, prev.loc.Latitude, prev.loc.Longitude)
		samePlace := e.placeName == prev.placeName
		// Keep if moved >30m, place changed, or this is the last entry.
		if dist > 30 || !samePlace || i == len(entries)-1 {
			filtered = append(filtered, e)
		}
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "位置情報履歴 (直近%.0f時間, %d件):\n", in.HoursAgo, len(filtered))
	for _, e := range filtered {
		fmt.Fprintf(&sb, "  %s [%s] %s Lat=%.6f Lon=%.6f",
			e.loc.Timestamp.In(jst).Format("2006-01-02 15:04"), e.loc.DeviceID, e.owner,
			e.loc.Latitude, e.loc.Longitude)
		if e.placeName != "" {
			fmt.Fprintf(&sb, " → %s", e.placeName)
		}
		if e.loc.Speed > 0.5 {
			fmt.Fprintf(&sb, " Speed=%.1fm/s", e.loc.Speed)
		}
		if e.loc.Motion != "" && e.loc.Motion != "[]" {
			fmt.Fprintf(&sb, " Motion=%s", e.loc.Motion)
		}
		sb.WriteString("\n")
	}
	return tool.TextResult(sb.String()), nil
}

var _ tool.Tool = (*GetLocationHistory)(nil)

// ReverseGeocode resolves lat/lon to a place name via Nominatim (OpenStreetMap).
type ReverseGeocode struct{}

func (t *ReverseGeocode) Name() string { return "reverse_geocode" }
func (t *ReverseGeocode) Description() string {
	return "緯度経度から地名・住所を取得する（逆ジオコーディング）。"
}

func (t *ReverseGeocode) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"latitude":  {"type": "number", "description": "緯度"},
			"longitude": {"type": "number", "description": "経度"}
		},
		"required": ["latitude", "longitude"]
	}`)
}

type reverseGeocodeInput struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type nominatimResponse struct {
	DisplayName string `json:"display_name"`
	Address     struct {
		Road        string `json:"road"`
		Suburb      string `json:"suburb"`
		City        string `json:"city"`
		Town        string `json:"town"`
		Village     string `json:"village"`
		State       string `json:"state"`
		Country     string `json:"country"`
		Postcode    string `json:"postcode"`
	} `json:"address"`
}

func (t *ReverseGeocode) Execute(ctx context.Context, input json.RawMessage) (*tool.ToolResult, error) {
	var in reverseGeocodeInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.ErrorResult("入力が不正です: " + err.Error()), nil
	}

	url := fmt.Sprintf(
		"https://nominatim.openstreetmap.org/reverse?format=jsonv2&lat=%f&lon=%f&accept-language=ja",
		in.Latitude, in.Longitude,
	)

	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return tool.ErrorResult("リクエストの構築に失敗しました: " + err.Error()), nil
	}
	req.Header.Set("User-Agent", "suzuha-bot/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return tool.ErrorResult("ジオコードリクエストに失敗しました: " + err.Error()), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return tool.ErrorResult(fmt.Sprintf("ジオコードAPIがステータス %d を返しました", resp.StatusCode)), nil
	}

	var nr nominatimResponse
	if err := json.NewDecoder(resp.Body).Decode(&nr); err != nil {
		return tool.ErrorResult("レスポンスの解析に失敗しました: " + err.Error()), nil
	}

	a := nr.Address
	locality := a.City
	if locality == "" {
		locality = a.Town
	}
	if locality == "" {
		locality = a.Village
	}

	var parts []string
	if a.Country != "" {
		parts = append(parts, a.Country)
	}
	if a.State != "" {
		parts = append(parts, a.State)
	}
	if locality != "" {
		parts = append(parts, locality)
	}

	if len(parts) == 0 {
		return tool.TextResult("地名が見つかりませんでした。"), nil
	}
	return tool.TextResult(strings.Join(parts, " ")), nil
}

var _ tool.Tool = (*ReverseGeocode)(nil)
