package di

import (
	"context"
	"time"

	"github.com/haryoiro/suzuha/internal/adapter/transcript"
	"github.com/haryoiro/suzuha/internal/adapter/twitter"
	"github.com/haryoiro/suzuha/internal/agent"
	"github.com/haryoiro/suzuha/internal/agent/prompt"
	"github.com/haryoiro/suzuha/internal/capability/memory/summarize"
)

// videoMetaAdapter は transcript.MetadataFetcher を agent.VideoMetadataFetcher に適合させる。
type videoMetaAdapter struct {
	inner transcript.MetadataFetcher
}

func (a *videoMetaAdapter) Supports(url string) bool {
	return a.inner.Supports(url)
}

func (a *videoMetaAdapter) FetchMetadata(ctx context.Context, url string) (agent.VideoInfo, error) {
	info, err := a.inner.FetchMetadata(ctx, url)
	if err != nil {
		return agent.VideoInfo{}, err
	}
	return agent.VideoInfo{Title: info.Title, Duration: info.Duration}, nil
}

// tweetFetcherAdapter は twitter.Fetcher を agent.TweetFetcher に適合させる。
type tweetFetcherAdapter struct {
	inner twitter.Fetcher
}

func (a *tweetFetcherAdapter) Supports(url string) bool {
	return a.inner.Supports(url)
}

func (a *tweetFetcherAdapter) Fetch(ctx context.Context, url string) (*agent.TweetPreview, error) {
	t, err := a.inner.Fetch(ctx, url)
	if err != nil {
		return nil, err
	}
	return &agent.TweetPreview{AuthorID: t.AuthorID, Text: t.Text}, nil
}

// diaryReaderAdapter は summarize.Store を prompt.DiaryReader に適合させる。
type diaryReaderAdapter struct {
	store *summarize.Store
}

func (a *diaryReaderAdapter) ListByKind(ctx context.Context, kind string, since time.Time, limit int) ([]prompt.DiaryEntry, error) {
	entries, err := a.store.ListByKind(ctx, kind, since, limit)
	if err != nil {
		return nil, err
	}
	result := make([]prompt.DiaryEntry, len(entries))
	for i, e := range entries {
		result[i] = prompt.DiaryEntry{PeriodStart: e.PeriodStart, Content: e.Content}
	}
	return result, nil
}
