package mcp

import (
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestConvertResult(t *testing.T) {
	tests := []struct {
		name          string
		input         *mcpsdk.CallToolResult
		wantIsError   bool
		wantLen       int
		wantFirstType string
		wantFirstText string
	}{
		{
			"text content",
			&mcpsdk.CallToolResult{
				Content: []mcpsdk.Content{
					&mcpsdk.TextContent{Text: "hello world"},
				},
			},
			false, 1, "text", "hello world",
		},
		{
			"error flag",
			&mcpsdk.CallToolResult{
				Content: []mcpsdk.Content{
					&mcpsdk.TextContent{Text: "something went wrong"},
				},
				IsError: true,
			},
			true, 1, "", "something went wrong",
		},
		{
			"empty result",
			&mcpsdk.CallToolResult{},
			false, 1, "", "(空の結果)",
		},
		{
			"image content",
			&mcpsdk.CallToolResult{
				Content: []mcpsdk.Content{
					&mcpsdk.ImageContent{MIMEType: "image/png", Data: []byte("fakepng")},
				},
			},
			false, 1, "", "[image: image/png]",
		},
		{
			"multiple contents",
			&mcpsdk.CallToolResult{
				Content: []mcpsdk.Content{
					&mcpsdk.TextContent{Text: "line 1"},
					&mcpsdk.TextContent{Text: "line 2"},
				},
			},
			false, 2, "", "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ConvertResult(tt.input)
			if got.IsError != tt.wantIsError {
				t.Errorf("IsError = %v, want %v", got.IsError, tt.wantIsError)
			}
			if len(got.Content) != tt.wantLen {
				t.Fatalf("len(Content) = %d, want %d", len(got.Content), tt.wantLen)
			}
			if tt.wantFirstType != "" && got.Content[0].Type != tt.wantFirstType {
				t.Errorf("Content[0].Type = %q, want %q", got.Content[0].Type, tt.wantFirstType)
			}
			if tt.wantFirstText != "" && got.Content[0].Text != tt.wantFirstText {
				t.Errorf("Content[0].Text = %q, want %q", got.Content[0].Text, tt.wantFirstText)
			}
		})
	}
}
