package mcpbridge

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestConvertResult_TextContent(t *testing.T) {
	res := &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: "hello world"},
		},
	}
	got := ConvertResult(res)
	if got.IsError {
		t.Fatal("expected IsError=false")
	}
	if len(got.Content) != 1 {
		t.Fatalf("expected 1 content, got %d", len(got.Content))
	}
	if got.Content[0].Type != "text" || got.Content[0].Text != "hello world" {
		t.Fatalf("unexpected content: %+v", got.Content[0])
	}
}

func TestConvertResult_ErrorFlag(t *testing.T) {
	res := &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: "something went wrong"},
		},
		IsError: true,
	}
	got := ConvertResult(res)
	if !got.IsError {
		t.Fatal("expected IsError=true")
	}
	if got.Content[0].Text != "something went wrong" {
		t.Fatalf("unexpected text: %s", got.Content[0].Text)
	}
}

func TestConvertResult_Empty(t *testing.T) {
	res := &mcp.CallToolResult{}
	got := ConvertResult(res)
	if len(got.Content) != 1 {
		t.Fatalf("expected 1 placeholder content, got %d", len(got.Content))
	}
	if got.Content[0].Text != "(empty result)" {
		t.Fatalf("unexpected placeholder: %s", got.Content[0].Text)
	}
}

func TestConvertResult_ImageContent(t *testing.T) {
	res := &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.ImageContent{MIMEType: "image/png", Data: []byte("fakepng")},
		},
	}
	got := ConvertResult(res)
	if got.Content[0].Text != "[image: image/png]" {
		t.Fatalf("unexpected image placeholder: %s", got.Content[0].Text)
	}
}

func TestConvertResult_MultipleContents(t *testing.T) {
	res := &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: "line 1"},
			&mcp.TextContent{Text: "line 2"},
		},
	}
	got := ConvertResult(res)
	if len(got.Content) != 2 {
		t.Fatalf("expected 2 contents, got %d", len(got.Content))
	}
}
