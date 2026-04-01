package memento

import "time"

// ExtractionRule はメモリ抽出パイプラインに寄与するインターフェース。
// 現時点ではプロンプトセクションの提供のみ。将来的にバリデーション
// メソッド（例: PostExtract）を追加しても既存ルールを壊さない設計。
type ExtractionRule interface {
	// PromptSection はシステムプロンプトに追加するプロンプトセクションを返す。
	// スキップする場合は空文字列を返す。
	PromptSection() string
}

// AcquireConfig は獲得フェーズの動作を制御する設定。
type AcquireConfig struct {
	// RecentMemoryLimit は重複排除コンテキストとして取得する既存メモリの最大件数。
	// 0の場合コンテキスト取得を無効にする。
	RecentMemoryLimit int

	// RecentMemoryWindow はコンテキストメモリを取得する際の遡り期間。
	RecentMemoryWindow time.Duration

	// Rules はシステムプロンプトに追加される抽出ルール。
	Rules []ExtractionRule
}

// DefaultAcquireConfig はデフォルト設定を返す。
func DefaultAcquireConfig() AcquireConfig {
	return AcquireConfig{
		RecentMemoryLimit:  10,
		RecentMemoryWindow: 6 * time.Hour,
		Rules:              []ExtractionRule{Disambiguation},
	}
}
