package textutil

import "testing"

func TestSentenceBuffer_JapanesePunctuation(t *testing.T) {
	var got []string
	sb := NewSentenceBuffer(0, func(s string) { got = append(got, s) })

	sb.Write("こんにちは。元気？はい！")
	sb.Flush()

	want := []string{"こんにちは。", "元気？", "はい！"}
	if len(got) != len(want) {
		t.Fatalf("got %d sentences, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sentence[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSentenceBuffer_IncrementalTokens(t *testing.T) {
	var got []string
	sb := NewSentenceBuffer(0, func(s string) { got = append(got, s) })

	sb.Write("それで")
	sb.Write("もいいよ。")
	sb.Write("部屋の")
	sb.Write("ゴミまとめて")

	if len(got) != 1 {
		t.Fatalf("mid-stream: got %d sentences, want 1: %v", len(got), got)
	}
	if got[0] != "それでもいいよ。" {
		t.Errorf("sentence[0] = %q, want %q", got[0], "それでもいいよ。")
	}

	sb.Flush()
	if len(got) != 2 {
		t.Fatalf("after flush: got %d sentences, want 2: %v", len(got), got)
	}
	if got[1] != "部屋のゴミまとめて" {
		t.Errorf("sentence[1] = %q, want %q", got[1], "部屋のゴミまとめて")
	}
}

func TestSentenceBuffer_MaxLen(t *testing.T) {
	var got []string
	sb := NewSentenceBuffer(10, func(s string) { got = append(got, s) })

	sb.Write("あいうえおかきくけこさしすせそ")
	sb.Flush()

	if len(got) < 2 {
		t.Fatalf("expected at least 2 segments with maxLen=10, got %d: %v", len(got), got)
	}
}

func TestSentenceBuffer_Newline(t *testing.T) {
	var got []string
	sb := NewSentenceBuffer(0, func(s string) { got = append(got, s) })

	sb.Write("一行目\n二行目")
	sb.Flush()

	want := []string{"一行目", "二行目"}
	if len(got) != len(want) {
		t.Fatalf("got %d sentences, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sentence[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSentenceBuffer_EmptyWrite(t *testing.T) {
	var got []string
	sb := NewSentenceBuffer(0, func(s string) { got = append(got, s) })

	sb.Write("")
	sb.Write("  ")
	sb.Flush()

	if len(got) != 0 {
		t.Fatalf("expected no output for empty/whitespace, got %v", got)
	}
}

func TestSentenceBuffer_ConsecutivePunctuation(t *testing.T) {
	var got []string
	sb := NewSentenceBuffer(0, func(s string) { got = append(got, s) })

	sb.Write("え？？本当！？")
	sb.Flush()

	for _, s := range got {
		if s == "" {
			t.Error("empty sentence should not be emitted")
		}
	}
}
