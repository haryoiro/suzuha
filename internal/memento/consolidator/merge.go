package consolidator

import (
	"sort"
	"strings"

	"github.com/haryoiro/suzuha/internal/memory"
)

// MergeSourceTag はメンテナンス統合で生成されたメモリに付与するメタデータタグ。
// DB に格納済みの値のため変更しないこと。
const MergeSourceTag = "consolidator_merge"

// mergeMemoryFields は複数のソースメモリからメタデータと構造化フィールドを統合し、
// 単一の統合メモリにまとめる。メモリの統合方法に関する唯一の正規実装。
func mergeMemoryFields(sources []memEntry) (map[string]any, []string, []string, string) {
	merged := map[string]any{"source": MergeSourceTag}

	participantSet := make(map[string]bool)
	personSet := make(map[string]bool)
	keywordSet := make(map[string]bool)
	var tones []string
	var userID string
	var topic string

	for _, src := range sources {
		// メタデータから参加者を収集する。
		if src.metadata != nil {
			switch v := src.metadata["participants"].(type) {
			case []any:
				for _, p := range v {
					if s, ok := p.(string); ok && s != "" {
						participantSet[s] = true
					}
				}
			case []string:
				for _, s := range v {
					if s != "" {
						participantSet[s] = true
					}
				}
			}
			if t, ok := src.metadata["emotional_tone"].(string); ok && t != "" {
				tones = append(tones, t)
			}
			if uid, ok := src.metadata["user_id"].(string); ok && uid != "" && userID == "" {
				userID = uid
			}
		}

		// 構造化フィールドを収集する。
		for _, p := range src.persons {
			if p != "" {
				personSet[p] = true
			}
		}
		for _, k := range src.keywords {
			if k != "" {
				keywordSet[k] = true
			}
		}
		// 最新のエントリからトピックを使用する（時系列順にソートされているためスライスの最後）。
		if src.topic != "" {
			topic = src.topic
		}
	}

	if len(participantSet) > 0 {
		participants := sortedKeys(participantSet)
		merged["participants"] = participants
	}
	if len(tones) > 0 {
		merged["emotional_tone"] = strings.Join(tones, ",")
	}
	if userID != "" {
		merged["user_id"] = userID
	}

	persons := sortedKeys(personSet)
	keywords := sortedKeys(keywordSet)

	return merged, persons, keywords, topic
}

// buildMergedMemory は統合判定結果から新しいメモリを作成する。
func buildMergedMemory(d decision) *memory.Memory {
	metadata, persons, keywords, topic := mergeMemoryFields(d.sourceEntries)
	mem := &memory.Memory{
		Type:     d.groupType,
		Content:  d.mergedContent,
		Metadata: metadata,
		Persons:  persons,
		Keywords: keywords,
		Topic:    topic,
	}
	return mem
}

func sortedKeys(m map[string]bool) []string {
	result := make([]string, 0, len(m))
	for k := range m {
		result = append(result, k)
	}
	sort.Strings(result)
	return result
}
