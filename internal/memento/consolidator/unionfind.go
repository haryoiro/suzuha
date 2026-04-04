package consolidator

// unionFind implements a disjoint-set data structure with path compression
// and union by rank for grouping similar memories.
type unionFind struct {
	parent []int
	rank   []int
}

func newUnionFind(n int) *unionFind {
	parent := make([]int, n)
	rank := make([]int, n)
	for i := range parent {
		parent[i] = i
	}
	return &unionFind{parent: parent, rank: rank}
}

func (uf *unionFind) find(x int) int {
	if uf.parent[x] != x {
		uf.parent[x] = uf.find(uf.parent[x])
	}
	return uf.parent[x]
}

func (uf *unionFind) union(x, y int) {
	rx, ry := uf.find(x), uf.find(y)
	if rx == ry {
		return
	}
	if uf.rank[rx] < uf.rank[ry] {
		rx, ry = ry, rx
	}
	uf.parent[ry] = rx
	if uf.rank[rx] == uf.rank[ry] {
		uf.rank[rx]++
	}
}

// groups returns all connected components with 2+ members.
// Keys are root indices, values are slices of member indices.
func (uf *unionFind) groups() map[int][]int {
	all := make(map[int][]int)
	for i := range uf.parent {
		root := uf.find(i)
		all[root] = append(all[root], i)
	}
	result := make(map[int][]int)
	for root, members := range all {
		if len(members) >= 2 {
			result[root] = members
		}
	}
	return result
}
