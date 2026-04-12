package voice

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/disgoorg/disgo/discord"
	botgateway "github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/disgo/voice"
	"github.com/disgoorg/godave/golibdave"
	"github.com/disgoorg/snowflake/v2"
	"github.com/hraban/opus"
)

// Session manages a single Discord voice channel connection using disgo's
// voice.Conn (DAVE E2EE support) while bridging events from discordgo's gateway.
type Session struct {
	discordSession *discordgo.Session
	guildID        string
	channelID      string
	logger         *slog.Logger

	conn voice.Conn

	// onSpeech is called when a user finishes speaking (after VAD).
	onSpeech func(userID string, pcm []byte)

	// Per-user VAD instances.
	vadMu sync.Mutex
	vads  map[string]*VAD

	// Opus decoder (shared, Discord sends mono 48kHz).
	decoderMu sync.Mutex
	decoder   *opus.Decoder

	// Opus encoder for sending audio (48kHz stereo).
	encoderMu sync.Mutex
	encoder   *opus.Encoder
	speaking  bool // BeginSpeaking で true、EndSpeaking で false

	// discordgo event handler cleanup functions.
	cleanupHandlers []func()
}

// NewSession creates a voice session for the given guild/channel.
func NewSession(s *discordgo.Session, guildID, channelID string, logger *slog.Logger) *Session {
	return &Session{
		discordSession: s,
		guildID:        guildID,
		channelID:      channelID,
		logger:         logger,
		vads:           make(map[string]*VAD),
	}
}

// OnSpeech sets the callback invoked when a complete speech segment is detected.
// The callback receives the speaker's Discord user ID and raw PCM audio (16-bit LE, mono, 48kHz).
func (s *Session) OnSpeech(fn func(userID string, pcm []byte)) {
	s.onSpeech = fn
}

// Join connects to the voice channel using disgo's DAVE-capable voice connection.
func (s *Session) Join(ctx context.Context) error {
	// Create Opus decoder (48kHz, mono).
	dec, err := opus.NewDecoder(48000, 1)
	if err != nil {
		return fmt.Errorf("voice: Opusデコーダ作成失敗: %w", err)
	}
	s.decoder = dec

	// Create Opus encoder (48kHz, stereo) for sending audio.
	enc, err := opus.NewEncoder(48000, 2, opus.AppVoIP)
	if err != nil {
		return fmt.Errorf("voice: Opusエンコーダ作成失敗: %w", err)
	}
	s.encoder = enc

	guildSF, err := snowflake.Parse(s.guildID)
	if err != nil {
		return fmt.Errorf("voice: guild_id パース失敗: %w", err)
	}
	channelSF, err := snowflake.Parse(s.channelID)
	if err != nil {
		return fmt.Errorf("voice: channel_id パース失敗: %w", err)
	}
	userSF, err := snowflake.Parse(s.discordSession.State.User.ID)
	if err != nil {
		return fmt.Errorf("voice: user_id パース失敗: %w", err)
	}

	// voiceStateUpdateFunc sends a voice state update via discordgo's gateway.
	voiceStateUpdateFunc := func(ctx context.Context, guildID snowflake.ID, channelID *snowflake.ID, selfMute bool, selfDeaf bool) error {
		var chID string
		if channelID != nil {
			chID = channelID.String()
		}
		s.logger.Debug("VC接続の状態を更新中", "guild", guildID, "channel", chID)
		return s.discordSession.ChannelVoiceJoinManual(guildID.String(), chID, selfMute, selfDeaf)
	}

	// Create the disgo voice connection with DAVE support.
	s.conn = voice.NewConn(guildSF, userSF, voiceStateUpdateFunc, func() {}, // removeFunc
		voice.WithConnLogger(s.logger),
		voice.WithConnDaveSessionCreateFunc(golibdave.NewSession),
		voice.WithConnAudioReceiverCreateFunc(NewTolerantAudioReceiver),
	)

	// Register discordgo handlers to forward voice events to disgo's conn.
	removeVSU := s.discordSession.AddHandler(func(_ *discordgo.Session, vs *discordgo.VoiceStateUpdate) {
		if vs.GuildID != s.guildID {
			return
		}
		s.conn.HandleVoiceStateUpdate(convertVoiceStateUpdate(vs))
	})
	removeVSrv := s.discordSession.AddHandler(func(_ *discordgo.Session, vs *discordgo.VoiceServerUpdate) {
		if vs.GuildID != s.guildID {
			return
		}
		s.conn.HandleVoiceServerUpdate(convertVoiceServerUpdate(vs))
	})
	s.cleanupHandlers = append(s.cleanupHandlers, removeVSU, removeVSrv)

	// Open the voice connection (triggers voice state update and waits for ready).
	openCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := s.conn.Open(openCtx, channelSF, false, false); err != nil {
		s.cleanup()
		return fmt.Errorf("voice: VC参加失敗: %w", err)
	}

	// Set up opus frame receiver AFTER connection is open (UDP must be ready).
	s.conn.SetOpusFrameReceiver(&opusReceiver{session: s})

	s.logger.Info("ボイスチャンネルに入った", "guild", s.guildID, "channel", s.channelID)
	return nil
}

