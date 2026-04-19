package admin

import (
	"context"
	"net/http"

	"github.com/haryoiro/suzuha/internal/api/admin/gen"
	"github.com/haryoiro/suzuha/internal/api/admin/handler"
	"github.com/ogen-go/ogen/middleware"
)

// AdminRawStreamsLogsStream implements GET /api/logs/stream.
func (h *AdminHandler) AdminRawStreamsLogsStream(ctx context.Context, w http.ResponseWriter) error {
	r := rawRequest(ctx)
	if r == nil {
		return errMissingRequest
	}
	// LogHandler は server.go で組まれているが、ここでは都度作り直す。
	// agentURL は AdminHandler が把握しているので流用する。
	logH := handler.NewLogHandler(h.agentBase+"/internal/logs", "", h.logger)
	logH.Stream(w, r)
	return nil
}

// AdminRawStreamsDeviceFrame implements GET /api/device/frame (JPEG proxy)。
func (h *AdminHandler) AdminRawStreamsDeviceFrame(ctx context.Context, w http.ResponseWriter) error {
	r := rawRequest(ctx)
	if r == nil {
		return errMissingRequest
	}
	h.proxyDeviceFrame(w, r)
	return nil
}

// AdminRawStreamsDeviceDetections implements GET /api/device/detections (SSE proxy)。
func (h *AdminHandler) AdminRawStreamsDeviceDetections(ctx context.Context, w http.ResponseWriter) error {
	r := rawRequest(ctx)
	if r == nil {
		return errMissingRequest
	}
	h.proxyDeviceDetections(w, r)
	return nil
}

// AdminRawStreamsServeMedia implements GET /api/media/{path}.
// path は URL から serveMedia 側で解析するので params は未使用。
func (h *AdminHandler) AdminRawStreamsServeMedia(ctx context.Context, params gen.AdminRawStreamsServeMediaParams, w http.ResponseWriter) error {
	r := rawRequest(ctx)
	if r == nil {
		return errMissingRequest
	}
	h.serveMedia(w, r)
	return nil
}

// AdminRawStreamsUploadMedia implements POST /api/memories/{id}/media.
func (h *AdminHandler) AdminRawStreamsUploadMedia(ctx context.Context, params gen.AdminRawStreamsUploadMediaParams, w http.ResponseWriter) error {
	r := rawRequest(ctx)
	if r == nil {
		return errMissingRequest
	}
	h.uploadMedia(w, r)
	return nil
}

// AdminRawStreamsSearchByImage implements POST /api/memories/search-image.
func (h *AdminHandler) AdminRawStreamsSearchByImage(ctx context.Context, w http.ResponseWriter) error {
	r := rawRequest(ctx)
	if r == nil {
		return errMissingRequest
	}
	h.searchByImage(w, r)
	return nil
}

// --- middleware ---

type rawRequestKey struct{}

// RequestMiddleware は raw *http.Request を context に載せる (control/raw_handler.go と同様)。
func RequestMiddleware(req middleware.Request, next middleware.Next) (middleware.Response, error) {
	req.Context = context.WithValue(req.Context, rawRequestKey{}, req.Raw)
	return next(req)
}

func rawRequest(ctx context.Context) *http.Request {
	if r, ok := ctx.Value(rawRequestKey{}).(*http.Request); ok {
		return r
	}
	return nil
}

var errMissingRequest = &rawHandlerError{msg: "raw request not found in context (RequestMiddleware missing?)"}

type rawHandlerError struct{ msg string }

func (e *rawHandlerError) Error() string { return e.msg }
