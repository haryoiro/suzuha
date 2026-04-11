package textutil

import "strings"

// TruncateRunes は文字列を指定したルーン数で切り詰め、超過時は末尾に "..." を付与する。
func TruncateRunes(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "..."
}

// StripCodeFence はコードブロックのフェンス記号を除去する。
func StripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```json") {
		s = strings.TrimPrefix(s, "```json")
	} else if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```")
	}
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}

// EstimateTokens は文字種ごとの重みを用いて文字列のトークン数を推定する。
func EstimateTokens(s string) int {
	var total float64
	for _, r := range s {
		switch {
		case r <= 0x007F:
			total += 0.25
		case r >= 0x4E00 && r <= 0x9FFF,
			r >= 0x3400 && r <= 0x4DBF,
			r >= 0xF900 && r <= 0xFAFF,
			r >= 0x20000 && r <= 0x2A6DF:
			total += 1.5
		case r >= 0x3040 && r <= 0x309F:
			total += 1.0
		case r >= 0x30A0 && r <= 0x30FF:
			total += 1.0
		case r >= 0x3000 && r <= 0x303F,
			r >= 0xFF00 && r <= 0xFFEF:
			total += 1.0
		default:
			total += 1.5
		}
	}
	return int(total + 0.5)
}
