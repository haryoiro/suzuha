package memento

// disambiguationRule は自己完結的で曖昧さのないメモリ内容を強制するルール。
// SimpleMemの「Force Disambiguation」手法に着想を得ている。
type disambiguationRule struct{}

// Disambiguation は曖昧さ排除ルールのシングルトンインスタンス。
var Disambiguation ExtractionRule = disambiguationRule{}

func (disambiguationRule) PromptSection() string {
	return `## 曖昧さ排除ルール（重要）
各メモリは単独で読んでも完全に意味が通る自己完結した文にすること。以下を徹底すること:

1. 代名詞禁止: 「彼」「彼女」「あの人」「それ」→ 具体的な名前またはユーザーIDに置き換える
   - 悪い例: 「彼はPythonが好き」
   - 良い例: 「user_id=12345のたろうはPythonが好き」

2. 相対時間禁止: 「昨日」「今日」「さっき」「この前」→ ISO 8601形式の絶対日時に置き換える
   - 悪い例: 「昨日カレーを食べた」
   - 良い例: 「2026-03-30にカレーを食べた」

3. 文脈依存禁止: 「あの話」「例の件」→ 具体的な内容を記述する
   - 会話のコンテキストが消えた後も、そのメモリだけで何のことか分かるように書く`
}
