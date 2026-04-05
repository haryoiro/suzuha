package admin

import (
	"context"
	"time"
)

// Action はスケジュール済みアクションを表す。
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

// ActionListOpts はアクション一覧のフィルタリングオプションを指定する。
type ActionListOpts struct {
	Status string
	Limit  int
}

// ActionUpdateFields はアクションの更新対象フィールドを指定する。
type ActionUpdateFields struct {
	ChannelID   *string
	Content     *string
	Mode        *string
	ScheduledAt *string
	CronExpr    *string
	Status      *string
}

// ActionStore はスケジュール済みアクションの CRUD 操作を提供する。
type ActionStore interface {
	List(ctx context.Context, opts ActionListOpts) ([]Action, error)
	Create(ctx context.Context, a *Action) error
	Update(ctx context.Context, id string, fields ActionUpdateFields) error
	Delete(ctx context.Context, id string) error
}

// UserLocation はデバイス・場所情報を含む位置情報を表す。
type UserLocation struct {
	DeviceID           string
	UserID             string
	Latitude           float64
	Longitude          float64
	Altitude           float64
	Speed              float64
	HorizontalAccuracy float64
	PlaceName          string
	Timestamp          time.Time
}

// DeviceMapping はデバイスとユーザーの紐付けを表す。
type DeviceMapping struct {
	DeviceID  string
	OwnerName string
	UserID    string
	CreatedAt string
}

// Place は名前付きの地理的な場所を表す。
type Place struct {
	ID        string
	Name      string
	Latitude  float64
	Longitude float64
	RadiusM   float64
	CreatedAt string
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

// DiaryEntry は日記エントリを表す。
type DiaryEntry struct {
	ID          string
	Kind        string
	Content     string
	PeriodStart time.Time
	PeriodEnd   time.Time
	CreatedAt   time.Time
}

// DiaryStore は日記エントリの読み取りを提供する。
type DiaryStore interface {
	ListByKind(ctx context.Context, kind string, since time.Time, limit int) ([]DiaryEntry, error)
}
