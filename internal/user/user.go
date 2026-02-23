package user

import (
	"context"
	"time"
)

// Role represents a user's permission level.
type Role string

// Role constants define the available permission levels.
const (
	RoleOwner  Role = "owner"
	RoleMember Role = "member"
	RoleGuest  Role = "guest"
)

// User is an internal user identity.
type User struct {
	ID          string         `json:"id"`
	DisplayName string         `json:"display_name"`
	Role        Role           `json:"role"`
	Affinity    float64        `json:"affinity"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

// PlatformLink connects an internal user to a platform identity.
type PlatformLink struct {
	ID             string    `json:"id"`
	UserID         string    `json:"user_id"`
	Platform       string    `json:"platform"`
	PlatformUserID string    `json:"platform_user_id"`
	PlatformName   string    `json:"platform_name"`
	CreatedAt      time.Time `json:"created_at"`
}

// AffinityEvent records a single affinity change from consolidation.
type AffinityEvent struct {
	ID             string    `json:"id"`
	UserID         string    `json:"user_id"`
	Delta          float64   `json:"delta"`
	Reason         string    `json:"reason"`
	InteractionIDs []string  `json:"interaction_ids,omitempty"`
	GroupStart     time.Time `json:"group_start"`
	GroupEnd       time.Time `json:"group_end"`
	CreatedAt      time.Time `json:"created_at"`
}

// Store is the user storage interface.
type Store interface {
	// Resolve looks up an internal user by platform + platform_user_id.
	// If the user does not exist, it auto-creates one and links them.
	// CLI platform users are created with RoleOwner.
	Resolve(ctx context.Context, platform, platformUserID, platformName string) (*User, error)

	// Get returns a user by internal ID.
	Get(ctx context.Context, id string) (*User, error)

	// UpdateDisplayName changes the user's nickname.
	UpdateDisplayName(ctx context.Context, userID, displayName string) error

	// UpdateAffinity atomically applies an affinity delta and records the event.
	UpdateAffinity(ctx context.Context, evt *AffinityEvent) error

	// GetAffinity returns recent affinity events for a user.
	GetAffinity(ctx context.Context, userID string, limit int) ([]AffinityEvent, error)

	// Close releases resources.
	Close() error
}
