package greet

import "testing"

func TestGreetingPreservesUnicodeName(t *testing.T) {
	if got, want := Greeting("世界"), "Hello, 世界!"; got != want {
		t.Fatalf("Greeting() = %q, want %q", got, want)
	}
}
