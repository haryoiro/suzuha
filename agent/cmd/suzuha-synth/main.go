package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"math/rand/v2"
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
