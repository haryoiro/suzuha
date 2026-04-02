package transcript

import "os"

// openFile は os.Open のラッパー。テストでモック可能にするため分離���
func openFile(path string) (*os.File, error) {
	return os.Open(path)
}
