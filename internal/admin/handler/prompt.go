package handler

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// PromptHandler handles reading and writing prompt files (IDENTITY.md, SOUL.md).
type PromptHandler struct {
	promptDir string // absolute path to the prompt directory
	agentBase string // agent internal HTTP base URL
	client    *http.Client
	logger    *slog.Logger
}

// NewPromptHandler creates a new PromptHandler.
func NewPromptHandler(promptDir, agentBase string, logger *slog.Logger) *PromptHandler {
	return &PromptHandler{
		promptDir: promptDir,
		agentBase: agentBase,
		client:    &http.Client{Timeout: 10 * time.Second},
		logger:    logger,
	}
}

// allowedFiles restricts which files can be read/written.
var allowedFiles = map[string]bool{
	"IDENTITY.md": true,
	"SOUL.md":     true,
}

type promptFile struct {
	Name      string `json:"name"`
	Content   string `json:"content"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// List returns all prompt files and their contents.
func (h *PromptHandler) List(w http.ResponseWriter, r *http.Request) {
	var files []promptFile
	for name := range allowedFiles {
		path := filepath.Join(h.promptDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				files = append(files, promptFile{Name: name, Content: ""})
				continue
			}
			h.logger.Error("プロンプトファイルの読み込みに失敗", "name", name, "error", err)
			continue
		}
		info, _ := os.Stat(path)
		var updatedAt string
		if info != nil {
			updatedAt = info.ModTime().Format(time.RFC3339)
		}
		files = append(files, promptFile{
			Name:      name,
			Content:   string(data),
			UpdatedAt: updatedAt,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(files)
}

// Get returns a single prompt file.
func (h *PromptHandler) Get(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !allowedFiles[name] {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}
	path := filepath.Join(h.promptDir, name)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(promptFile{Name: name, Content: ""})
			return
		}
		http.Error(w, `{"error":"read failed"}`, http.StatusInternalServerError)
		return
	}
	info, _ := os.Stat(path)
	var updatedAt string
	if info != nil {
		updatedAt = info.ModTime().Format(time.RFC3339)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(promptFile{
		Name:      name,
		Content:   string(data),
		UpdatedAt: updatedAt,
	})
}

// Update writes a prompt file and notifies the agent to reload.
func (h *PromptHandler) Update(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !allowedFiles[name] {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}

	var body struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
		return
	}

	path := filepath.Join(h.promptDir, name)
	if err := os.WriteFile(path, []byte(body.Content), 0644); err != nil {
		h.logger.Error("プロンプトファイルの書き込みに失敗", "name", name, "error", err)
		http.Error(w, `{"error":"write failed"}`, http.StatusInternalServerError)
		return
	}

	h.logger.Info("プロンプトファイルを更新しました", "name", name, "length", len(body.Content))

	// Notify agent to reload prompt.
	reloaded := h.notifyReload(r)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"ok":       true,
		"reloaded": reloaded,
	})
}

// notifyReload tells the agent to reload its system prompt.
func (h *PromptHandler) notifyReload(r *http.Request) bool {
	if h.agentBase == "" {
		return false
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, h.agentBase+"/internal/reload-prompt", nil)
	if err != nil {
		h.logger.Warn("プロンプト再読み込みリクエストの作成に失敗", "error", err)
		return false
	}
	resp, err := h.client.Do(req)
	if err != nil {
		h.logger.Warn("プロンプト再読み込みのプロキシに失敗", "error", err)
		return false
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return resp.StatusCode == http.StatusOK
}
