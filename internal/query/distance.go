package query

// editDistance returns the Levenshtein distance between a and b compared
// byte by byte, or limit+1 as soon as it is known to exceed limit: the
// comparison of an address with the names of a workspace stays linear in
// their number.
func editDistance(a, b string, limit int) int {
	if len(a) > len(b) {
		a, b = b, a
	}
	if len(b)-len(a) > limit {
		return limit + 1
	}
	prev := make([]int, len(a)+1)
	cur := make([]int, len(a)+1)
	for i := range prev {
		prev[i] = i
	}
	for j := 1; j <= len(b); j++ {
		cur[0] = j
		best := cur[0]
		for i := 1; i <= len(a); i++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[i] = min(prev[i]+1, cur[i-1]+1, prev[i-1]+cost)
			best = min(best, cur[i])
		}
		if best > limit {
			return limit + 1
		}
		prev, cur = cur, prev
	}
	return prev[len(a)]
}

// typoLimit is the edit distance within which a name of n bytes is
// offered for another: one edit for a short name, a quarter of a long
// one, three at most.
func typoLimit(n int) int { return max(1, min(3, n/4)) }
