package runner

import (
	"bufio"
	"bytes"
	"io"
	"path/filepath"
	"strings"

	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

// Windows PowerShell's native startup error can be UTF-16LE without a BOM,
// before a script can set OutputEncoding. Match its exact prefix only on the
// resolved powershell.exe stderr; do not guess an ANSI codepage or reinterpret
// all Windows output as UTF-16. Normal command output remains UTF-8.
const commandRuntimePowerShellStartupPrefix = "W\x00i\x00n\x00d\x00o\x00w\x00s\x00 \x00P\x00o\x00w\x00e\x00r\x00S\x00h\x00e\x00l\x00l\x00 \x00"

func commandRuntimeLegacyPowerShellOutput(job CommandRuntimeJob, stream CommandRuntimeStream) bool {
	return stream == CommandRuntimeStderr && job.Profile == CommandRuntimePowerShell &&
		strings.EqualFold(filepath.Base(job.ExecutablePath), "powershell.exe")
}

// commandRuntimeTextReader preserves raw byte accounting in its input reader.
// The resulting UTF-8 must still pass through the existing redacting stream.
func commandRuntimeTextReader(input io.Reader, legacyPowerShell bool) io.Reader {
	buffer := bufio.NewReaderSize(input, len(commandRuntimePowerShellStartupPrefix))
	prefix, _ := buffer.Peek(2)
	if bytes.Equal(prefix, []byte{0xff, 0xfe}) {
		return transform.NewReader(buffer, unicode.UTF16(unicode.LittleEndian, unicode.ExpectBOM).NewDecoder())
	}
	if bytes.Equal(prefix, []byte{0xfe, 0xff}) {
		return transform.NewReader(buffer, unicode.UTF16(unicode.BigEndian, unicode.ExpectBOM).NewDecoder())
	}
	if legacyPowerShell && bytes.Equal(prefix, []byte{'W', 0}) {
		prefix, _ = buffer.Peek(len(commandRuntimePowerShellStartupPrefix))
		if string(prefix) == commandRuntimePowerShellStartupPrefix {
			return transform.NewReader(buffer, unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM).NewDecoder())
		}
	}
	return buffer
}

type commandRuntimeObservedReader struct {
	reader  io.Reader
	observe func(int)
}

func (r commandRuntimeObservedReader) Read(data []byte) (int, error) {
	count, err := r.reader.Read(data)
	if count > 0 {
		r.observe(count)
	}
	return count, err
}
