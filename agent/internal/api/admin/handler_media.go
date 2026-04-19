package admin

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"

	"github.com/google/uuid"
	"github.com/haryoiro/suzuha/internal/adapter/embedder"
	"github.com/haryoiro/suzuha/internal/memory"
)

const maxUploadSize = 20 * 1024 * 1024 // 20 MB

// serveMedia handles GET /api/media/{key...}
// Serves binary media from MediaStore with proper Content-Type.
func (h *AdminHandler) serveMedia(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.URL.Path, "/api/media/")
	if key == "" {
		http.Error(w, `{"error":"key required"}`, http.StatusBadRequest)
		return
	}

	data, err := h.mediaStore.Get(r.Context(), key)
	if err != nil {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}

	mime := mimeFromKey(key)
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write(data)
}

// uploadMedia handles POST /api/memories/{id}/media
// Accepts multipart form with "file" field and optional "modality" field.
// Stores in MediaStore and adds Attachment to memory.
func (h *AdminHandler) uploadMedia(w http.ResponseWriter, r *http.Request) {
	memID := r.PathValue("id")
	if memID == "" {
		http.Error(w, `{"error":"memory id required"}`, http.StatusBadRequest)
		return
	}

	// Load the memory to verify it exists.
	mem, err := h.memStore.Get(r.Context(), memID)
	if err != nil {
		http.Error(w, `{"error":"memory not found"}`, http.StatusNotFound)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		http.Error(w, `{"error":"file too large or invalid form"}`, http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, `{"error":"file field required"}`, http.StatusBadRequest)
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, `{"error":"failed to read file"}`, http.StatusInternalServerError)
		return
	}

	// Determine modality and MIME type.
	modality := r.FormValue("modality")
	mime := header.Header.Get("Content-Type")
	if modality == "" {
		if strings.HasPrefix(mime, "image/") {
			modality = "image"
		} else if strings.HasPrefix(mime, "audio/") {
			modality = "audio"
		} else {
			modality = "image" // default
		}
	}

	// Generate storage key.
	ext := extFromMime(mime)
	key := fmt.Sprintf("memories/%s/%s%s", memID, uuid.NewString()[:8], ext)

	// Store the file.
	if err := h.mediaStore.Put(r.Context(), key, data); err != nil {
		http.Error(w, `{"error":"failed to save media"}`, http.StatusInternalServerError)
		return
	}

	// Add attachment to memory.
	att := memory.Attachment{
		Key:      key,
		Modality: modality,
		MimeType: mime,
	}
	mem.Attachments = append(mem.Attachments, att)
	if err := h.memStore.Update(r.Context(), mem); err != nil {
		http.Error(w, `{"error":"failed to update memory"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{
		"key":       key,
		"modality":  modality,
		"mime_type": mime,
	})
}

// searchByImage handles POST /api/memories/search-image
// Accepts multipart form with "file" field. Embeds the image and returns
// similar memories via vector search.
func (h *AdminHandler) searchByImage(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		http.Error(w, `{"error":"file too large or invalid form"}`, http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, `{"error":"file field required"}`, http.StatusBadRequest)
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, `{"error":"failed to read file"}`, http.StatusInternalServerError)
		return
	}

	mime := header.Header.Get("Content-Type")
	if mime == "" {
		mime = "image/png"
	}

	var modality string
	if strings.HasPrefix(mime, "image/") {
		modality = "image"
	} else if strings.HasPrefix(mime, "audio/") {
		modality = "audio"
	} else {
		http.Error(w, `{"error":"unsupported file type"}`, http.StatusBadRequest)
		return
	}

	parts := []embedding.Part{
		{Modality: embedding.Modality(modality), Data: data, MimeType: mime},
	}

	memories, err := h.memStore.SearchByParts(r.Context(), parts, 10)
	if err != nil {
		http.Error(w, `{"error":"search failed"}`, http.StatusInternalServerError)
		return
	}

	type resultItem struct {
		ID        string         `json:"id"`
		Type      string         `json:"type"`
		Content   string         `json:"content"`
		Metadata  map[string]any `json:"metadata,omitempty"`
		CreatedAt string         `json:"created_at"`
		UpdatedAt string         `json:"updated_at"`
	}
	results := make([]resultItem, 0, len(memories))
	for _, m := range memories {
		results = append(results, resultItem{
			ID:        m.ID,
			Type:      string(m.Type),
			Content:   m.Content,
			Metadata:  m.Metadata,
			CreatedAt: m.CreatedAt.Format("2006-01-02 15:04:05"),
			UpdatedAt: m.UpdatedAt.Format("2006-01-02 15:04:05"),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"data": results})
}

func mimeFromKey(key string) string {
	ext := strings.ToLower(path.Ext(key))
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".wav":
		return "audio/wav"
	case ".mp3":
		return "audio/mpeg"
	case ".ogg":
		return "audio/ogg"
	case ".webm":
		return "audio/webm"
	default:
		return "application/octet-stream"
	}
}

func extFromMime(mime string) string {
	switch mime {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "audio/wav":
		return ".wav"
	case "audio/mpeg":
		return ".mp3"
	case "audio/ogg":
		return ".ogg"
	case "audio/webm":
		return ".webm"
	default:
		return ".bin"
	}
}
