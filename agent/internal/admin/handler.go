package admin

import (
	"database/sql"
	"log/slog"
	"net/http"
	"time"

	"github.com/haryoiro/suzuha/internal/admin/api"
	"github.com/haryoiro/suzuha/internal/memory"
	"github.com/haryoiro/suzuha/internal/user"
)

// AdminHandler implements the ogen-generated api.Handler interface.
type AdminHandler struct {
	api.UnimplementedHandler

	memStore   memory.AdminStore
	userStore  user.AdminStore
	schedStore ActionStore
	diaryStore DiaryStore
	locStore   LocationStore
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
	locStore LocationStore,
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
		locStore:   locStore,
		mediaStore: mediaStore,
		db:         memStore.DB(),
		agentBase:  agentBase,
		promptDir:  promptDir,
		logger:     logger,
		client:     &http.Client{Timeout: 10 * time.Second},
		longClient: &http.Client{Timeout: 5 * time.Minute},
	}
}
