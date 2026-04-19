package discord

import "testing"

func TestSplitMessage(t *testing.T) {
	t.Run("short message stays as single chunk", func(t *testing.T) {
		chunks := splitMessage("hello", 2000)
		if len(chunks) != 1 {
			t.Fatalf("expected 1 chunk, got %d", len(chunks))
		}
		if chunks[0] != "hello" {
			t.Errorf("chunk: got %q", chunks[0])
		}
	})

	t.Run("exact limit", func(t *testing.T) {
		msg := ""
		for i := 0; i < 2000; i++ {
			msg += "a"
		}
		chunks := splitMessage(msg, 2000)
		if len(chunks) != 1 {
			t.Errorf("expected 1 chunk for exact limit, got %d", len(chunks))
		}
	})

	t.Run("over limit splits at newline", func(t *testing.T) {
		long := "line1\nline2\nline3\nline4"
		chunks := splitMessage(long, 12)
		if len(chunks) < 2 {
			t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
		}

		for i, c := range chunks {
			if len(c) > 12 {
				t.Errorf("chunk %d exceeds maxLen: len=%d", i, len(c))
			}
		}
	})

	t.Run("zero maxLen defaults to 2000", func(t *testing.T) {
		chunks := splitMessage("test", 0)
		if len(chunks) != 1 {
			t.Errorf("expected 1 chunk with zero maxLen, got %d", len(chunks))
		}
	})

	t.Run("long message without newlines", func(t *testing.T) {
		msg := "abcdefghij"
		chunks := splitMessage(msg, 3)
		total := ""
		for _, c := range chunks {
			total += c
		}
		if total != msg {
			t.Errorf("reconstructed: got %q, want %q", total, msg)
		}
	})
}
