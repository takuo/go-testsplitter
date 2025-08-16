package pkg2

import (
	"testing"
	"time"
)

func TestReverse(t *testing.T) {
	// Simulate very slow test - 200ms
	time.Sleep(200 * time.Millisecond)

	result := Reverse("hello")
	if result != "olleh" {
		t.Errorf("Expected 'olleh', got '%s'", result)
	}
}

func TestReverseEmpty(t *testing.T) {
	// Simulate fast test - 20ms
	time.Sleep(20 * time.Millisecond)

	result := Reverse("")
	if result != "" {
		t.Errorf("Expected empty string, got '%s'", result)
	}
}

func TestToUpper(t *testing.T) {
	// Simulate medium test - 75ms
	time.Sleep(75 * time.Millisecond)

	result := ToUpper("hello")
	if result != "HELLO" {
		t.Errorf("Expected 'HELLO', got '%s'", result)
	}
}
