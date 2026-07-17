package bundle

import "fmt"

// Warning is a non-fatal advisory raised while loading a bundle directory: an
// unknown top-level directory (with a typo suggestion when one is close), or an
// empty convention directory. Warnings never fail a load on their own — the
// command layer prints them, and `bundle validate --strict` elevates them to
// errors.
type Warning struct {
	// Message is the fully-composed advisory text.
	Message string
}

func (w Warning) String() string { return w.Message }

// unknownDirWarning builds the advisory for a top-level directory that is
// neither the bundle marker nor a convention directory. When the name is a
// near-miss of a convention directory, the suggestion is appended.
func unknownDirWarning(dir string) Warning {
	msg := fmt.Sprintf("unknown top-level directory %q — bundle components live under %s/, %s/, or %s/",
		dir, harnessesDir, stacksDir, monitoringDir)
	if suggestion, ok := suggestConventionDir(dir); ok {
		msg += fmt.Sprintf(" (did you mean %s/?)", suggestion)
	}
	return Warning{Message: msg}
}

// emptyComponentDirWarning builds the advisory for a convention directory that
// exists but holds no component subdirectories — a bundle that ships nothing of
// that type.
func emptyComponentDirWarning(t ComponentType) Warning {
	return Warning{Message: fmt.Sprintf("convention directory %s/ is empty — no %s components", t.Dir(), t)}
}

// suggestConventionDir returns the convention directory name closest to dir when
// it is within a small edit distance (a plausible typo), else ("", false).
func suggestConventionDir(dir string) (string, bool) {
	// Only suggest a genuine near-miss: within a third of the candidate's
	// length (at least 1), never a match of an unrelated word.
	const nearMissDivisor = 3
	best := ""
	bestDist := 0
	for _, candidate := range []string{harnessesDir, stacksDir, monitoringDir} {
		d := levenshtein(dir, candidate)
		threshold := max(len(candidate)/nearMissDivisor, 1)
		if d <= threshold && (best == "" || d < bestDist) {
			best, bestDist = candidate, d
		}
	}
	return best, best != ""
}

// levenshtein computes the edit distance between two strings — the classic
// two-row dynamic-programming form, sufficient for the short directory names it
// compares.
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	curr := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		curr[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			curr[j] = min(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(rb)]
}
