package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/haryoiro/suzuha/internal/config"
	"github.com/haryoiro/suzuha/internal/lib/textutil"
	"github.com/haryoiro/suzuha/internal/llm"
)

func main() {
	cfgPath := flag.String("config", "config.yaml", "config file path")
	count := flag.Int("count", 50, "number of scenarios to generate")
	output := flag.String("output", "", "output JSONL file path")
	dataset := flag.String("dataset", "", "Langfuse dataset name")
	uploadFile := flag.String("upload", "", "upload JSONL file to Langfuse dataset")
	exportDataset := flag.String("export", "", "export Langfuse dataset to Fireworks JSONL (requires -output)")
	flag.Parse()

	// Export: Langfuse dataset → Fireworks JSONL
	if *exportDataset != "" {
		if *output == "" {
			log.Fatal("-output is required for export")
		}
		if err := runExport(*cfgPath, *exportDataset, *output); err != nil {
			log.Fatal(err)
		}
		return
	}

	// Upload: JSONL → Langfuse dataset
	if *uploadFile != "" {
		if *dataset == "" {
			log.Fatal("-dataset is required for upload")
		}
		if err := runUpload(*cfgPath, *uploadFile, *dataset); err != nil {
			log.Fatal(err)
		}
		return
	}

	// Generate: LLM → JSONL
	if *output == "" {
		log.Fatal("-output is required: specify a file path to save generated data")
	}

	if err := runGenerate(*cfgPath, *count, *output); err != nil {
		log.Fatal(err)
	}
}

type pair struct {
	Input  string `json:"input"`
	Output string `json:"output"`
}

func runGenerate(cfgPath string, count int, outputPath string) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	client, err := llm.NewClient(
		cfg.LLM.Provider, cfg.LLM.Model, cfg.LLM.APIKey, cfg.LLM.APIBase,
		cfg.LLM.MaxTokens,
		llm.EmbeddingConfig{}, llm.VisionConfig{},
		logger,
	)
	if err != nil {
		return fmt.Errorf("llm: %w", err)
	}

	identity, err := os.ReadFile(".suzuha/IDENTITY.md")
	if err != nil {
		return fmt.Errorf("IDENTITY.md: %w", err)
	}
	systemPrompt := strings.TrimSpace(string(identity))

	ctx := context.Background()

	// Step 1: Generate scenarios.
	log.Printf("Generating %d scenarios...", count)
	scenarios, err := generateScenarios(ctx, client, count)
	if err != nil {
		return fmt.Errorf("scenarios: %w", err)
	}
	log.Printf("Got %d scenarios", len(scenarios))

	// Step 2: Generate responses, saving each to file immediately.
	f, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("create output: %w", err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)

	saved := 0
	for i, scenario := range scenarios {
		resp, err := client.Complete(ctx, []llm.Message{
			{Role: "system", Content: systemPrompt, Timestamp: time.Now()},
			{Role: "user", Content: scenario, Timestamp: time.Now()},
		}, nil)
		if err != nil {
			log.Printf("[%d/%d] error: %v", i+1, len(scenarios), err)
			continue
		}
		text := strings.TrimSpace(resp.Text)
		if text == "" || llm.IsSilentResponse(text) {
			log.Printf("[%d/%d] skipped (empty/silent)", i+1, len(scenarios))
			continue
		}
		if strings.Contains(text, "device_look") || strings.Contains(text, "skip_response") {
			log.Printf("[%d/%d] skipped (tool leak)", i+1, len(scenarios))
			continue
		}
		p := pair{Input: scenario, Output: text}
		enc.Encode(p)
		saved++
		log.Printf("[%d/%d] %s → %s", i+1, len(scenarios), textutil.TruncateRunes(scenario, 40), textutil.TruncateRunes(text, 40))
	}

	log.Printf("Saved %d pairs to %s", saved, outputPath)
	return nil
}

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
		endpoint:   cfg.Langfuse.Endpoint,
		pubKey:     cfg.Langfuse.PublicKey,
		secKey:     cfg.Langfuse.SecretKey,
		httpClient: &http.Client{Timeout: 30 * time.Second},
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
		endpoint:   cfg.Langfuse.Endpoint,
		pubKey:     cfg.Langfuse.PublicKey,
		secKey:     cfg.Langfuse.SecretKey,
		httpClient: &http.Client{Timeout: 30 * time.Second},
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
		resp, err := c.httpClient.Do(req)
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

// generateScenarios uses the LLM to create diverse conversation inputs.
func generateScenarios(ctx context.Context, client *llm.Client, count int) ([]string, error) {
	categories := []string{
		"日常の雑談（天気、食べ物、予定）",
		"感情を含む会話（嬉しい、疲れた、悲しい）",
		"質問・相談（おすすめ、どう思う）",
		"報告・共有（〜した、〜見つけた）",
		"短い呼びかけ（おはよう、ただいま、暇）",
		"意見を求める（〜ってどう思う？）",
		"突飛な話題（宇宙、時間旅行、もしも系）",
		"自分について聞かれる（好きなもの、趣味）",
		"ツッコミ・いじり",
		"沈黙・短い反応（うん、そう、へー）",
	}

	perCategory := count / len(categories)
	if perCategory < 1 {
		perCategory = 1
	}

	prompt := fmt.Sprintf(`以下のカテゴリごとに、Discordで友達に話しかけるような自然な日本語のセリフを%d個ずつ生成してください。

カテゴリ:
%s

ルール:
- 1行1セリフ
- 敬語なし、タメ口
- 短いもの（1〜2文）
- バリエーション豊富に
- カテゴリ名は出力しない
- 番号も付けない
- 1行に1つだけ`, perCategory, strings.Join(categories, "\n"))

	resp, err := client.Complete(ctx, []llm.Message{
		{Role: "user", Content: prompt, Timestamp: time.Now()},
	}, nil)
	if err != nil {
		return nil, err
	}

	var scenarios []string
	for _, line := range strings.Split(resp.Text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "-") {
			continue
		}
		// Strip leading numbers like "1." "1)" etc.
		line = stripLeadingNumber(line)
		if line != "" {
			scenarios = append(scenarios, line)
		}
	}

	// Shuffle for variety.
	rand.Shuffle(len(scenarios), func(i, j int) {
		scenarios[i], scenarios[j] = scenarios[j], scenarios[i]
	})

	if len(scenarios) > count {
		scenarios = scenarios[:count]
	}
	return scenarios, nil
}

func stripLeadingNumber(s string) string {
	// Remove patterns like "1. ", "1) ", "1、"
	for i, r := range s {
		if r >= '0' && r <= '9' {
			continue
		}
		if r == '.' || r == ')' || r == '、' || r == '：' {
			rest := strings.TrimSpace(s[i+1:])
			if rest != "" {
				return rest
			}
		}
		break
	}
	return s
}

// langfuseClient is a minimal Langfuse API client for dataset operations.
type langfuseClient struct {
	endpoint   string
	pubKey     string
	secKey     string
	httpClient *http.Client
}

func (c *langfuseClient) createDataset(ctx context.Context, name string) error {
	body, _ := json.Marshal(map[string]string{"name": name})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/api/public/v2/datasets", bytes.NewReader(body))
	req.SetBasicAuth(c.pubKey, c.secKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
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
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

