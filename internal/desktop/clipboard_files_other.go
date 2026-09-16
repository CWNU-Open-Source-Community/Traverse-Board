//go:build !windows

package desktop

// Other platforms retain the standard browser paste/file-picker path.
func NewNativeClipboardFileReader() ClipboardFileReader { return nil }
