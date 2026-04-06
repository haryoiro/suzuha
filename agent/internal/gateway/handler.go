package gateway

import (
	"encoding/json"
	"net/http"
)

// StatusHandler は Gateway の全ソース状態を返す HTTP ハンドラを返す。
func (g *Gateway) StatusHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(g.Status())
	}
}
