// Package vad は Voice Activity Detection の契約を定義する。
// 実装は capability/voice/vad.go。
package vad

// Result は VAD の 1 フレーム分の判定結果。
type Result struct {
	IsSpeech   bool    // 発話中かどうか
	Confidence float64 // 0.0〜1.0 の信頼度 (実装依存)
}

// Detector は PCM を受けて発話区間を判定する。
type Detector interface {
	// Feed は 1 フレーム分の PCM を処理し、判定結果を返す。
	// frame は 16-bit LE / mono、長さは実装ごとに定められたフレーム長。
	Feed(frame []byte) Result

	// Reset は内部状態 (平滑化バッファ等) をクリアする。
	Reset()
}
