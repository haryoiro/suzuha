package consolidator

import (
	"math"
	"sort"
)

// cosineDistance は2つのベクトル間のコサイン距離を返す（0=同一, 2=正反対）。
func cosineDistance(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 2.0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	denom := math.Sqrt(normA) * math.Sqrt(normB)
	if denom == 0 {
		return 2.0
	}
	return 1.0 - dot/denom
}

// buildSimilarityGroups は同一型内でコサイン距離に基づくクラスタリングを行い、
// メンバーが2件以上のグループをサイズ降順で返す。各グループは maxGroupSize で制限。
func buildSimilarityGroups(entries []memEntry, embeddings map[string][]float32, threshold float64, maxGroupSize int) []memoryGroup {
	uf := newUnionFind(len(entries))

	// 同一型ブロック内でのみ比較する（entries は type 順にソート済み）。
	typeStart := 0
	for typeStart < len(entries) {
		typeEnd := typeStart + 1
		for typeEnd < len(entries) && entries[typeEnd].memType == entries[typeStart].memType {
			typeEnd++
		}
		for i := typeStart; i < typeEnd; i++ {
			embI, okI := embeddings[entries[i].id]
			if !okI {
				continue
			}
			for j := i + 1; j < typeEnd; j++ {
				embJ, okJ := embeddings[entries[j].id]
				if !okJ {
					continue
				}
				if cosineDistance(embI, embJ) < threshold {
					uf.union(i, j)
				}
			}
		}
		typeStart = typeEnd
	}

	// メンバーが2件以上のグループを抽出する。
	rawGroups := uf.groups()
	var groups []memoryGroup
	for _, memberIndices := range rawGroups {
		g := memoryGroup{memType: entries[memberIndices[0]].memType}
		for _, idx := range memberIndices {
			g.members = append(g.members, entries[idx])
		}
		// 時系列順にソート。
		sort.Slice(g.members, func(i, j int) bool {
			return g.members[i].createdAt.Before(g.members[j].createdAt)
		})
		if len(g.members) > maxGroupSize {
			g.members = g.members[:maxGroupSize]
		}
		groups = append(groups, g)
	}

	// サイズ降順にソート（大きいグループを先に処理）。
	sort.Slice(groups, func(i, j int) bool {
		return len(groups[i].members) > len(groups[j].members)
	})

	return groups
}
