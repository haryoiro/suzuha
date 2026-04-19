package llm

// Client はロール解決可能な LLM 呼び出しを抽象化する。
// scheduler の CronContext や behavior の task 側で LLM を受け取るときに
// concrete *llm.Client ではなくこの契約に依存させる。
type Client interface {
	For(role string) RoleClient
}

// RoleClient は解決済みロールでの LLM 呼び出し契約。
// Completer を埋め込むことで CompleteRaw を共通化する。
type RoleClient interface {
	Completer
}
