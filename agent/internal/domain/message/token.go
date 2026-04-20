package message

// TokenCounter はテキストをトークン数に変換する関数。
//
// capability/llm が provider / model ごとに実装を返す (port/llm.TokenCounterFactory)。
// domain 層では Message のトークン数計算の署名として扱う。
type TokenCounter func(text string) int

// CountMessages はメッセージ列のトークン数を合計する。
// メッセージごとに role overhead (+4) を加算し、tool_calls の関数名と
// 引数 JSON もトークン数に含める。
//
// counter が nil なら 0 を返す。
func CountMessages(counter TokenCounter, msgs []Message) int {
	if counter == nil {
		return 0
	}
	total := 0
	for _, m := range msgs {
		total += counter(m.Content) + 4
		for _, tc := range m.ToolCalls {
			total += counter(tc.Function.Name) + counter(tc.Function.Arguments)
		}
	}
	return total
}
