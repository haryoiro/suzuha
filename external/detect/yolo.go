package detect

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Detection represents a single YOLO detection result.
type Detection struct {
	Label      string  `json:"label"`
	Confidence float64 `json:"confidence"`
	BBox       [4]int  `json:"bbox"` // x1, y1, x2, y2
}

// DetectionResult is the response from the YOLO sidecar.
type DetectionResult struct {
	Detections  []Detection `json:"detections"`
	InferenceMs float64     `json:"inference_ms"`
}

// YOLOClient calls the YOLO sidecar HTTP API.
type YOLOClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewYOLOClient creates a YOLO client.
func NewYOLOClient(baseURL string) *YOLOClient {
	return &YOLOClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Detect sends a JPEG image to the YOLO sidecar and returns detections.
func (c *YOLOClient) Detect(ctx context.Context, jpeg []byte) (*DetectionResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/detect", bytes.NewReader(jpeg))
	if err != nil {
		return nil, fmt.Errorf("yolo: リクエスト作成失敗: %w", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("yolo: リクエスト失敗: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("yolo: ステータス %d: %s", resp.StatusCode, string(body))
	}

	var result DetectionResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("yolo: JSON解析失敗: %w", err)
	}
	return &result, nil
}
