package agent

// SourceRegistration はソースごとの Agent 設定を持つ。
// Gateway にソースを登録する際に、対応する Agent 側の設定をこの構造体で渡す。
type SourceRegistration struct {
	// Key はこのソースの SourceKey (例: SourceKeyDiscord)。
	Key SourceKey
	// NewSession はソース用の Session を生成するファクトリ関数。
	// agentCtx はこのソース専用の会話コンテキスト。
	NewSession func(agentCtx *Context) Session
	// Directive はこのソース固有のパイプライン設定。
	Directive DirectiveConfig
	// PersistKey はコンテキスト永続化に使う DB キー。空の場合は Key を使う。
	PersistKey string
}
