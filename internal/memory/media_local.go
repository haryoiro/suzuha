package memory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// LocalMediaStore implements MediaStore using the local filesystem.
// Keys are treated as relative paths under the base directory.
type LocalMediaStore struct {
	baseDir string
}

// NewLocalMediaStore creates a MediaStore backed by the local filesystem.
// baseDir is created if it doesn't exist.
func NewLocalMediaStore(baseDir string) (*LocalMediaStore, error) {
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return nil, fmt.Errorf("media: ディレクトリの作成に失敗: %w", err)
	}
	return &LocalMediaStore{baseDir: baseDir}, nil
}

func (s *LocalMediaStore) Put(_ context.Context, key string, data []byte) error {
	path := filepath.Join(s.baseDir, filepath.Clean(key))
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("media: ディレクトリの作成に失敗: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

func (s *LocalMediaStore) Get(_ context.Context, key string) ([]byte, error) {
	path := filepath.Join(s.baseDir, filepath.Clean(key))
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, err
		}
		return nil, fmt.Errorf("media: 読み取りに失敗: %w", err)
	}
	return data, nil
}

func (s *LocalMediaStore) Delete(_ context.Context, key string) error {
	path := filepath.Join(s.baseDir, filepath.Clean(key))
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("media: 削除に失敗: %w", err)
	}
	return nil
}

var _ MediaStore = (*LocalMediaStore)(nil)
