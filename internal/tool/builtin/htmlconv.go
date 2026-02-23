package builtin

import (
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// skipElements are tags whose entire subtree should be discarded.
var skipElements = map[atom.Atom]bool{
	atom.Script:   true,
	atom.Style:    true,
	atom.Noscript: true,
	atom.Svg:      true,
	atom.Iframe:   true,
	atom.Nav:      true,
	atom.Footer:   true,
	atom.Header:   true,
}

// blockElements are tags that should produce a newline boundary.
var blockElements = map[atom.Atom]bool{
	atom.P:          true,
	atom.Div:        true,
	atom.Section:    true,
	atom.Article:    true,
	atom.Aside:      true,
	atom.Main:       true,
	atom.Blockquote: true,
	atom.Pre:        true,
	atom.Ul:         true,
	atom.Ol:         true,
	atom.Table:      true,
	atom.Tr:         true,
	atom.Dd:         true,
	atom.Dt:         true,
	atom.Figcaption: true,
	atom.Figure:     true,
	atom.Details:    true,
}

// htmlToMarkdown converts raw HTML to a simplified Markdown string.
// It strips scripts, styles, nav/header/footer, and converts common
// elements (headings, links, lists, code, blockquotes) to Markdown.
func htmlToMarkdown(rawHTML string) string {
	doc, err := html.Parse(strings.NewReader(rawHTML))
	if err != nil {
		return rawHTML // fallback: return as-is
	}

	var b strings.Builder
	walkNode(&b, doc)

	return collapseWhitespace(b.String())
}

func walkNode(b *strings.Builder, n *html.Node) {
	switch n.Type {
	case html.TextNode:
		b.WriteString(n.Data)
		return
	case html.ElementNode:
		// skip entire subtree for these elements
		if skipElements[n.DataAtom] {
			return
		}
	}

	if n.Type != html.ElementNode {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walkNode(b, c)
		}
		return
	}

	tag := n.DataAtom

	// Block-level newline before
	if blockElements[tag] {
		b.WriteString("\n\n")
	}

	switch tag {
	case atom.H1:
		b.WriteString("\n\n# ")
		walkChildren(b, n)
		b.WriteString("\n\n")
	case atom.H2:
		b.WriteString("\n\n## ")
		walkChildren(b, n)
		b.WriteString("\n\n")
	case atom.H3:
		b.WriteString("\n\n### ")
		walkChildren(b, n)
		b.WriteString("\n\n")
	case atom.H4:
		b.WriteString("\n\n#### ")
		walkChildren(b, n)
		b.WriteString("\n\n")
	case atom.H5:
		b.WriteString("\n\n##### ")
		walkChildren(b, n)
		b.WriteString("\n\n")
	case atom.H6:
		b.WriteString("\n\n###### ")
		walkChildren(b, n)
		b.WriteString("\n\n")

	case atom.A:
		href := getAttr(n, "href")
		b.WriteString("[")
		walkChildren(b, n)
		b.WriteString("](")
		b.WriteString(href)
		b.WriteString(")")

	case atom.Strong, atom.B:
		b.WriteString("**")
		walkChildren(b, n)
		b.WriteString("**")

	case atom.Em, atom.I:
		b.WriteString("*")
		walkChildren(b, n)
		b.WriteString("*")

	case atom.Code:
		b.WriteString("`")
		walkChildren(b, n)
		b.WriteString("`")

	case atom.Pre:
		b.WriteString("\n\n```\n")
		walkChildren(b, n)
		b.WriteString("\n```\n\n")

	case atom.Blockquote:
		b.WriteString("\n\n> ")
		var inner strings.Builder
		walkChildren(&inner, n)
		b.WriteString(strings.ReplaceAll(strings.TrimSpace(inner.String()), "\n", "\n> "))
		b.WriteString("\n\n")

	case atom.Li:
		b.WriteString("\n- ")
		walkChildren(b, n)

	case atom.Br:
		b.WriteString("\n")

	case atom.Hr:
		b.WriteString("\n\n---\n\n")

	case atom.Img:
		alt := getAttr(n, "alt")
		if alt != "" {
			b.WriteString("[image: ")
			b.WriteString(alt)
			b.WriteString("]")
		}

	default:
		walkChildren(b, n)
		if blockElements[tag] {
			b.WriteString("\n\n")
		}
	}
}

func walkChildren(b *strings.Builder, n *html.Node) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walkNode(b, c)
	}
}

func getAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// collapseWhitespace reduces runs of blank lines to at most two newlines
// and trims leading/trailing whitespace.
func collapseWhitespace(s string) string {
	lines := strings.Split(s, "\n")
	var out []string
	blanks := 0
	for _, line := range lines {
		trimmed := strings.TrimRight(line, " \t")
		if trimmed == "" {
			blanks++
			if blanks <= 1 {
				out = append(out, "")
			}
		} else {
			blanks = 0
			out = append(out, trimmed)
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}
