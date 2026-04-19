package gateway

import "context"

// Source は外部プラットフォーム (Discord, CLI, Device 等) のライフサイクルを表す。
// 各アダプタがこのインターフェースを実装する。
type Source interface {
	// Name はこのソースの一意な識別子を返す (例: "discord", "cli", "device")。
	Name() string
	// Run はソースのイベントループを開始する。ctx がキャンセルされるまでブロックする。
	Run(ctx context.Context) error
}