// Leave disconnects from the voice channel.
func (s *Session) Leave() error {
	if s.conn != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.conn.Close(ctx)
	}
	s.cleanup()
	return nil
}

func (s *Session) cleanup() {
	for _, fn := range s.cleanupHandlers {
		fn()
	}
	s.cleanupHandlers = nil
}

// opusFrameSamples は 20ms フレームのサンプル数 (48kHz)。
const opusFrameSamples = 960

// opusFrameBytes は 48kHz ステレオ 20ms フレームのバイト数。
const opusFrameBytes = opusFrameSamples * 2 * 2 // stereo, 16-bit

// silenceFrame は Opus silence パケット。
var silenceFrame = []byte{0xF8, 0xFF, 0xFE}

// BeginSpeaking は DAVE 鍵待機・speaking フラグ設定・無音プリアンブルを行う。
// encoderMu を取得し保持する。必ず EndSpeaking で解放すること。
func (s *Session) BeginSpeaking() error {
	if s.conn == nil {
		return fmt.Errorf("voice: VCに接続されていません")
	}

	s.encoderMu.Lock()

	if err := s.waitForDAVEReady(); err != nil {
		s.encoderMu.Unlock()
		return err
	}

	// DAVE epoch convergence 待ち: 他クライアントが MLS epoch を処理する時間を確保。
	time.Sleep(2 * time.Second)

	ctx := context.Background()
	if err := s.conn.SetSpeaking(ctx, voice.SpeakingFlagMicrophone); err != nil {
		s.logger.Warn("話し始めの合図に失敗", "error", err)
	}

	udp := s.conn.UDP()
	for range 15 {
		if _, err := udp.Write(silenceFrame); err != nil {
			s.logger.Warn("無音の送信に失敗", "error", err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	s.speaking = true
	return nil
}

// SendPCMChunk は BeginSpeaking 済みの状態で PCM チャンクを Opus エンコードして送信する。
// PCM は 16-bit LE, stereo, 48kHz。BeginSpeaking が先に呼ばれていること。
func (s *Session) SendPCMChunk(pcm []byte) error {
	if !s.speaking {
		return fmt.Errorf("voice: BeginSpeaking が呼ばれていません")
	}
	udp := s.conn.UDP()
	ctx := context.Background()

	totalFrames := len(pcm) / opusFrameBytes
	s.logger.Debug("音声を送り始める", "pcm_bytes", len(pcm),
		"frames", totalFrames, "duration_ms", totalFrames*20)

	var sentFrames int
	for offset := 0; offset+opusFrameBytes <= len(pcm); offset += opusFrameBytes {
		frame := make([]int16, opusFrameSamples*2) // stereo
		for i := range frame {
			frame[i] = int16(binary.LittleEndian.Uint16(pcm[offset+i*2:]))
		}

		opusBuf := make([]byte, 1000)
		n, err := s.encoder.Encode(frame, opusBuf)
		if err != nil {
			return fmt.Errorf("voice: Opusエンコード失敗: %w", err)
		}

		if _, err := udp.Write(opusBuf[:n]); err != nil {
			if strings.Contains(err.Error(), "missing key ratchet") {
				s.logger.Debug("暗号鍵を待っている")
				if waitErr := s.waitForDAVEReady(); waitErr != nil {
					return waitErr
				}
				_ = s.conn.SetSpeaking(ctx, voice.SpeakingFlagMicrophone)
				if _, err := udp.Write(opusBuf[:n]); err != nil {
					return fmt.Errorf("voice: Opus送信失敗（リトライ後）: %w", err)
				}
			} else {
				return fmt.Errorf("voice: Opus送信失敗: %w", err)
			}
		}

		sentFrames++
		time.Sleep(20 * time.Millisecond)
	}

	s.logger.Debug("音声を送り終わった", "sent", sentFrames, "total", totalFrames)
	return nil
}

// EndSpeaking は trailing silence を送り、speaking フラグをクリアし、encoderMu を解放する。
// BeginSpeaking とペアで使うこと。ストリーミング利用時は defer で呼ぶこと。
func (s *Session) EndSpeaking() {
	if !s.speaking {
		return
	}
	s.speaking = false

	if s.conn == nil {
		s.encoderMu.Unlock()
		return
	}

	udp := s.conn.UDP()
	for range 5 {
		_, _ = udp.Write(silenceFrame)
		time.Sleep(20 * time.Millisecond)
	}

	ctx := context.Background()
	_ = s.conn.SetSpeaking(ctx, 0)

	s.logger.Debug("音声の送信が完了")
	s.encoderMu.Unlock()
}

// SendPCM は PCM 全体を一括エンコード・送信する。
// BeginSpeaking + SendPCMChunk + EndSpeaking のラッパー。
// PCM は 16-bit LE, stereo (2ch), 48kHz。
func (s *Session) SendPCM(pcm []byte) error {
	if err := s.BeginSpeaking(); err != nil {
		return err
	}
	defer s.EndSpeaking()
	return s.SendPCMChunk(pcm)
}

// waitForDAVEReady probes the UDP connection with a silent Opus frame to check
// if the DAVE key ratchet is ready. Retries up to 5 seconds.
func (s *Session) waitForDAVEReady() error {
	udp := s.conn.UDP()

	// 960 samples of silence encoded as a minimal Opus frame.
	silenceFrame := []byte{0xF8, 0xFF, 0xFE}

	for i := range 50 { // 50 * 200ms = 10s max
		_, err := udp.Write(silenceFrame)
		if err == nil {
			if i > 0 {
				s.logger.Info("暗号鍵の準備ができた", "waited_ms", i*200)
			}
			return nil
		}
		if !strings.Contains(err.Error(), "missing key ratchet") {
			return fmt.Errorf("voice: DAVE鍵待機中にエラー: %w", err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("voice: DAVE鍵ラチェットが10秒以内に準備できませんでした")
}

// IsConnected returns true if currently connected to a voice channel.
func (s *Session) IsConnected() bool {
	return s.conn != nil && s.conn.ChannelID() != nil
}

// opusReceiver implements voice.OpusFrameReceiver to handle incoming audio.
type opusReceiver struct {
	session *Session
}

func (r *opusReceiver) ReceiveOpusFrame(userID snowflake.ID, packet *voice.Packet) error {
	if len(packet.Opus) == 0 {
		return nil
	}

	uid := userID.String()

	// Decode Opus to PCM (mono, 48kHz).
	pcmBuf := make([]int16, 960)
	r.session.decoderMu.Lock()
	n, err := r.session.decoder.Decode(packet.Opus, pcmBuf)
	r.session.decoderMu.Unlock()
	if err != nil {
		r.session.logger.Debug("voice: Opusデコード失敗", "error", err)
		return nil
	}
	pcmBuf = pcmBuf[:n]

	// Convert int16 to bytes (16-bit LE).
	mono := int16ToBytes(pcmBuf)

	// Feed to per-user VAD.
	r.session.vadMu.Lock()
	vad, exists := r.session.vads[uid]
	if !exists {
		vad = NewVAD()
		r.session.vads[uid] = vad
	}
	r.session.vadMu.Unlock()

	result := vad.Process(mono, time.Now())
	if result.SpeechEnded && r.session.onSpeech != nil {
		r.session.logger.Info("voice: 発話検出", "user_id", uid, "audio_bytes", len(result.Audio))
		r.session.onSpeech(uid, result.Audio)
	}
	return nil
}

func (r *opusReceiver) CleanupUser(userID snowflake.ID) {
	uid := userID.String()
	r.session.vadMu.Lock()
	delete(r.session.vads, uid)
	r.session.vadMu.Unlock()
}

func (r *opusReceiver) Close() {}

// convertVoiceStateUpdate converts a discordgo VoiceStateUpdate to disgo's gateway event.
func convertVoiceStateUpdate(vs *discordgo.VoiceStateUpdate) botgateway.EventVoiceStateUpdate {
	evt := botgateway.EventVoiceStateUpdate{
		VoiceState: discord.VoiceState{
			GuildID:   mustParseSnowflake(vs.GuildID),
			UserID:    mustParseSnowflake(vs.UserID),
			SessionID: vs.SessionID,
			GuildDeaf: vs.Deaf,
			GuildMute: vs.Mute,
			SelfDeaf:  vs.SelfDeaf,
			SelfMute:  vs.SelfMute,
			SelfVideo: vs.SelfVideo,
			Suppress:  vs.Suppress,
		},
	}
	if vs.ChannelID != "" {
		chID := mustParseSnowflake(vs.ChannelID)
		evt.VoiceState.ChannelID = &chID
	}
	return evt
}

// convertVoiceServerUpdate converts a discordgo VoiceServerUpdate to disgo's gateway event.
func convertVoiceServerUpdate(vs *discordgo.VoiceServerUpdate) botgateway.EventVoiceServerUpdate {
	evt := botgateway.EventVoiceServerUpdate{
		Token:   vs.Token,
		GuildID: mustParseSnowflake(vs.GuildID),
	}
	if vs.Endpoint != "" {
		evt.Endpoint = &vs.Endpoint
	}
	return evt
}

func mustParseSnowflake(id string) snowflake.ID {
	n, _ := strconv.ParseUint(id, 10, 64)
	return snowflake.ID(n)
}

// int16ToBytes converts an int16 slice to little-endian bytes.
func int16ToBytes(samples []int16) []byte {
	buf := make([]byte, len(samples)*2)
	for i, s := range samples {
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(s))
	}
	return buf
}
