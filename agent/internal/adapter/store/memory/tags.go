package memory

import "regexp"

var tagRe = regexp.MustCompile(`(?:^|\s)#([a-zA-Z0-9_\p{L}]+)`)

// ExtractTags parses #tag patterns from content and returns unique tags.
func ExtractTags(content string) []string {
	matches := tagRe.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(matches))
	var tags []string
	for _, m := range matches {
		tag := m[1]
		if !seen[tag] {
			seen[tag] = true
			tags = append(tags, tag)
		}
	}
	return tags
}
