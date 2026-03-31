package consolidator

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/memory"
	"github.com/mozilla-ai/any-llm-go/providers"
)

// completer はLLM補完呼び出しを抽象化するインターフェース。
// *llm.Client が実装する。テストではモックに差し替え可能。
type completer interface {
	CompleteRawDefault(ctx context.Context, msgs []providers.Message) (*llm.Response, error)
}

// Server は Client および Maintainer インターフェースを実装する。
// メモリの全ライフサイクルを管理する: 抽出（書き込みパス）とメンテナンス（バックグラウンドパス）。
type Server struct {
	llmClient completer
	store     memory.Store
	admin     memory.AdminStore // Maintain の DeleteBatch で必要; nil の場合あり
	config    Config
	logger    *slog.Logger
}

// NewServer はコンソリデーターサーバーを作成する。
func NewServer(llmClient *llm.Client, store memory.Store, cfg Config, logger *slog.Logger) *Server {
	return &Server{
		llmClient: llmClient,
		store:     store,
		config:    cfg,
		logger:    logger,
	}
}

// SetAdminStore はメンテナンス操作用のAdminStoreを設定する。
func (s *Server) SetAdminStore(admin memory.AdminStore) {
	s.admin = admin
}

// Compact は指定されたメッセージから長期メモリを抽出する。
func (s *Server) Compact(ctx context.Context, req *CompactRequest) (*CompactResult, error) {
	if len(req.Messages) == 0 {
		return &CompactResult{}, nil
	}

	// 抽出パイプラインを実行: コンテキスト取得 → LLM → パース → メディア添付。
	memories, err := s.extract(ctx, req.Messages)
	if err != nil {
		return nil, fmt.Errorf("consolidator: 抽出に失敗: %w", err)
	}

	// バッチ重複チェック — 全候補に対して1回の埋め込みAPI呼び出し。
	candidates := make([]memory.DupCandidate, len(memories))
	for i, mem := range memories {
		candidates[i] = memory.DupCandidate{Content: mem.Content, Type: mem.Type}
	}
	dupResults, dupErr := s.store.IsDuplicateBatch(ctx, candidates)
	if dupErr != nil {
		s.logger.Warn("consolidator: バッチ重複チェックに失敗", "error", dupErr)
	}

	// 重複でないメモリを保存し、重複チェックで得た埋め込みを再利用する。
	result := &CompactResult{}
	for i := range memories {
		mem := &memories[i]
		if dupResults != nil && dupResults[i].DupID != "" {
			s.logger.Debug("consolidator: 重複メモリをスキップ", "existing_id", dupResults[i].DupID, "content", mem.Content)
			continue
		}
		if dupResults != nil && len(dupResults[i].Embedding) > 0 {
			mem.Embedding = dupResults[i].Embedding
		}
		if err := s.store.Save(ctx, mem); err != nil {
			s.logger.Warn("consolidator: メモリの保存に失敗", "error", err)
		}
		result.Memories = append(result.Memories, *mem)
	}

	return result, nil
}
