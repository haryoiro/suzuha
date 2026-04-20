// Package memory は長期記憶 (memo) の永続化実装。
// data 型は domain/memo/ の正準定義を再エクスポートし、同 package 内の
// postgres*.go / media_local.go から bare 名 (Memory, MemoryType 等) で
// 参照できるようにする。
// 旧 internal/memory/ の公開 API は全て本 package に統合済み。
package memory

import (
	"context"

	"github.com/haryoiro/suzuha/internal/domain/memo"
	embedding "github.com/haryoiro/suzuha/internal/port/embedder"
)

// embedder 入力型の再エクスポート (SearchByParts 等で使う)。
type (
	Part     = embedding.Part
	Modality = embedding.Modality
)

const (
	ModalityText  = embedding.ModalityText
	ModalityImage = embedding.ModalityImage
	ModalityAudio = embedding.ModalityAudio
)

// domain/memo のデータ型を本 package からも直接参照できるよう alias 化。
type (
	Memory         = memo.Memory
	MemoryType     = memo.MemoryType
	Attachment     = memo.Attachment
	SymbolicFilter = memo.SymbolicFilter
	DupCandidate   = memo.DupCandidate
	DupResult      = memo.DupResult
	DuplicateGroup = memo.DuplicateGroup
	ListOpts       = memo.ListOpts
)

// MemoryType 定数も bare で使えるように再エクスポート。
const (
	MemoryTypeUser    = memo.MemoryTypeUser
	MemoryTypeWorld   = memo.MemoryTypeWorld
	MemoryTypeTool    = memo.MemoryTypeTool
	MemoryTypeEpisode = memo.MemoryTypeEpisode
	MemoryTypeSelf    = memo.MemoryTypeSelf
)

// MediaStore は binary 添付の put/get 契約 (consumer-side interface)。
// LocalMediaStore が実装する。上位層では port/memory.Media を使う。
type MediaStore interface {
	Put(ctx context.Context, key string, data []byte) error
	Get(ctx context.Context, key string) ([]byte, error)
	Delete(ctx context.Context, key string) error
}
