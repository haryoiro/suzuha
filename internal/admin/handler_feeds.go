package admin

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/haryoiro/suzuha/internal/admin/api"
	"github.com/haryoiro/suzuha/internal/rss"
)

func feedToAPI(f rss.Feed) api.Feed {
	id, _ := strconv.Atoi(f.ID)
	af := api.Feed{
		ID:        int32(id),
		Name:      f.Name,
		URL:       f.URL,
		ChannelID: f.ChannelID,
		Enabled:   f.Enabled,
		CreatedAt: f.CreatedAt.Format(time.RFC3339),
		UpdatedAt: f.UpdatedAt.Format(time.RFC3339),
	}
	if f.LastPolled != nil {
		af.LastFetchedAt = api.NewOptString(f.LastPolled.Format(time.RFC3339))
	}
	return af
}

func (h *AdminHandler) feedStore() *rss.FeedStore {
	return rss.NewFeedStore(h.db)
}

func (h *AdminHandler) FeedsList(ctx context.Context) (*api.FeedsListOK, error) {
	feeds, err := h.feedStore().ListAll(ctx)
	if err != nil {
		h.logger.Error("フィード一覧の取得に失敗", "error", err.Error())
		return nil, fmt.Errorf("internal error")
	}
	data := make([]api.Feed, len(feeds))
	for i, f := range feeds {
		data[i] = feedToAPI(f)
	}
	return &api.FeedsListOK{Data: data, Total: int32(len(feeds))}, nil
}

func (h *AdminHandler) FeedsCreate(ctx context.Context, req *api.CreateFeedRequest) (*api.FeedsCreateCreated, error) {
	feed := &rss.Feed{
		Name:      req.Name,
		URL:       req.URL,
		ChannelID: req.ChannelID,
		Enabled:   true,
	}
	if err := h.feedStore().AddFeed(ctx, feed); err != nil {
		h.logger.Error("フィードの作成に失敗", "error", err.Error())
		return nil, fmt.Errorf("internal error")
	}
	return &api.FeedsCreateCreated{Data: feedToAPI(*feed)}, nil
}

func (h *AdminHandler) FeedsGet(ctx context.Context, params api.FeedsGetParams) (*api.FeedsGetOK, error) {
	feed, err := h.feedStore().GetFeed(ctx, fmt.Sprintf("%d", params.ID))
	if err != nil {
		return nil, fmt.Errorf("not found")
	}
	return &api.FeedsGetOK{Data: feedToAPI(*feed)}, nil
}

func (h *AdminHandler) FeedsUpdate(ctx context.Context, req *api.UpdateFeedRequest, params api.FeedsUpdateParams) (*api.FeedsUpdateOK, error) {
	store := h.feedStore()
	feed, err := store.GetFeed(ctx, fmt.Sprintf("%d", params.ID))
	if err != nil {
		return nil, fmt.Errorf("not found")
	}

	if v, ok := req.Name.Get(); ok {
		feed.Name = v
	}
	if v, ok := req.URL.Get(); ok {
		feed.URL = v
	}
	if v, ok := req.ChannelID.Get(); ok {
		feed.ChannelID = v
	}
	if v, ok := req.Enabled.Get(); ok {
		feed.Enabled = v
	}

	if err := store.UpdateFeed(ctx, feed); err != nil {
		h.logger.Error("フィードの更新に失敗", "error", err.Error())
		return nil, fmt.Errorf("internal error")
	}
	return &api.FeedsUpdateOK{Data: feedToAPI(*feed)}, nil
}

func (h *AdminHandler) FeedsDelete(ctx context.Context, params api.FeedsDeleteParams) error {
	if err := h.feedStore().RemoveFeed(ctx, fmt.Sprintf("%d", params.ID)); err != nil {
		h.logger.Error("フィードの削除に失敗", "error", err.Error())
		return fmt.Errorf("not found")
	}
	return nil
}

func (h *AdminHandler) FeedsItems(ctx context.Context, params api.FeedsItemsParams) (*api.FeedsItemsOK, error) {
	offset := int(params.Offset.Or(0))
	limit := int(params.Limit.Or(20))

	items, total, err := h.feedStore().ListItems(ctx, fmt.Sprintf("%d", params.ID), offset, limit)
	if err != nil {
		h.logger.Error("フィードアイテム一覧の取得に失敗", "error", err.Error())
		return nil, fmt.Errorf("internal error")
	}

	data := make([]api.FeedItem, len(items))
	for i, item := range items {
		itemID, _ := strconv.Atoi(item.ID)
		feedID, _ := strconv.Atoi(item.FeedID)
		fi := api.FeedItem{
			ID:        int32(itemID),
			FeedID:    int32(feedID),
			Title:     item.Title,
			URL:       item.Link,
			CreatedAt: item.CreatedAt.Format(time.RFC3339),
		}
		if item.Description != "" {
			fi.Content = api.NewOptString(item.Description)
		}
		if item.PublishedAt != nil {
			fi.PublishedAt = api.NewOptString(item.PublishedAt.Format(time.RFC3339))
		}
		data[i] = fi
	}
	return &api.FeedsItemsOK{Data: data, Total: int32(total)}, nil
}

func (h *AdminHandler) FeedsStats(ctx context.Context) (*api.FeedStats, error) {
	total, enabled, err := h.feedStore().CountFeeds(ctx)
	if err != nil {
		h.logger.Error("フィード統計の取得に失敗", "error", err.Error())
		return nil, fmt.Errorf("internal error")
	}
	return &api.FeedStats{Total: int32(total), Enabled: int32(enabled)}, nil
}
