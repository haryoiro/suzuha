package acquire

import "time"

// ExtractionRule はメモリ抽出パイプラインに寄与するインターフェース。
type ExtractionRule interface {
	PromptSection() string
}

// Config は獲得フェーズの動作を制御する設定。
type Config struct {
	RecentMemoryLimit  int
	RecentMemoryWindow time.Duration
	Rules              []ExtractionRule
}

// DefaultConfig はデフォルト設定を返す。
func DefaultConfig() Config {
	return Config{
		RecentMemoryLimit:  10,
		RecentMemoryWindow: 6 * time.Hour,
		Rules:              []ExtractionRule{Disambiguation},
	}
}
