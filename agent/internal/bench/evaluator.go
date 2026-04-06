package bench

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Evaluate は claude -p を使って応答を評価する。
func Evaluate(results []Result, identityPath string) ([]Result, error) {
	identity, err := os.ReadFile(identityPath)
	if err != nil {
		return nil, fmt.Errorf("bench: IDENTITY.md 読み込みに失敗: %w", err)
	}

	for i := range results {
		if results[i].Error != "" || results[i].Response == "" {
			continue
		}
		scores, err := evaluateOne(results[i], string(identity))
		if err != nil {
			results[i].Error = fmt.Sprintf("evaluation failed: %v", err)
			continue
		}
		results[i].Scores = scores
	}
	return results, nil
}

func evaluateOne(r Result, identity string) (*Scores, error) {
	prompt := fmt.Sprintf(`以下のAIエージェントの応答を評価してください。

## キャラクター設定
%s

## テスト入力
プロンプト: %s

## エージェントの応答
%s

## 評価基準
%s

## 指示
以下の3軸で1-5点で評価し、JSON で返してください。他のテキストは不要です。

{
  "relevance": <1-5: プロンプトに対する関連性と適切さ>,
  "naturalness": <1-5: 日本語としての自然さ、口語体の適切さ>,
  "character": <1-5: キャラクター設定への一貫性 (話し方、長さ、禁止事項の遵守)>,
  "reasoning": "<評価の根拠を1-2文で>"
}`, identity, r.Prompt, r.Response, r.Expect)

	cmd := exec.Command("claude", "-p", "--output-format", "text", prompt)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("claude -p 実行に失敗: %w", err)
	}

	// JSON 部分を抽出
	raw := strings.TrimSpace(string(out))
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end < 0 || end <= start {
		return nil, fmt.Errorf("JSON が見つからない: %s", raw)
	}
	jsonStr := raw[start : end+1]

	var scores Scores
	if err := json.Unmarshal([]byte(jsonStr), &scores); err != nil {
		return nil, fmt.Errorf("JSON 解析に失敗: %w: %s", err, jsonStr)
	}
	return &scores, nil
}

// PrintReport は結果のサマリーを標準出力に表示する。
func PrintReport(results []Result) {
	var totalR, totalN, totalC, count int
	for _, r := range results {
		if r.Scores == nil {
			continue
		}
		totalR += r.Scores.Relevance
		totalN += r.Scores.Naturalness
		totalC += r.Scores.Character
		count++
	}

	fmt.Println("\n=== ベンチマーク結果 ===")
	fmt.Printf("テストケース: %d / %d 評価完了\n", count, len(results))
	if count > 0 {
		fmt.Printf("関連性:     %.1f / 5.0\n", float64(totalR)/float64(count))
		fmt.Printf("自然さ:     %.1f / 5.0\n", float64(totalN)/float64(count))
		fmt.Printf("キャラ一貫: %.1f / 5.0\n", float64(totalC)/float64(count))
	}

	fmt.Println("\n--- 詳細 ---")
	for _, r := range results {
		if r.Error != "" {
			fmt.Printf("[%s/%s] ERROR: %s\n", r.ScenarioName, r.CaseID, r.Error)
			continue
		}
		if r.Scores == nil {
			fmt.Printf("[%s/%s] 未評価\n", r.ScenarioName, r.CaseID)
			continue
		}
		fmt.Printf("[%s/%s] R=%d N=%d C=%d | %s\n",
			r.ScenarioName, r.CaseID,
			r.Scores.Relevance, r.Scores.Naturalness, r.Scores.Character,
			r.Scores.Reasoning)
		fmt.Printf("  prompt:   %s\n", r.Prompt)
		fmt.Printf("  response: %s\n", r.Response)
	}
}
