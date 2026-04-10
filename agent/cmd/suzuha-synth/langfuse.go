package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/haryoiro/suzuha/internal/config"
)

// fireworksEntry represents one line of Fireworks AI JSONL format.
type fireworksEntry struct {
	Tools    json.RawMessage          `json:"tools,omitempty"`
	Messages []map[string]interface{} `json:"messages,omitempty"`
	// Simple pair format fallback.
	Input  string `json:"input,omitempty"`
	Output string `json:"output,omitempty"`
}

func runUpload(cfgPath, filePath, datasetName string) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if !cfg.Langfuse.Enabled {
		return fmt.Errorf("langfuse is not enabled in config")
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}

	var entries []fireworksEntry
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var e fireworksEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			log.Printf("skip bad line: %v", err)
			continue
		}
		entries = append(entries, e)
	}
	log.Printf("Loaded %d entries from %s", len(entries), filePath)

	ctx := context.Background()
	lf := &langfuseClient{
		endpoint: cfg.Langfuse.Endpoint,
		pubKey:   cfg.Langfuse.PublicKey,
		secKey:   cfg.Langfuse.SecretKey,
	}

	if err := lf.createDataset(ctx, datasetName); err != nil {
		return fmt.Errorf("create dataset: %w", err)
	}
	log.Printf("Dataset '%s' ready", datasetName)

	uploaded := 0
	for i, e := range entries {
		input, expectedOutput := extractInputOutput(e)
		if err := lf.createDatasetItemRaw(ctx, datasetName, input, expectedOutput); err != nil {
			log.Printf("[%d] upload error: %v", i, err)
			continue
		}
		uploaded++
	}
	log.Printf("Uploaded %d/%d items to dataset '%s'", uploaded, len(entries), datasetName)
	return nil
}

// extractInputOutput pulls the user input and assistant output from a Fireworks entry.
func extractInputOutput(e fireworksEntry) (input any, expectedOutput any) {
	// Simple pair format.
	if e.Input != "" {
		return e.Input, e.Output
	}

	// Fireworks messages format: extract last user message as input,
	// everything from assistant onwards as expectedOutput.
	var userInput string
	var userName string
	var assistantParts []map[string]interface{}
	foundUser := false
	for _, m := range e.Messages {
		role, _ := m["role"].(string)
		if role == "user" {
			content, _ := m["content"].(string)
			userInput = content
			if n, ok := m["name"].(string); ok {
				userName = n
			}
			foundUser = true
			assistantParts = nil // reset
		} else if foundUser && (role == "assistant" || role == "tool") {
			assistantParts = append(assistantParts, m)
		}
	}

	// Build structured input with tools if present.
	inputObj := map[string]any{"user_message": userInput}
	if userName != "" {
		inputObj["user_name"] = userName
	}
	if len(e.Tools) > 0 {
		var tools any
		json.Unmarshal(e.Tools, &tools)
		inputObj["tools"] = tools
	}
	// Add system prompt if present.
	for _, m := range e.Messages {
		if role, _ := m["role"].(string); role == "system" {
			inputObj["system"] = m["content"]
			break
		}
	}

	return inputObj, assistantParts
}

func runExport(cfgPath, datasetName, outputPath string) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if !cfg.Langfuse.Enabled {
		return fmt.Errorf("langfuse is not enabled in config")
	}

	lf := &langfuseClient{
		endpoint: cfg.Langfuse.Endpoint,
		pubKey:   cfg.Langfuse.PublicKey,
		secKey:   cfg.Langfuse.SecretKey,
	}

	ctx := context.Background()
	items, err := lf.fetchDatasetItems(ctx, datasetName)
	if err != nil {
		return fmt.Errorf("fetch dataset: %w", err)
	}
	log.Printf("Fetched %d items from dataset '%s'", len(items), datasetName)

	f, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("create output: %w", err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)

	exported := 0
	for _, item := range items {
		entry := convertToFireworks(item)
		if entry == nil {
			continue
		}
		enc.Encode(entry)
		exported++
	}

	log.Printf("Exported %d items to %s", exported, outputPath)
	return nil
}

