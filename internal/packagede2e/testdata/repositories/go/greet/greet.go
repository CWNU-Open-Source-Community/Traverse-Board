package greet

// Greeting returns the user-visible greeting used by the packaged E2E fixture.
func Greeting(name string) string {
	return "hello " + name
}
