package admin

import (
	"bytes"

	"github.com/go-faster/jx"
	"github.com/haryoiro/suzuha/internal/admin/api"
)

// jxReader wraps jx.Raw as an io.Reader.
func jxReader(data jx.Raw) *bytes.Reader {
	return bytes.NewReader(data)
}

func optStr(s string) api.OptString {
	if s == "" {
		return api.OptString{}
	}
	return api.NewOptString(s)
}

func optStrPtr(s *string) api.OptString {
	if s == nil {
		return api.OptString{}
	}
	return api.NewOptString(*s)
}

func safePct(n, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(n) / float64(total) * 100
}
