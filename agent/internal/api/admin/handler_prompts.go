package admin

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/haryoiro/suzuha/internal/api/admin/gen"
)

var allowedPromptFiles = map[string]bool{
	"IDENTITY.md": true,
	"SOUL.md":     true,
}

func (h *AdminHandler) PromptsList(ctx context.Context) ([]gen.PromptFile, error) {
	var files []gen.PromptFile
	for name := range allowedPromptFiles {
		path := filepath.Join(h.promptDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				files = append(files, gen.PromptFile{Name: name, Content: ""})
				continue
			}
			h.logger.Error("プロンプトファイルの読み込みに失敗", "name", name, "error", err.Error())
			continue
		}
		var updatedAt string
		if info, err := os.Stat(path); err != nil {
			slog.Warn("プロンプトファイルのstat取得に失敗", "name", name, "error", err)
		} else {
			updatedAt = info.ModTime().Format(time.RFC3339)
		}
		files = append(files, gen.PromptFile{
			Name:      name,
			Content:   string(data),
			UpdatedAt: updatedAt,
		})
	}
	return files, nil
}

func (h *AdminHandler) PromptsGet(ctx context.Context, params gen.PromptsGetParams) (*gen.PromptFile, error) {
	if !allowedPromptFiles[params.Name] {
		return nil, fmt.Errorf("not found")
	}
	path := filepath.Join(h.promptDir, params.Name)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &gen.PromptFile{Name: params.Name, Content: ""}, nil
		}
		return nil, fmt.Errorf("read failed")
	}
	var updatedAt string
	if info, err := os.Stat(path); err != nil {
		slog.Warn("プロンプトファイルのstat取得に失敗", "name", params.Name, "error", err)
	} else {
		updatedAt = info.ModTime().Format(time.RFC3339)
	}
	return &gen.PromptFile{
		Name:      params.Name,
		Content:   string(data),
		UpdatedAt: updatedAt,
	}, nil
}

func (h *AdminHandler) PromptsUpdate(ctx context.Context, req *gen.UpdatePromptRequest, params gen.PromptsUpdateParams) (*gen.PromptsUpdateOK, error) {
	if !allowedPromptFiles[params.Name] {
		return nil, fmt.Errorf("not found")
	}

	path := filepath.Join(h.promptDir, params.Name)
	if err := os.WriteFile(path, []byte(req.Content), 0644); err != nil {
		return nil, fmt.Errorf("write failed")
	}

	h.logger.Info("プロンプトファイルを更新しました", "name", params.Name, "length", len(req.Content))

	reloaded := h.notifyPromptReload(ctx)
	return &gen.PromptsUpdateOK{Ok: true, Reloaded: reloaded}, nil
}

func (h *AdminHandler) notifyPromptReload(ctx context.Context) bool {
	if h.agentBase == "" {
		return false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.agentBase+"/internal/reload-prompt", nil)
	if err != nil {
		return false
	}
	resp, err := h.client.Do(req)
	if err != nil {
		h.logger.Warn("プロンプト再読み込みのプロキシに失敗", "error", err.Error())
		return false
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return resp.StatusCode == http.StatusOK
}
