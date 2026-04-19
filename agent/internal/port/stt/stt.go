// Package stt は Speech-to-Text の契約を定義する。
// 実装は adapter/stt/ (deepgram / whisper)、consumer は capability/voice
// (Phase 8a 後半で移動予定) や channel/discord 等。
package stt

import "context"

// Transcriber は PCM 音声をテキストに変換する。
type Transcriber interface {
	// Transcribe は 16-bit LE / mono PCM を受け取りテキストを返す。
	Transcribe(ctx context.Context, pcm []byte, sampleRate int) (string, error)
}
