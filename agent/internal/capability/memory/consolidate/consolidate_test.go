package consolidate

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/haryoiro/suzuha/internal/llm"
	"github.com/haryoiro/suzuha/internal/adapter/store/memory"
)

// --- モック ---

// mockAdminStore は consolidate テスト用の AdminStore モック。
type mockAdminStore struct {
	memory.AdminStore // 未実装メソッドは panic する（テスト対象外）
	memories          []memory.Memory
	embeddings        map[string][]float32
	deleted           []string
}

func (m *mockAdminStore) ListEmbeddedMemories(_ context.Context) ([]memory.Memory, error) {
	return m.memories, nil
}
func (m *mockAdminStore) ListAllEmbeddings(_ context.Context) (map[string][]float32, error) {
	return m.embeddings, nil
}
func (m *mockAdminStore) DeleteBatch(_ context.Context, ids []string) (int, error) {
	m.deleted = append(m.deleted, ids...)
	return len(ids), nil
}

// mockCompleter はLLMレスポンスを固定で返すモック。
type mockCompleter struct {
	response string
	err      error
}

func (m *mockCompleter) CompleteRaw(_ context.Context, _ []llm.RawMessage) (*llm.Response, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &llm.Response{Text: m.response}, nil
}

// mockSaveStore は Save を記録するモック。
type mockSaveStore struct {
	memory.Store // 未実装メソッドは panic する
	saved        []*memory.Memory
}

func (m *mockSaveStore) Save(_ context.Context, mem *memory.Memory) error {
	m.saved = append(m.saved, mem)
	return nil
}

// --- テストケース ---

func TestConsolidate_NilAdmin(t *testing.T) {
	c := &Consolidator{logger: slog.Default()}
	_, err := c.Consolidate(context.Background(), &ConsolidateOpts{})
	if err == nil {
		t.Error("AdminStore nil でエラーを期待")
	}
}

func TestConsolidate_SingleEntry(t *testing.T) {
	admin := &mockAdminStore{
		memories: []memory.Memory{
			{ID: "a", Type: memory.MemoryTypeUser, Content: "テスト", CreatedAt: time.Now()},
		},
	}
	c := &Consolidator{admin: admin, logger: slog.Default()}
	result, err := c.Consolidate(context.Background(), &ConsolidateOpts{SimilarityThreshold: 0.3, MaxGroupSize: 8})
	if err != nil {
		t.Fatal(err)
	}
	if result.Groups != 0 {
		t.Errorf("1件では重複グループなし, got %d", result.Groups)
	}
}

func TestConsolidate_KeepAction(t *testing.T) {
	now := time.Now()
	admin := &mockAdminStore{
		memories: []memory.Memory{
			{ID: "a", Type: memory.MemoryTypeUser, Content: "Goが好き", CreatedAt: now},
			{ID: "b", Type: memory.MemoryTypeUser, Content: "Go言語が好き", CreatedAt: now.Add(time.Minute)},
		},
		embeddings: map[string][]float32{
			"a": {1, 0, 0},
			"b": {0.99, 0.1, 0}, // a に非常に近い
		},
	}
	store := &mockSaveStore{}
	mc := &mockCompleter{
		response: `[{"group":1,"action":"keep","keep_id":"b","reason":"bの方が詳しい"}]`,
	}
	c := &Consolidator{llm: mc, store: store, admin: admin, logger: slog.Default()}

	result, err := c.Consolidate(context.Background(), &ConsolidateOpts{
		SimilarityThreshold: 0.3,
		MaxGroupSize:        8,
		MaxGroupsPerLLMCall: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalDeleted != 1 {
		t.Errorf("1件削除期待, got %d", result.TotalDeleted)
	}
	if len(admin.deleted) != 1 || admin.deleted[0] != "a" {
		t.Errorf("ID=a の削除を期待, got %v", admin.deleted)
	}
	if len(store.saved) != 0 {
		t.Error("keep アクションでは Save は呼ばれないべき")
	}
}

func TestConsolidate_MergeAction(t *testing.T) {
	now := time.Now()
	admin := &mockAdminStore{
		memories: []memory.Memory{
			{ID: "a", Type: memory.MemoryTypeUser, Content: "Goが好き", CreatedAt: now, Persons: []string{"123"}},
			{ID: "b", Type: memory.MemoryTypeUser, Content: "Pythonも好き", CreatedAt: now.Add(time.Minute), Persons: []string{"123"}},
		},
		embeddings: map[string][]float32{
			"a": {1, 0, 0},
			"b": {0.99, 0.1, 0},
		},
	}
	store := &mockSaveStore{}
	mc := &mockCompleter{
		response: `[{"group":1,"action":"merge","merged_content":"GoとPythonが好き","reason":"統合"}]`,
	}
	c := &Consolidator{llm: mc, store: store, admin: admin, logger: slog.Default()}

	result, err := c.Consolidate(context.Background(), &ConsolidateOpts{
		SimilarityThreshold: 0.3,
		MaxGroupSize:        8,
		MaxGroupsPerLLMCall: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalMerged != 1 {
		t.Errorf("1件統合期待, got %d", result.TotalMerged)
	}
	if len(store.saved) != 1 {
		t.Fatalf("1件の Save 期待, got %d", len(store.saved))
	}
	if store.saved[0].Content != "GoとPythonが好き" {
		t.Errorf("統合内容が保存されるべき, got %q", store.saved[0].Content)
	}
}

func TestConsolidate_DryRun(t *testing.T) {
	now := time.Now()
	admin := &mockAdminStore{
		memories: []memory.Memory{
			{ID: "a", Type: memory.MemoryTypeUser, Content: "test1", CreatedAt: now},
			{ID: "b", Type: memory.MemoryTypeUser, Content: "test2", CreatedAt: now},
		},
		embeddings: map[string][]float32{
			"a": {1, 0, 0},
			"b": {1, 0, 0},
		},
	}
	store := &mockSaveStore{}
	mc := &mockCompleter{
		response: `[{"group":1,"action":"keep","keep_id":"a","reason":"test"}]`,
	}
	c := &Consolidator{llm: mc, store: store, admin: admin, logger: slog.Default()}

	result, err := c.Consolidate(context.Background(), &ConsolidateOpts{
		SimilarityThreshold: 0.3,
		MaxGroupSize:        8,
		MaxGroupsPerLLMCall: 5,
		DryRun:              true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(admin.deleted) != 0 {
		t.Error("dry run では削除されないべき")
	}
	if result.TotalDeleted != 0 {
		t.Error("dry run では削除カウント0")
	}
}

func TestConsolidate_LLMError(t *testing.T) {
	now := time.Now()
	admin := &mockAdminStore{
		memories: []memory.Memory{
			{ID: "a", Type: memory.MemoryTypeUser, Content: "test1", CreatedAt: now},
			{ID: "b", Type: memory.MemoryTypeUser, Content: "test2", CreatedAt: now},
		},
		embeddings: map[string][]float32{
			"a": {1, 0, 0},
			"b": {1, 0, 0},
		},
	}
	mc := &mockCompleter{err: context.DeadlineExceeded}
	c := &Consolidator{llm: mc, store: &mockSaveStore{}, admin: admin, logger: slog.Default()}

	result, err := c.Consolidate(context.Background(), &ConsolidateOpts{
		SimilarityThreshold: 0.3,
		MaxGroupSize:        8,
		MaxGroupsPerLLMCall: 5,
	})
	// LLM エラーはバッチ単位で continue するためパイプライン自体はエラーにならない。
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalDeleted != 0 && result.TotalMerged != 0 {
		t.Error("LLM エラー時は何もされないべき")
	}
}
