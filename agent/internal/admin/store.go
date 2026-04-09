package admin

import (
	"context"
	"time"
)

// ActionStore はスケジュールアクションの CRUD を提供する。
type ActionStore interface {
	List(ctx context.Context, opts ActionListOpts) ([]Action, error)
	Create(ctx context.Context, a *Action) error
	Update(ctx context.Context, id string, fields ActionUpdateFields) error
	Delete(ctx context.Context, id string) error
}

// Action はスケジュールアクション行を表す。
type Action struct {
	ID            string
	ChannelID     string
	Content       string
	Mode          string
	ScheduledAt   time.Time
	CronExpr      string
	RandomMinutes int
	CreatedBy     string
	Status        string
	RetryCount    int
	ExecutedAt    *time.Time
	CreatedAt     time.Time
}

// ActionListOpts は List のフィルタリングオプション。
type ActionListOpts struct {
	Status string
	Limit  int
}

// ActionUpdateFields は部分更新のフィールド。
type ActionUpdateFields struct {
	ChannelID   *string
	Content     *string
	Mode        *string
	ScheduledAt *string
	CronExpr    *string
	Status      *string
}

// DiaryStore は日記エントリの読み取りアクセスを提供する。
type DiaryStore interface {
	ListByKind(ctx context.Context, kind string, since time.Time, limit int) ([]DiaryEntry, error)
}

// DiaryEntry は日記エントリ。
type DiaryEntry struct {
	ID          string
	Kind        string
	Content     string
	PeriodStart time.Time
	PeriodEnd   time.Time
	CreatedAt   time.Time
}

// LocationStore は位置情報の管理操作を提供する。
type LocationStore interface {
	QueryLatestByUserID(ctx context.Context, userID string) ([]UserLocation, error)
	ListDevices(ctx context.Context) ([]DeviceMapping, error)
	UpsertDevice(ctx context.Context, deviceID, ownerName, userID string) error
	DeleteDevice(ctx context.Context, deviceID string) error
	ListPlaces(ctx context.Context) ([]Place, error)
	CreatePlace(ctx context.Context, p Place) error
	UpdatePlace(ctx context.Context, p Place) error
	DeletePlace(ctx context.Context, id string) error
}

// UserLocation は位置情報とデバイスマッピングの組み合わせ。
type UserLocation struct {
	Location  *Location
	Device    *DeviceMapping
	PlaceName string
}

// Location は GPS 位置レコード。
type Location struct {
	Latitude           float64
	Longitude          float64
	Altitude           float64
	Speed              float64
	HorizontalAccuracy float64
	Timestamp          time.Time
}

// DeviceMapping はデバイス ID からオーナーへのマッピング。
type DeviceMapping struct {
	DeviceID  string
	OwnerName string
	UserID    string
	CreatedAt string
}

// Place は名前付き地理的位置。
type Place struct {
	ID        string
	Name      string
	Latitude  float64
	Longitude float64
	RadiusM   float64
	CreatedAt string
}
