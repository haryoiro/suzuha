package llm

import (
	"context"

	"github.com/mozilla-ai/any-llm-go/providers"
)

// Client はロール解決可能な LLM 呼び出しを抽象化する。
// runtime/agent / behavior の task が concrete *llm.Client ではなくこの契約に
// 依存することで、capability/llm の具象実装を runtime 層から切り離す。
type Client interface {
	// For は指定ロールの RoleClient を返す。ロール未設定でも nil ではなく
	// 空ロール実装を返し、呼び出し側でエラーを拾えるよう defensive にする。
	For(role string) RoleClient

	// WithCapability は指定ロールが capability を持つ場合に RoleClient と true を返す。
	// 持たない場合は fallback チェインを辿らず (nil, false) を返す。
	WithCapability(role, capability string) (RoleClient, bool)

	// DescribeImage は画像 URL (または data URI) を vision capable な LLM で説明する。
	// prompts は任意の追加プロンプト。
	DescribeImage(ctx context.Context, imageURL string, prompts ...string) (string, error)
}

// RoleClient は解決済みロールでの LLM 呼び出し契約。
// Completer を埋め込むことで CompleteRaw を共通化する。
type RoleClient interface {
	Completer

	// MaxContextTokens はこのロールで使用可能な最大コンテキストトークン数を返す。
	MaxContextTokens() int

	// HasCapability はこのロールが指定 capability を持つかを返す。
	HasCapability(capability string) bool

	// ProviderName はこのロールに割り当てられたプロバイダ名を返す。
	ProviderName() string

	// Model はこのロールに割り当てられたモデル名を返す。
	Model() string

	// CompleteWithTools はツール定義付きの同期補完を実行する。
	CompleteWithTools(ctx context.Context, messages []RawMessage, tools []providers.Tool) (*Response, error)

	// CompleteStreamWithTools は streaming 補完を実行し、チャンクとエラーのチャネルを返す。
	// 呼び出し側は両チャネルが閉じるまで受信する。
	CompleteStreamWithTools(ctx context.Context, messages []RawMessage, tools []providers.Tool) (<-chan StreamChunk, <-chan error)
}
