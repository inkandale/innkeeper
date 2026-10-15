package actions

import (
	"sync"
	"testing"
	"time"
)

func TestProgressSaverSavesAtMostOncePerInterval(t *testing.T) {
	var mu sync.Mutex
	var saves []time.Time
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(saves)
	}
	p := &progressSaver{every: 50 * time.Millisecond, save: func() {
		mu.Lock()
		saves = append(saves, time.Now())
		mu.Unlock()
	}}
	waitFor := func(n int) {
		t.Helper()
		for deadline := time.Now().Add(2 * time.Second); count() < n; time.Sleep(time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("saves = %d, want %d", count(), n)
			}
		}
	}

	p.poke()
	waitFor(1)
	for range 20 {
		p.poke()
	}
	if count() != 1 {
		t.Fatalf("saves = %d right after a burst, want 1", count())
	}
	// The burst's latest progress still gets saved, one interval later.
	waitFor(2)
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(saves) != 2 || saves[1].Sub(saves[0]) < 50*time.Millisecond {
		t.Fatalf("saves = %v", saves)
	}
}
