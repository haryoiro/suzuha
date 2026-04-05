package bench

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Scenario はベンチマークシナリオの定義。
type Scenario struct {
	Name        string      `yaml:"name"`
	Description string      `yaml:"description"`
	InjectLogs  []InjectLog `yaml:"inject_logs"`
	Cases       []TestCase  `yaml:"cases"`
}

// InjectLog はテスト前に会話ログとして注入するメッセージ。
type InjectLog struct {
	Role     string `yaml:"role"`
	UserName string `yaml:"user_name"`
	Content  string `yaml:"content"`
	Channel  string `yaml:"channel,omitempty"`
}

// TestCase は 1 つのテストプロンプトと期待される応答の特徴。
type TestCase struct {
	ID      string `yaml:"id"`
	Prompt  string `yaml:"prompt"`
	Source  string `yaml:"source"`            // "discord" or "internal"
	Channel string `yaml:"channel,omitempty"` // テスト時のチャンネル ID
	Expect  string `yaml:"expect"`            // 評価基準 (LLM-as-Judge に渡す)
	Turns   []Turn `yaml:"turns,omitempty"`   // マルチターン (設定時は Prompt を無視)
}

// Turn はマルチターン会話の 1 ターン。
type Turn struct {
	Prompt string `yaml:"prompt"`            // 固定プロンプト (空の場合は claude -p で生成)
	Goal   string `yaml:"goal"`              // このターンの目標/評価基準
}

// Result は 1 テストケースの実行結果。
type Result struct {
	ScenarioName string       `json:"scenario"`
	CaseID       string       `json:"case_id"`
	Prompt       string       `json:"prompt"`
	Response     string       `json:"response"`
	Expect       string       `json:"expect"`
	Scores       *Scores      `json:"scores,omitempty"`
	Error        string       `json:"error,omitempty"`
	TurnResults  []TurnResult `json:"turn_results,omitempty"` // マルチターン時
}

// TurnResult はマルチターン会話の 1 ターンの結果。
type TurnResult struct {
	TurnIndex int    `json:"turn"`
	Prompt    string `json:"prompt"`
	Response  string `json:"response"`
	Goal      string `json:"goal"`
}

// Scores は LLM-as-Judge の評価スコア。
type Scores struct {
	Relevance   int    `json:"relevance"`   // 1-5: プロンプトに対する関連性
	Naturalness int    `json:"naturalness"` // 1-5: 日本語としての自然さ
	Character   int    `json:"character"`   // 1-5: キャラクター一貫性 (IDENTITY.md 準拠)
	Reasoning   string `json:"reasoning"`   // 評価の根拠
}

// LoadScenario は YAML ファイルからシナリオを読み込む。
func LoadScenario(path string) (*Scenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("bench: シナリオ読み込みに失敗: %w", err)
	}
	var s Scenario
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("bench: シナリオ解析に失敗: %w", err)
	}
	return &s, nil
}

// LoadAllScenarios はディレクトリ内の全 YAML シナリオを読み込む。
func LoadAllScenarios(dir string) ([]*Scenario, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("bench: ディレクトリ読み込みに失敗: %w", err)
	}
	var scenarios []*Scenario
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		s, err := LoadScenario(dir + "/" + e.Name())
		if err != nil {
			return nil, err
		}
		scenarios = append(scenarios, s)
	}
	return scenarios, nil
}