// datasetItem represents a Langfuse dataset item from the API.
type datasetItem struct {
	ID             string `json:"id"`
	Input          any    `json:"input"`
	ExpectedOutput any    `json:"expectedOutput"`
	Status         string `json:"status"`
}

func (c *langfuseClient) fetchDatasetItems(ctx context.Context, datasetName string) ([]datasetItem, error) {
	var allItems []datasetItem
	page := 1
	for {
		url := fmt.Sprintf("%s/api/public/dataset-items?datasetName=%s&page=%d&limit=50",
			c.endpoint, datasetName, page)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		req.SetBasicAuth(c.pubKey, c.secKey)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		var result struct {
			Data []datasetItem `json:"data"`
			Meta struct {
				Page       int `json:"page"`
				TotalPages int `json:"totalPages"`
			} `json:"meta"`
		}
		json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()

		allItems = append(allItems, result.Data...)
		if page >= result.Meta.TotalPages {
			break
		}
		page++
	}
	return allItems, nil
}

// convertToFireworks converts a Langfuse dataset item to Fireworks JSONL format.
func convertToFireworks(item datasetItem) map[string]any {
	var messages []map[string]any
	var inputMap map[string]any

	// System prompt is intentionally excluded — character behavior
	// is baked into the model weights via fine-tuning.

	// Handle both string input and structured input.
	switch inp := item.Input.(type) {
	case string:
		// Simple pair: input is a plain string.
		if inp != "" {
			messages = append(messages, map[string]any{"role": "user", "content": inp, "name": "はりょ"})
		}
	case map[string]any:
		inputMap = inp
		if userMsg, ok := inp["user_message"]; ok {
			userMessage := map[string]any{"role": "user", "content": userMsg}
			if name, ok := inp["user_name"]; ok {
				userMessage["name"] = name
			}
			messages = append(messages, userMessage)
		}
	default:
		return nil
	}

	// Expected output → assistant messages.
	switch out := item.ExpectedOutput.(type) {
	case string:
		if out != "" {
			messages = append(messages, map[string]any{"role": "assistant", "content": out})
		}
	case []any:
		// Array of message objects (tool call flows).
		for _, m := range out {
			if msg, ok := m.(map[string]any); ok {
				messages = append(messages, msg)
			}
		}
	}

	if len(messages) < 2 {
		return nil
	}

	entry := map[string]any{"messages": messages}

	// Tools.
	if inputMap != nil {
		if tools, ok := inputMap["tools"]; ok {
			entry["tools"] = tools
		}
	}

	return entry
}

// langfuseClient is a minimal Langfuse API client for dataset operations.
type langfuseClient struct {
	endpoint string
	pubKey   string
	secKey   string
}

func (c *langfuseClient) createDataset(ctx context.Context, name string) error {
	body, _ := json.Marshal(map[string]string{"name": name})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/api/public/v2/datasets", bytes.NewReader(body))
	req.SetBasicAuth(c.pubKey, c.secKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// 200 = created, 409 = already exists — both OK.
	if resp.StatusCode != 200 && resp.StatusCode != 409 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

func (c *langfuseClient) createDatasetItem(ctx context.Context, datasetName, systemPrompt, input, expectedOutput string) error {
	return c.createDatasetItemRaw(ctx, datasetName,
		map[string]any{
			"system":       systemPrompt,
			"user_message": input,
		},
		expectedOutput,
	)
}

func (c *langfuseClient) createDatasetItemRaw(ctx context.Context, datasetName string, input, expectedOutput any) error {
	body, _ := json.Marshal(map[string]any{
		"datasetName":    datasetName,
		"input":          input,
		"expectedOutput": expectedOutput,
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/api/public/dataset-items", bytes.NewReader(body))
	req.SetBasicAuth(c.pubKey, c.secKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}
