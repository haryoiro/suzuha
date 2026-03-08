package admin

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/haryoiro/suzuha/internal/admin/api"
)

var allowedPromptFiles = map[string]bool{
	"IDENTITY.md": true,
	"SOUL.md":     true,
}

func (h *AdminHandler) PromptsList(ctx context.Context) ([]api.PromptFile, error) {
	var files []api.PromptFile
	for name := range allowedPromptFiles {
		path := filepath.Join(h.promptDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				files = append(files, api.PromptFile{Name: name, Content: ""})
				continue
			}
			h.logger.Error("プロンプトファイルの読み込みに失敗", "name", name, "error", err.Error())
			continue
		}
		info, _ := os.Stat(path)
		var updatedAt string
		if info != nil {
			updatedAt = info.ModTime().Format(time.RFC3339)
		}
		files = append(files, api.PromptFile{
			Name:      name,
			Content:   string(data),
			UpdatedAt: updatedAt,
		})
	}
	return files, nil
}

func (h *AdminHandler) PromptsGet(ctx context.Context, params api.PromptsGetParams) (*api.PromptFile, error) {
	if !allowedPromptFiles[params.Name] {
		return nil, fmt.Errorf("not found")
	}
	path := filepath.Join(h.promptDir, params.Name)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &api.PromptFile{Name: params.Name, Content: ""}, nil
		}
		return nil, fmt.Errorf("read failed")
	}
	info, _ := os.Stat(path)
	var updatedAt string
	if info != nil {
		updatedAt = info.ModTime().Format(time.RFC3339)
	}
	return &api.PromptFile{
		Name:      params.Name,
		Content:   string(data),
		UpdatedAt: updatedAt,
	}, nil
}

func (h *AdminHandler) PromptsUpdate(ctx context.Context, req *api.UpdatePromptRequest, params api.PromptsUpdateParams) (*api.PromptsUpdateOK, error) {
	if !allowedPromptFiles[params.Name] {
		return nil, fmt.Errorf("not found")
	}

	path := filepath.Join(h.promptDir, params.Name)
	if err := os.WriteFile(path, []byte(req.Content), 0644); err != nil {
		h.logger.Error("プロンプトファイルの書き込みに失敗", "name", params.Name, "error", err.Error())
		return nil, fmt.Errorf("write failed")
	}

	h.logger.Info("プロンプトファイルを更新しました", "name", params.Name, "length", len(req.Content))

	reloaded := h.notifyPromptReload(ctx)
	return &api.PromptsUpdateOK{Ok: true, Reloaded: reloaded}, nil
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
