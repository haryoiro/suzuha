package control

import (
	"context"
	"net/http"

	"github.com/haryoiro/suzuha/internal/api/control/gen"
	"github.com/haryoiro/suzuha/internal/config"
	"github.com/haryoiro/suzuha/internal/feature/location"
	"github.com/haryoiro/suzuha/internal/feature/vision"
	"github.com/haryoiro/suzuha/internal/observe"
	"github.com/ogen-go/ogen/middleware"
	"github.com/samber/do/v2"
	"log/slog"
)

// RawHandler は SSE / binary / stream 系 (x-ogen-raw-response) を実装する。
// ogen は (ctx, http.ResponseWriter) しか渡さないので、生 *http.Request が
// 必要なハンドラは RequestMiddleware で ctx に注入したものを取り出す。
type RawHandler struct {
	logRing  *observe.RingBuffer
	vision   *vision.Feature
	locStore *location.Store
	locToken string
	logger   *slog.Logger
}

// NewRawHandler は DI injector から依存を取り出して RawHandler を生成する。
func NewRawHandler(i do.Injector) (gen.RawHandler, error) {
	cfg := do.MustInvoke[*config.Config](i)
	return &RawHandler{
		logRing:  do.MustInvoke[*observe.RingBuffer](i),
		vision:   do.MustInvoke[*vision.Feature](i),
		locStore: do.MustInvoke[*location.Store](i),
		locToken: cfg.Location.Token,
		logger:   do.MustInvoke[*slog.Logger](i),
	}, nil
}

// RawStreamsLogs implements GET /internal/logs (SSE).
func (h *RawHandler) RawStreamsLogs(ctx context.Context, w http.ResponseWriter) error {
	r := rawRequest(ctx)
	if r == nil {
		return errMissingRequest
	}
	observe.LogHandler(h.logRing).ServeHTTP(w, r)
	return nil
}

// RawStreamsDeviceDetections implements GET /internal/device/detections (SSE).
func (h *RawHandler) RawStreamsDeviceDetections(ctx context.Context, w http.ResponseWriter) error {
	r := rawRequest(ctx)
	if r == nil {
		return errMissingRequest
	}
	h.vision.Frames().DetectionStreamHandler()(w, r)
	return nil
}

// RawStreamsDeviceFrame implements GET /internal/device/frame (binary JPEG).
func (h *RawHandler) RawStreamsDeviceFrame(ctx context.Context, w http.ResponseWriter) error {
	r := rawRequest(ctx)
	if r == nil {
		return errMissingRequest
	}
	h.vision.Frames().FrameHandler()(w, r)
	return nil
}

// RawStreamsOverland implements POST /internal/overland (Bearer 認証 + JSON)。
// トークン検証とペイロード解析は location.Handler に委譲する。
func (h *RawHandler) RawStreamsOverland(ctx context.Context, w http.ResponseWriter) error {
	r := rawRequest(ctx)
	if r == nil {
		return errMissingRequest
	}
	location.NewHandler(h.locStore, h.locToken, h.logger).ServeHTTP(w, r)
	return nil
}

// --- middleware で *http.Request を ctx に注入するユーティリティ ---

type rawRequestKey struct{}

// RequestMiddleware は raw *http.Request を context に載せる。
// ogen の RawHandler シグネチャは ctx と w しか渡してこないため、
// 既存の http.Handler 互換実装を呼ぶにはここで掴んでおく必要がある。
func RequestMiddleware(req middleware.Request, next middleware.Next) (middleware.Response, error) {
	req.Context = context.WithValue(req.Context, rawRequestKey{}, req.Raw)
	return next(req)
}

// rawRequest は RequestMiddleware 経由で ctx に載った raw *http.Request を返す。
// middleware が登録されていない場合 nil。
func rawRequest(ctx context.Context) *http.Request {
	if r, ok := ctx.Value(rawRequestKey{}).(*http.Request); ok {
		return r
	}
	return nil
}

// errMissingRequest は RequestMiddleware が登録されていないときに返す。
var errMissingRequest = &rawHandlerError{msg: "raw request not found in context (RequestMiddleware missing?)"}

type rawHandlerError struct{ msg string }

func (e *rawHandlerError) Error() string { return e.msg }
