// Package tts は Text-to-Speech の契約を定義する。
// 実装は adapter/tts/ (voicevox / sbv2)、consumer は capability/voice や
// channel/device 等。
package tts

import "context"

// Synthesizer はテキストを PCM 音声に合成する。
type Synthesizer interface {
	// Synthesize は text を 16-bit LE / mono PCM に合成し、出力サンプルレートと共に返す。
	Synthesize(ctx context.Context, text string) (pcm []byte, sampleRate int, err error)
}
