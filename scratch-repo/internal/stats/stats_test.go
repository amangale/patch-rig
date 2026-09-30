package stats

import (
	"math"
	"testing"
)

func TestSum(t *testing.T) {
	tests := []struct {
		name string
		in   []float64
		want float64
	}{
		{"empty", nil, 0},
		{"single", []float64{42}, 42},
		{"many", []float64{1, 2, 3, 4}, 10},
	}
	for _, tt := range tests {
		if got := Sum(tt.in); got != tt.want {
			t.Errorf("%s: Sum(%v) = %v, want %v", tt.name, tt.in, got, tt.want)
		}
	}
}

func TestMean(t *testing.T) {
	if got := Mean([]float64{2, 4}); got != 3 {
		t.Errorf("Mean = %v, want 3", got)
	}
	if !math.IsNaN(Mean(nil)) {
		t.Errorf("Mean(nil) should be NaN")
	}
}
