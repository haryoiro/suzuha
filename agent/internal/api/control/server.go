package control

import (
	"fmt"
	"net/http"

	"github.com/haryoiro/suzuha/internal/api/control/gen"
)

// NewOgenHandler は gen が生成した Server を http.Handler として返す。
// main の mux に mount して使う。h は各 sub-handler を embed した統合型。
// RequestMiddleware を登録して RawHandler が *http.Request を取り出せるようにする。
func NewOgenHandler(h gen.Handler) (http.Handler, error) {
	srv, err := gen.NewServer(h, gen.WithMiddleware(RequestMiddleware))
	if err != nil {
		return nil, fmt.Errorf("control: ogen サーバーの作成に失敗: %w", err)
	}
	return srv, nil
}
