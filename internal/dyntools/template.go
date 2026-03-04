package dyntools

import (
	"fmt"
	"regexp"
	"strings"
)

// toolTemplate is the Go source template for dynamic tools.
// The LLM-provided source code is inserted at %s. It must define:
//
//	func run(input json.RawMessage) (string, error)
//
// The template pre-imports commonly used stdlib packages and suppresses
// unused-import errors with blank identifiers. The harness handles
// stdin/stdout JSON marshaling.
const toolTemplate = `package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Suppress unused import errors.
var (
	_ = bytes.Compare
	_ = fmt.Sprintf
	_ = json.Marshal
	_ = io.EOF
	_ = math.Abs
	_ = http.Get
	_ = url.Parse
	_ = regexp.MustCompile
	_ = sort.Strings
	_ = strconv.Itoa
	_ = strings.Contains
	_ = time.Now
)

// --- Generated implementation ---

%s

// --- Harness (do not modify) ---

func main() {
	inputBytes, err := io.ReadAll(os.Stdin)
	if err != nil {
		_writeResult("", fmt.Errorf("read stdin: %%w", err))
		return
	}
	result, err := run(json.RawMessage(inputBytes))
	_writeResult(result, err)
}

func _writeResult(content string, err error) {
	type _out struct {
		Content string %s
		IsError bool   %s
	}
	o := _out{Content: content}
	if err != nil {
		o.Content = err.Error()
		o.IsError = true
	}
	json.NewEncoder(os.Stdout).Encode(o)
}
`

// stripBoilerplate removes package declarations, import blocks, and markdown
// fences from LLM-generated code so it can be cleanly inserted into the template.
func stripBoilerplate(code string) string {
	// Remove markdown code fences (```go ... ``` or ``` ... ```)
	code = regexp.MustCompile("(?m)^```(?:go)?\\s*$").ReplaceAllString(code, "")

	// Remove "package main" or "package <name>"
	code = regexp.MustCompile(`(?m)^\s*package\s+\w+\s*$`).ReplaceAllString(code, "")

	// Remove single-line imports: import "foo"
	code = regexp.MustCompile(`(?m)^\s*import\s+"[^"]+"\s*$`).ReplaceAllString(code, "")

	// Remove multi-line import blocks: import ( ... )
	code = regexp.MustCompile(`(?ms)^\s*import\s*\(.*?\)`).ReplaceAllString(code, "")

	// Remove leading/trailing blank lines
	code = strings.TrimSpace(code)

	return code
}

// GenerateSource wraps the LLM-provided code in the harness template.
func GenerateSource(userCode string) string {
	tag1 := "`json:\"content\"`"
	tag2 := "`json:\"is_error\"`"
	cleaned := stripBoilerplate(userCode)
	return fmt.Sprintf(toolTemplate, cleaned, tag1, tag2)
}
