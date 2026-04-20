package llm

import (
	"testing"

	"github.com/mozilla-ai/any-llm-go/providers"
)

func toolCallDelta(id, name, args string) providers.ToolCall {
	return providers.ToolCall{
		ID:   id,
		Type: "function",
		Function: providers.FunctionCall{
			Name:      name,
			Arguments: args,
		},
	}
}

func TestFilterThinkTags_Simple(t *testing.T) {
	a := &streamAccumulator{}
	got := a.filterThinkTags("<think>reasoning</think>hello")
	if got != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
	if a.reasoning.String() != "reasoning" {
		t.Errorf("reasoning = %q, want %q", a.reasoning.String(), "reasoning")
	}
}

func TestFilterThinkTags_SplitAcrossChunks(t *testing.T) {
	a := &streamAccumulator{}

	// "<think>" is split: "<thi" + "nk>reasoning</think>hello"
	out1 := a.filterThinkTags("prefix<thi")
	out2 := a.filterThinkTags("nk>reasoning</think>hello")

	if out1 != "prefix" {
		t.Errorf("chunk1: got %q, want %q", out1, "prefix")
	}
	if out2 != "hello" {
		t.Errorf("chunk2: got %q, want %q", out2, "hello")
	}
	if a.reasoning.String() != "reasoning" {
		t.Errorf("reasoning = %q, want %q", a.reasoning.String(), "reasoning")
	}
}

func TestFilterThinkTags_CloseTagSplit(t *testing.T) {
	a := &streamAccumulator{}

	// Open tag arrives whole, close tag is split.
	out1 := a.filterThinkTags("<think>reasoning</thi")
	out2 := a.filterThinkTags("nk>hello")

	if out1 != "" {
		t.Errorf("chunk1: got %q, want %q", out1, "")
	}
	if out2 != "hello" {
		t.Errorf("chunk2: got %q, want %q", out2, "hello")
	}
}

func TestFilterThinkTags_NoTags(t *testing.T) {
	a := &streamAccumulator{}
	got := a.filterThinkTags("just normal text")
	if got != "just normal text" {
		t.Errorf("got %q, want %q", got, "just normal text")
	}
}

func TestFilterThinkTags_MultipleChunksNoTags(t *testing.T) {
	a := &streamAccumulator{}
	out1 := a.filterThinkTags("hello ")
	out2 := a.filterThinkTags("world")

	if out1+out2 != "hello world" {
		t.Errorf("got %q, want %q", out1+out2, "hello world")
	}
}

func TestFilterThinkTags_FalsePositiveAngleBracket(t *testing.T) {
	a := &streamAccumulator{}
	// "<" at end looks like it might be "<think>" but isn't.
	out1 := a.filterThinkTags("a < b")
	if out1 != "a " {
		// "< b" の "<" が "<think>" の prefix と一致するため tagBuf にバッファされる。
		// 次のチャンクで確定する。
	}
	out2 := a.filterThinkTags(" and c > d")
	combined := out1 + out2
	if combined != "a < b and c > d" {
		t.Errorf("got %q, want %q", combined, "a < b and c > d")
	}
}

func TestPartialTagSuffix(t *testing.T) {
	tests := []struct {
		text string
		tag  string
		want string
	}{
		{"abc<thi", "<think>", "<thi"},
		{"abc<", "<think>", "<"},
		{"abc<think", "<think>", "<think"},
		{"abc<think>", "<think>", ""}, // full match, not partial
		{"abcdef", "<think>", ""},     // no match
		{"abc</thi", "</think>", "</thi"},
		{"abc</", "</think>", "</"},
	}
	for _, tt := range tests {
		got := partialTagSuffix(tt.text, tt.tag)
		if got != tt.want {
			t.Errorf("partialTagSuffix(%q, %q) = %q, want %q", tt.text, tt.tag, got, tt.want)
		}
	}
}

func TestStreamAccumulator_ToolCallAccumulation(t *testing.T) {
	a := &streamAccumulator{}

	// First chunk: new tool call with ID and name
	a.accumulateToolCall(toolCallDelta("call_1", "get_weather", `{"loc`))
	// Second chunk: argument continuation
	a.accumulateToolCall(toolCallDelta("", "", `ation":"tokyo"}`))

	if len(a.toolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(a.toolCalls))
	}
	tc := a.toolCalls[0]
	if tc.ID != "call_1" {
		t.Errorf("ID = %q, want %q", tc.ID, "call_1")
	}
	if tc.Function.Name != "get_weather" {
		t.Errorf("Name = %q, want %q", tc.Function.Name, "get_weather")
	}
	if tc.Function.Arguments != `{"location":"tokyo"}` {
		t.Errorf("Arguments = %q, want %q", tc.Function.Arguments, `{"location":"tokyo"}`)
	}
}

func TestFinalize_FlushesTagBuf(t *testing.T) {
	a := &streamAccumulator{}
	// Simulate a partial tag at stream end (not actually a tag).
	a.filterThinkTags("hello<")
	final := a.finalize()
	if final.Content != "hello<" {
		t.Errorf("finalize Content = %q, want %q", final.Content, "hello<")
	}
}
