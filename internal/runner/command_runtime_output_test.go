package runner

import (
	"encoding/binary"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"cyberagent-workbench/internal/outputsafe"
)

func commandRuntimeUTF16TestBytes(value string, order binary.ByteOrder, bom bool) []byte {
	units := utf16.Encode([]rune(value))
	if bom {
		units = append([]uint16{0xfeff}, units...)
	}
	data := make([]byte, len(units)*2)
	for index, unit := range units {
		order.PutUint16(data[index*2:], unit)
	}
	return data
}

func TestCommandRuntimeManagerDecodesPowerShellStartupBeforeRedaction(t *testing.T) {
	store := newCommandRuntimeMemoryStore()
	starter := &commandRuntimeFakeStarter{}
	manager, err := NewCommandRuntimeManager(store, starter, "output-decoding-owner")
	if err != nil {
		t.Fatal(err)
	}
	request := commandRuntimeTestRequest(manager, 2000)
	request.Spec.Spec.Profile = CommandRuntimePowerShell
	request.Spec.ExecutablePath = filepath.Join(t.TempDir(), "powershell.exe")
	snapshot, _, err := manager.Start(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	text := "Windows PowerShell 被终止，返回以下错误:\r\n“日志类型”的初始值引发异常。😀\n\x1b[31mtoken=secret-value-1234567890\x1b[0m\n"
	raw := commandRuntimeUTF16TestBytes(text, binary.LittleEndian, false)
	process := starter.last()
	// Odd pipe boundaries also split the emoji surrogate pair and secret.
	for _, value := range raw {
		if _, err := process.stderrWriter.Write([]byte{value}); err != nil {
			t.Fatal(err)
		}
	}
	process.finish(1)
	terminal, page := waitCommandRuntimeTerminal(t, manager, snapshot.ID)
	stored, err := store.GetCommandRuntimeJob(t.Context(), snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Stderr != "Windows PowerShell 被终止，返回以下错误:\n“日志类型”的初始值引发异常。😀\ntoken=[REDACTED:secret]\n" {
		t.Fatalf("decoded/redacted stderr=%q", stored.Stderr)
	}
	if terminal.State != CommandRuntimeJobFailed || terminal.ExitCode == nil || *terminal.ExitCode != 1 ||
		stored.StderrObservedBytes != int64(len(raw)) || stored.StderrSHA256 != commandRuntimeStringSHA256(stored.Stderr) {
		t.Fatalf("raw count, persisted digest or failure changed: terminal=%#v stored=%#v", terminal, stored)
	}
	var framed strings.Builder
	for _, frame := range page.Frames {
		if frame.Stream == CommandRuntimeStderr {
			framed.WriteString(frame.Text)
		}
	}
	if framed.String() != stored.Stderr || page.EndCursor != uint64(len(stored.Stderr)) {
		t.Fatalf("frame cursor/content mismatch: %q page=%#v", framed.String(), page)
	}
}

// one-byte reads exercise encoding marks, code units, surrogate pairs and EOF.
type commandRuntimeByteReader struct{ data []byte }

func (r *commandRuntimeByteReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	p[0], r.data = r.data[0], r.data[1:]
	return 1, nil
}

func TestCommandRuntimeOutputReaderDecodingBoundaries(t *testing.T) {
	for _, test := range []struct {
		name   string
		data   []byte
		legacy bool
		want   string
	}{
		{"UTF8 Chinese", []byte("中文😀\n"), true, "中文😀\n"},
		{"UTF16LE BOM", commandRuntimeUTF16TestBytes("中文😀\n", binary.LittleEndian, true), false, "中文😀\n"},
		{"UTF16BE BOM", commandRuntimeUTF16TestBytes("中文😀\n", binary.BigEndian, true), false, "中文😀\n"},
		{"short UTF8", []byte("x"), true, "x"},
		{"partial startup prefix", []byte{'W', 0}, true, "W"},
		{"UTF8 Windows prefix", []byte("Windows PowerShell 中文\n"), true, "Windows PowerShell 中文\n"},
		{"no unmarked Unicode guessing", commandRuntimeUTF16TestBytes("中文", binary.LittleEndian, false), true, outputsafe.Sanitize(commandRuntimeUTF16TestBytes("中文", binary.LittleEndian, false))},
		{"unpaired surrogate", []byte{0xff, 0xfe, 0x3d, 0xd8}, false, "�"},
		{"odd final byte", []byte{0xff, 0xfe, 0x61}, false, "�"},
	} {
		t.Run(test.name, func(t *testing.T) {
			data, err := io.ReadAll(commandRuntimeTextReader(&commandRuntimeByteReader{data: test.data}, test.legacy))
			if err != nil {
				t.Fatal(err)
			}
			if got := outputsafe.Sanitize(data); got != test.want {
				t.Fatalf("decoded output=%q, want %q", got, test.want)
			}
		})
	}
}
