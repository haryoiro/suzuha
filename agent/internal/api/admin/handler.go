package admin

import (
	"database/sql"
	"log/slog"
	"net/http"
	"time"

	"github.com/haryoiro/suzuha/internal/adapter/store/memory"
	"github.com/haryoiro/suzuha/internal/api/admin/gen"
	"github.com/haryoiro/suzuha/internal/port/user"
)

// AdminHandler implements the ogen-generated gen.Handler interface.
type AdminHandler struct {
	gen.UnimplementedHandler

	memStore   memory.AdminStore
	userStore  user.AdminStore
	schedStore ActionStore
	diaryStore DiaryStore
	mediaStore memory.MediaStore
	db         *sql.DB
	agentBase  string // e.g. "http://agent:9090"
	promptDir  string
	logger     *slog.Logger
	client     *http.Client
	longClient *http.Client // for long-running requests (compact, playground)
}

// NewAdminHandler creates a new AdminHandler.
func NewAdminHandler(
	memStore memory.AdminStore,
	userStore user.AdminStore,
	schedStore ActionStore,
	diaryStore DiaryStore,
	mediaStore memory.MediaStore,
	agentBase string,
	promptDir string,
	logger *slog.Logger,
) *AdminHandler {
	return &AdminHandler{
		memStore:   memStore,
		userStore:  userStore,
		schedStore: schedStore,
		diaryStore: diaryStore,
		mediaStore: mediaStore,
		db:         memStore.DB(),
		agentBase:  agentBase,
		promptDir:  promptDir,
		logger:     logger,
		client:     &http.Client{Timeout: 10 * time.Second},
		longClient: &http.Client{Timeout: 5 * time.Minute},
	}
}
