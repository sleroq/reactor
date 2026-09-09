package monitor

import (
	"slices"
	"testing"
)

func TestMessageIDBatches(t *testing.T) {
	got := messageIDBatches([2]int{597560, 597660})
	if len(got) != 2 {
		t.Fatalf("messageIDBatches() returned %d batches, want 2", len(got))
	}

	wantFirst := make([]int, 100)
	for i := range wantFirst {
		wantFirst[i] = 597560 + i
	}
	if !slices.Equal(got[0], wantFirst) {
		t.Errorf("first batch = %v, want %v", got[0], wantFirst)
	}
	if wantLast := []int{597660}; !slices.Equal(got[1], wantLast) {
		t.Errorf("last batch = %v, want %v", got[1], wantLast)
	}
}
