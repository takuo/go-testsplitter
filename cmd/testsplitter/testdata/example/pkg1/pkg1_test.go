package pkg1

import (
	"testing"
	"time"
)

func TestAdd(t *testing.T) {
	// Simulate slow test - 100ms
	time.Sleep(100 * time.Millisecond)

	result := Add(2, 3)
	if result != 5 {
		t.Errorf("Expected 5, got %d", result)
	}
}

func TestAddNegative(t *testing.T) {
	// Simulate fast test - 10ms
	time.Sleep(10 * time.Millisecond)

	result := Add(-1, 1)
	if result != 0 {
		t.Errorf("Expected 0, got %d", result)
	}
}

func TestMultiply(t *testing.T) {
	// Simulate medium test - 50ms
	time.Sleep(50 * time.Millisecond)

	result := Multiply(3, 4)
	if result != 12 {
		t.Errorf("Expected 12, got %d", result)
	}
}

func TestMultiplyZero(t *testing.T) {
	// Simulate very fast test - 5ms
	time.Sleep(5 * time.Millisecond)

	result := Multiply(5, 0)
	if result != 0 {
		t.Errorf("Expected 0, got %d", result)
	}
}
