package textutil

import "testing"

func TestTruncateRunes(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxRunes int
		want     string
	}{
		{"empty string", "", 10, ""},
		{"within limit ASCII", "hello", 10, "hello"},
		{"exact limit", "hello", 5, "hello"},
		{"exceeds limit ASCII", "hello world", 5, "hello..."},
		{"CJK characters within limit", "こんにちは", 5, "こんにちは"},
		{"CJK characters exceeds limit", "こんにちは世界", 5, "こんにちは..."},
		{"zero max runes", "hello", 0, "..."},
		{"mixed ASCII and CJK", "hello世界", 5, "hello..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TruncateRunes(tt.input, tt.maxRunes)
			if got != tt.want {
				t.Errorf("TruncateRunes(%q, %d) = %q, want %q", tt.input, tt.maxRunes, got, tt.want)
			}
		})
	}
}

func TestStripCodeFence(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"no fence", "hello world", "hello world"},
		{"plain fence", "```\nhello\n```", "hello"},
		{"json fence", "```json\n{\"key\": \"value\"}\n```", "{\"key\": \"value\"}"},
		{"fence with whitespace", "  ```\nhello\n```  ", "hello"},
		{"only opening fence", "```\nhello", "hello"},
		{"only closing fence", "hello\n```", "hello"},
		{"empty input", "", ""},
		{"nested fences", "```\n```inner```\n```", "```inner```"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StripCodeFence(tt.input)
			if got != tt.want {
				t.Errorf("StripCodeFence(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{"empty string", "", 0},
		{"ASCII only", "hello", 1},
		{"ASCII sentence", "hello world, this is a test", 7},
		{"CJK characters", "漢字", 3},
		{"hiragana", "あいうえお", 5},
		{"katakana", "カタカナ", 4},
		{"mixed ASCII and CJK", "hello世界", 4},
		{"fullwidth punctuation", "！？", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EstimateTokens(tt.input)
			if got != tt.want {
				t.Errorf("EstimateTokens(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}
