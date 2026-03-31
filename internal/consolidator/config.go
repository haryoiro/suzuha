package consolidator

import "time"

// ExtractionRule はメモリ抽出パイプラインに寄与するインターフェース。
// 現時点ではプロンプトセクションの提供のみ。将来的にバリデーション
// メソッド（例: PostExtract）を追加しても既存ルールを壊さない設計。
type ExtractionRule interface {
	// PromptSection はシステムプロンプトに追加するプロンプトセクションを返す。
	// スキップする場合は空文字列を返す。
	PromptSection() string
}

// ExtractionConfig はメモリ抽出の動作を制御する設定。
type ExtractionConfig struct {
	// RecentMemoryLimit は重複排除コンテキストとして取得する既存メモリの最大件数。
	// 0の場合コンテキスト取得を無効にする。
	RecentMemoryLimit int

	// RecentMemoryWindow はコンテキストメモリを取得する際の遡り期間。
	RecentMemoryWindow time.Duration

	// Rules はシステムプロンプトに追加される抽出ルール。
	Rules []ExtractionRule
}

// MaintainConfig は定期メモリメンテナンスの動作を制御する設定。
type MaintainConfig struct {
	SimilarityThreshold float64 `json:"similarity_threshold"`
	MaxGroupSize        int     `json:"max_group_size"`
	MaxGroupsPerLLMCall int     `json:"max_groups_per_llm_call"`
	DryRun              bool    `json:"dry_run"`
}

// Config は抽出とメンテナンスの設定を統合した構造体。
type Config struct {
	Extraction ExtractionConfig
	Maintain   MaintainConfig
}

// DefaultConfig はデフォルト設定を返す。
func DefaultConfig() Config {
	return Config{
		Extraction: ExtractionConfig{
			RecentMemoryLimit:  10,
			RecentMemoryWindow: 6 * time.Hour,
			Rules:              []ExtractionRule{Disambiguation},
		},
		Maintain: MaintainConfig{
			SimilarityThreshold: 0.3,
			MaxGroupSize:        8,
			MaxGroupsPerLLMCall: 5,
		},
	}
}
