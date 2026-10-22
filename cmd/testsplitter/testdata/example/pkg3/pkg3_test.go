package pkg3

import (
	"fmt"
	"math"
	"testing"
	"time"
)

func TestMax(t *testing.T) {
	// Simulate medium-slow test - 120ms
	time.Sleep(120 * time.Millisecond)

	result := Max(5, 3)
	if result != 5 {
		t.Errorf("Expected 5, got %d", result)
	}
}

func TestMaxEqual(t *testing.T) {
	// Simulate very fast test - 3ms
	time.Sleep(3 * time.Millisecond)

	result := Max(5, 5)
	if result != 5 {
		t.Errorf("Expected 5, got %d", result)
	}
}

func TestMin(t *testing.T) {
	// Simulate slow test - 150ms
	time.Sleep(150 * time.Millisecond)

	result := Min(5, 3)
	if result != 3 {
		t.Errorf("Expected 3, got %d", result)
	}
}

func TestAbs(t *testing.T) {
	// Simulate fast test - 30ms
	time.Sleep(30 * time.Millisecond)

	result := Abs(-5)
	if result != 5 {
		t.Errorf("Expected 5, got %d", result)
	}
}

func TestAbsPositive(t *testing.T) {
	// Simulate very fast test - 8ms
	time.Sleep(8 * time.Millisecond)

	result := Abs(5)
	if result != 5 {
		t.Errorf("Expected 5, got %d", result)
	}
}

func ExampleMax() {
	fmt.Println(Max(5, 3))
	// Output: 5
}

// ExampleMin has no output comment, so `go test` compiles it without running it.
func ExampleMin() {
	fmt.Println(Min(5, 3))
}

func FuzzAbs(f *testing.F) {
	f.Add(-5)
	f.Fuzz(func(t *testing.T, x int) {
		if x != math.MinInt && Abs(x) < 0 {
			t.Errorf("Abs(%d) is negative", x)
		}
	})
}
