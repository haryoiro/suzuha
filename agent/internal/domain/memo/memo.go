// Package memo は長期記憶 (memory entry) のドメイン型を定義する。
// admin API / capability/memory / behavior / tool から参照される。
// 現行コードの `memory.Memory` 等は本 package の型を型エイリアスで温存する。
package memo

import "time"

// MemoryType は長期記憶エントリの分類。
type MemoryType string

// MemoryType 定数。
const (
	MemoryTypeUser    MemoryType = "user"
	MemoryTypeWorld   MemoryType = "world"
	MemoryTypeTool    MemoryType = "tool"
	MemoryTypeEpisode MemoryType = "episode"
	MemoryTypeSelf    MemoryType = "self"
)

// Attachment は MediaStore に格納された media ファイルへの参照。
type Attachment struct {
	Key      string `json:"key"`       // "memories/abc123/0.png" 等のストレージキー
	Modality string `json:"modality"`  // "image" / "audio"
	MimeType string `json:"mime_type"` // "image/png" / "audio/wav" 等
}

// Memory は 1 件の長期記憶エントリ。
type Memory struct {
	ID          string         `json:"id"`
	Type        MemoryType     `json:"type"`
	Content     string         `json:"content"`
	Attachments []Attachment   `json:"attachments,omitempty"`
	Embedding   []float32      `json:"embedding,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`

	// 構造化フィールド — コンソリデーターが抽出し、検索・フィルタリングに使う。
	Keywords  []string   `json:"keywords,omitempty"`   // 検索キーワード (名前 / エンティティ / トピック語)
	Topic     string     `json:"topic,omitempty"`      // トピック分類 ("技術/Go", "日常/食事" 等)
	Persons   []string   `json:"persons,omitempty"`    // 関連ユーザー ID (user_id + participants 統合)
	EventTime *time.Time `json:"event_time,omitempty"` // イベント発生日時 (CreatedAt とは別)
}

// SymbolicFilter は Symbolic 検索 (メタデータベース) の制約。
// ゼロ値は「そのフィールドでフィルタしない」。
type SymbolicFilter struct {
	PersonIDs   []string  // persons にいずれかが含まれるメモリにマッチ
	TopicPrefix string    // topic がこのプレフィックスで始まるメモリにマッチ
	Since       time.Time // event_time >= Since (nil なら created_at)
}

// IsEmpty は一切フィルタ指定が無いとき true。
func (f SymbolicFilter) IsEmpty() bool {
	return len(f.PersonIDs) == 0 && f.TopicPrefix == "" && f.Since.IsZero()
}

// DupCandidate は IsDuplicateBatch の単一入力。
type DupCandidate struct {
	Content string
	Type    MemoryType
}

// DupResult は IsDuplicateBatch の単一出力。
type DupResult struct {
	DupID     string    // 非空なら重複が見つかった
	Embedding []float32 // 算出済み埋め込み (FTS で拾われた場合は nil)
}

// DuplicateGroup は KNN / cosine で近似とみなされた Memory の束。
type DuplicateGroup struct {
	Memories []Memory `json:"memories"`
}

// ListOpts は admin List のページング + フィルタ。
type ListOpts struct {
	Offset   int
	Limit    int
	Type     MemoryType // 空なら全タイプ
	Query    string     // FTS 検索クエリ。空ならフィルタなし
	OrderBy  string     // "created_at" | "updated_at" (default "updated_at")
	OrderDir string     // "asc" | "desc" (default "desc")
}

// ConsolidateOpts は 1 回の統合 (重複排除) 実行を制御するオプション。
// capability/memory/consolidate の実装と capability/memory/forget のタスクが共有する。
type ConsolidateOpts struct {
	SimilarityThreshold float64 `json:"similarity_threshold"`
	MaxGroupSize        int     `json:"max_group_size"`
	MaxGroupsPerLLMCall int     `json:"max_groups_per_llm_call"`
	DryRun              bool    `json:"dry_run"`
}

// ConsolidateResult は統合中に行われた処理の結果を報告する。
type ConsolidateResult struct {
	Groups       int
	TotalDeleted int
	TotalMerged  int
}
