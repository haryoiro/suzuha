package control

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/go-faster/jx"
	"github.com/haryoiro/suzuha/internal/adapter/tts"
	"github.com/haryoiro/suzuha/internal/channel/discord"
	"github.com/haryoiro/suzuha/internal/api/control/gen"
	"github.com/haryoiro/suzuha/internal/config"
	"github.com/haryoiro/suzuha/internal/capability/voice"
	"github.com/samber/do/v2"
)

// VoicevoxHandler は VOICEVOX グループ (speakers / speaker) を実装する。
type VoicevoxHandler struct {
	client    *tts.VoicevoxClient  // nil: voicevox 未設定
	cfg       *config.TTSProvider  // 話者 ID 変更を config に反映
	pipeline  *voice.Pipeline      // runtime の話者切り替え用 (nil 可)
}

// NewVoicevoxHandler は DI injector から voicevox 関連依存を組み立てる。
// 設定に voicevox エントリがない場合も構築は成功し、endpoint 側で 503 を返す。
func NewVoicevoxHandler(i do.Injector) (gen.VoicevoxHandler, error) {
	cfg := do.MustInvoke[*config.Config](i)

	h := &VoicevoxHandler{}
	for idx := range cfg.Voice.TTS {
		if cfg.Voice.TTS[idx].Provider == "voicevox" {
			h.cfg = &cfg.Voice.TTS[idx]
			if h.cfg.URL != "" {
				h.client = tts.NewVoicevox(h.cfg.URL, h.cfg.SpeakerID)
			}
			break
		}
	}
	if dc, err := do.Invoke[*discord.Chat](i); err == nil && dc != nil {
		h.pipeline = dc.VoicePipeline()
	}
	return h, nil
}

// VoicevoxSpeakers implements GET /internal/voicevox/speakers.
func (h *VoicevoxHandler) VoicevoxSpeakers(ctx context.Context) ([]gen.VoicevoxSpeakersOKItem, error) {
	if h.client == nil {
		return nil, fmt.Errorf("voicevox not configured")
	}
	raw, err := h.client.ListSpeakers(ctx)
	if err != nil {
		return nil, err
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	out := make([]gen.VoicevoxSpeakersOKItem, len(items))
	for i, item := range items {
		m := make(gen.VoicevoxSpeakersOKItem, len(item))
		for k, v := range item {
			m[k] = jx.Raw(v)
		}
		out[i] = m
	}
	return out, nil
}

// VoicevoxGetSpeaker implements GET /internal/voicevox/speaker.
func (h *VoicevoxHandler) VoicevoxGetSpeaker(ctx context.Context) (*gen.VoicevoxSpeaker, error) {
	if h.cfg == nil {
		return nil, fmt.Errorf("voicevox not configured")
	}
	return &gen.VoicevoxSpeaker{SpeakerID: int32(h.cfg.SpeakerID)}, nil
}

// VoicevoxSetSpeaker implements PUT /internal/voicevox/speaker.
func (h *VoicevoxHandler) VoicevoxSetSpeaker(ctx context.Context, req *gen.SetSpeakerRequest) (*gen.OkResponse, error) {
	if h.cfg == nil {
		return nil, fmt.Errorf("voicevox not configured")
	}
	id := int(req.SpeakerID)
	h.cfg.SpeakerID = id
	if h.pipeline != nil {
		h.pipeline.SetSpeakerID(id)
	}
	return &gen.OkResponse{Ok: true}, nil
}
