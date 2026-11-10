package configupdate

import "strings"

// DiffLine is one line of a diff: Op is ' ', '-' or '+'. A zero DiffLine
// stands for skipped unchanged lines.
type DiffLine struct {
	Op   byte
	Text string
}

// Diff returns the changed lines between two texts with up to context
// unchanged lines around each change.
func Diff(before, after string, context int) []DiffLine {
	a, b := splitLines(before), splitLines(after)
	// Longest common subsequence table (files are small).
	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var all []DiffLine
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		switch {
		case i < len(a) && j < len(b) && a[i] == b[j]:
			all = append(all, DiffLine{' ', a[i]})
			i++
			j++
		case i < len(a) && (j == len(b) || lcs[i+1][j] >= lcs[i][j+1]):
			all = append(all, DiffLine{'-', a[i]})
			i++
		default:
			all = append(all, DiffLine{'+', b[j]})
			j++
		}
	}

	// Keep changes and their context.
	keep := make([]bool, len(all))
	for k, l := range all {
		if l.Op != ' ' {
			for c := max(0, k-context); c <= min(len(all)-1, k+context); c++ {
				keep[c] = true
			}
		}
	}
	var out []DiffLine
	skipped := false
	for k, l := range all {
		if !keep[k] {
			skipped = true
			continue
		}
		if skipped && len(out) > 0 {
			out = append(out, DiffLine{})
		}
		skipped = false
		out = append(out, l)
	}
	return out
}

func splitLines(s string) []string {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
