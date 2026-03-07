package builtin

import (
	"strings"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
)

// htmlToMarkdown converts raw HTML to clean Markdown using html-to-markdown.
func htmlToMarkdown(rawHTML string) string {
	md, err := htmltomarkdown.ConvertString(rawHTML)
	if err != nil {
		return rawHTML
	}
	return collapseWhitespace(md)
}

// collapseWhitespace reduces runs of blank lines to at most two newlines
// and trims leading/trailing whitespace.
func collapseWhitespace(s string) string {
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(s)
}
