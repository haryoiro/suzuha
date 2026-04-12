package textutil

import "strings"

// SentenceBuffer はテキストトークンを蓄積し、文単位で flush する。
// LLM のストリーミング出力を TTS に渡す粒度に分割するために使用する。
type SentenceBuffer struct {
	buf     strings.Builder
	onFlush func(sentence string)
	// maxLen は強制 flush する文字数 (ルーン数)。
	// 句読点のない長文が TTS を長時間ブロックするのを防ぐ。
	maxLen int
}

// sentenceBreaks は日本語の文区切り文字。
const sentenceBreaks = "。！？!?"

// NewSentenceBuffer は SentenceBuffer を作成する。
// onFlush は文が完成するたびに呼ばれる。空文字列では呼ばれない。
// maxLen は強制 flush する最大ルーン数 (0 以下で無制限)。
func NewSentenceBuffer(maxLen int, onFlush func(string)) *SentenceBuffer {
	return &SentenceBuffer{
		onFlush: onFlush,
		maxLen:  maxLen,
	}
}

// Write はテキストを追加し、文が完成していれば flush する。
func (sb *SentenceBuffer) Write(text string) {
	for _, r := range text {
		sb.buf.WriteRune(r)

		if strings.ContainsRune(sentenceBreaks, r) || r == '\n' {
			sb.flush()
			continue
		}

		// 強制 flush: maxLen を超えたら区切りがなくても出力する。
		if sb.maxLen > 0 && sentenceRuneCount(&sb.buf) >= sb.maxLen {
			sb.flush()
		}
	}
}

// Flush はバッファに残っているテキストを強制的に出力する。
// ストリーム終了時に呼ぶこと。
func (sb *SentenceBuffer) Flush() {
	sb.flush()
}

func (sb *SentenceBuffer) flush() {
	s := strings.TrimSpace(sb.buf.String())
	sb.buf.Reset()
	if s != "" && sb.onFlush != nil {
		sb.onFlush(s)
	}
}

// sentenceRuneCount は Builder 内のルーン数を返す。
func sentenceRuneCount(b *strings.Builder) int {
	n := 0
	for range b.String() {
		n++
	}
	return n
}
