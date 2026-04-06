package discord

import "testing"

func TestSplitMessage(t *testing.T) {
	// Short message stays as single chunk.
	chunks := splitMessage("hello", 2000)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0] != "hello" {
		t.Errorf("chunk: got %q", chunks[0])
	}

	// Exact limit.
	exact := string(make([]byte, 2000))
	for i := range exact {
		_ = i
	}
	msg := ""
	for i := 0; i < 2000; i++ {
		msg += "a"
	}
	chunks2 := splitMessage(msg, 2000)
	if len(chunks2) != 1 {
		t.Errorf("expected 1 chunk for exact limit, got %d", len(chunks2))
	}

	// Over limit, splits at newline.
	long := "line1\nline2\nline3\nline4"
	chunks3 := splitMessage(long, 12)
	if len(chunks3) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks3))
	}

	// Verify no chunk exceeds maxLen.
	for i, c := range chunks3 {
		if len(c) > 12 {
			t.Errorf("chunk %d exceeds maxLen: len=%d", i, len(c))
		}
	}

	// Zero maxLen defaults to 2000.
	chunks4 := splitMessage("test", 0)
	if len(chunks4) != 1 {
		t.Errorf("expected 1 chunk with zero maxLen, got %d", len(chunks4))
	}
}

func TestSplitMessage_LongNoNewline(t *testing.T) {
	// Long message without newlines splits at maxLen.
	msg := "abcdefghij" // 10 chars
	chunks := splitMessage(msg, 3)
	total := ""
	for _, c := range chunks {
		total += c
	}
	if total != msg {
		t.Errorf("reconstructed: got %q, want %q", total, msg)
	}
}
