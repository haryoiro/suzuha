package tasks

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Feed represents an RSS feed subscription.
type Feed struct {
	ID        string
	Name      string
	URL       string
	ChannelID string
	CreatedBy string
	Enabled   bool
	LastPolled *time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Item represents a fetched RSS article.
type Item struct {
	ID          string
	FeedID      string
	GUID        string
	Title       string
	Link        string
	Description string
	PublishedAt *time.Time
	MemoryID    string
	Notified    bool
	CreatedAt   time.Time
}

// FeedStore provides CRUD operations for rss_feeds and rss_items tables.
type FeedStore struct {
	db *sql.DB
}

// NewFeedStore creates a FeedStore.
func NewFeedStore(db *sql.DB) *FeedStore {
	return &FeedStore{db: db}
}

// Setup creates the rss_feeds and rss_items tables if they don't exist.
func (s *FeedStore) Setup(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS rss_feeds (
			id           TEXT PRIMARY KEY,
			name         TEXT NOT NULL,
			url          TEXT NOT NULL UNIQUE,
			channel_id   TEXT NOT NULL,
			created_by   TEXT,
			enabled      INTEGER NOT NULL DEFAULT 1,
			last_polled  DATETIME,
			created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`)
	if err != nil {
		return fmt.Errorf("create rss_feeds: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS rss_items (
			id           TEXT PRIMARY KEY,
			feed_id      TEXT NOT NULL REFERENCES rss_feeds(id) ON DELETE CASCADE,
			guid         TEXT NOT NULL,
			title        TEXT NOT NULL,
			link         TEXT NOT NULL,
			description  TEXT,
			published_at DATETIME,
			memory_id    TEXT,
			notified     INTEGER NOT NULL DEFAULT 0,
			created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(feed_id, guid)
		)`)
	if err != nil {
		return fmt.Errorf("create rss_items: %w", err)
	}

	return nil
}

// AddFeed inserts a new RSS feed.
func (s *FeedStore) AddFeed(ctx context.Context, f *Feed) error {
	if f.ID == "" {
		f.ID = uuid.NewString()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO rss_feeds (id, name, url, channel_id, created_by) VALUES (?, ?, ?, ?, ?)`,
		f.ID, f.Name, f.URL, f.ChannelID, f.CreatedBy)
	return err
}

// RemoveFeed deletes a feed by ID or URL.
func (s *FeedStore) RemoveFeed(ctx context.Context, idOrURL string) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM rss_feeds WHERE id = ? OR url = ? OR name = ?`,
		idOrURL, idOrURL, idOrURL)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("feed not found: %s", idOrURL)
	}
	return nil
}

// ListEnabled returns all enabled feeds.
func (s *FeedStore) ListEnabled(ctx context.Context) ([]Feed, error) {
	return s.queryFeeds(ctx, `SELECT id, name, url, channel_id, created_by, enabled, last_polled, created_at, updated_at FROM rss_feeds WHERE enabled = 1`)
}

// ListAll returns all feeds.
func (s *FeedStore) ListAll(ctx context.Context) ([]Feed, error) {
	return s.queryFeeds(ctx, `SELECT id, name, url, channel_id, created_by, enabled, last_polled, created_at, updated_at FROM rss_feeds ORDER BY created_at DESC`)
}

func (s *FeedStore) queryFeeds(ctx context.Context, query string, args ...any) ([]Feed, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var feeds []Feed
	for rows.Next() {
		var f Feed
		var lastPolled sql.NullTime
		var createdBy sql.NullString
		if err := rows.Scan(&f.ID, &f.Name, &f.URL, &f.ChannelID, &createdBy, &f.Enabled, &lastPolled, &f.CreatedAt, &f.UpdatedAt); err != nil {
			return nil, err
		}
		if lastPolled.Valid {
			f.LastPolled = &lastPolled.Time
		}
		if createdBy.Valid {
			f.CreatedBy = createdBy.String
		}
		feeds = append(feeds, f)
	}
	return feeds, rows.Err()
}

// UpdateLastPolled sets the last_polled timestamp for a feed.
func (s *FeedStore) UpdateLastPolled(ctx context.Context, feedID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE rss_feeds SET last_polled = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		feedID)
	return err
}

// ItemExists checks if an item with the given feed_id and guid already exists.
func (s *FeedStore) ItemExists(ctx context.Context, feedID, guid string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM rss_items WHERE feed_id = ? AND guid = ?`,
		feedID, guid).Scan(&count)
	return count > 0, err
}

// InsertItem adds a new RSS item.
func (s *FeedStore) InsertItem(ctx context.Context, item *Item) error {
	if item.ID == "" {
		item.ID = uuid.NewString()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO rss_items (id, feed_id, guid, title, link, description, published_at, memory_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		item.ID, item.FeedID, item.GUID, item.Title, item.Link, item.Description, item.PublishedAt, item.MemoryID)
	return err
}

// UpdateItemMemoryID sets the memory_id for an item.
func (s *FeedStore) UpdateItemMemoryID(ctx context.Context, itemID, memoryID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE rss_items SET memory_id = ? WHERE id = ?`, memoryID, itemID)
	return err
}

// UnnotifiedItems returns items that haven't been notified yet for the given feed IDs.
func (s *FeedStore) UnnotifiedItems(ctx context.Context, feedIDs []string) ([]Item, error) {
	if len(feedIDs) == 0 {
		return nil, nil
	}
	placeholders := strings.Repeat("?,", len(feedIDs))
	placeholders = placeholders[:len(placeholders)-1]
	query := fmt.Sprintf(
		`SELECT id, feed_id, guid, title, link, description, published_at, memory_id, notified, created_at
		 FROM rss_items WHERE feed_id IN (%s) AND notified = 0
		 ORDER BY created_at DESC`, placeholders)
	args := make([]any, len(feedIDs))
	for i, id := range feedIDs {
		args[i] = id
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []Item
	for rows.Next() {
		var it Item
		var pubAt sql.NullTime
		var memID sql.NullString
		if err := rows.Scan(&it.ID, &it.FeedID, &it.GUID, &it.Title, &it.Link, &it.Description,
			&pubAt, &memID, &it.Notified, &it.CreatedAt); err != nil {
			return nil, err
		}
		if pubAt.Valid {
			it.PublishedAt = &pubAt.Time
		}
		if memID.Valid {
			it.MemoryID = memID.String
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// MarkNotified marks items as notified.
func (s *FeedStore) MarkNotified(ctx context.Context, itemIDs []string) error {
	if len(itemIDs) == 0 {
		return nil
	}
	placeholders := strings.Repeat("?,", len(itemIDs))
	placeholders = placeholders[:len(placeholders)-1]
	query := fmt.Sprintf(`UPDATE rss_items SET notified = 1 WHERE id IN (%s)`, placeholders)
	args := make([]any, len(itemIDs))
	for i, id := range itemIDs {
		args[i] = id
	}
	_, err := s.db.ExecContext(ctx, query, args...)
	return err
}
