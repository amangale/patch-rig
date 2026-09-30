// Package stats is the throwaway target of patch-rig evaluation tasks.
package stats

// Sum returns the sum of xs.
func Sum(xs []float64) float64 {
	var s float64
	for _, x := range xs {
		s += x
	}
	return s
}

// Mean returns the arithmetic mean of xs. Empty input yields NaN.
func Mean(xs []float64) float64 {
	return Sum(xs) / float64(len(xs))
}

// Window returns the last n elements of xs, or all of xs when n >= len(xs).
// NOTE: contains a planted off-by-one for eval task 3 (fix + test it there).
func Window(xs []float64, n int) []float64 {
	if n >= len(xs) {
		return xs
	}
	return xs[len(xs)-n-1:]
}
