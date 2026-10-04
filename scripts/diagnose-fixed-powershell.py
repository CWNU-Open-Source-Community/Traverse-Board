"""Temporary PR239 phase probe; its overlay is not a production gate result."""

from hashlib import sha256
import json
import os
from pathlib import Path
import subprocess
import tempfile


def replace_once(text, old, new):
    if text.count(old) != 1:
        raise RuntimeError("fixed phase probe source no longer matches")
    return text.replace(old, new, 1)


def phase(label):
    return (
        "[Console]::Error.WriteLine(('fixed_phase=" + label
        + " utc_ticks={0} cpu_ms={1}' -f [DateTime]::UtcNow.Ticks, "
        + "[int64][Diagnostics.Process]::GetCurrentProcess().TotalProcessorTime.TotalMilliseconds)); "
    )


def make_overlay(source, test):
    # The first marker precedes the Process/CPU query. Keep the existing cmdlet
    # pipeline intact so diagnostics do not reorder module initialization.
    source = replace_once(
        source,
        "param([string]$RelativePathHex) if",
        "param([string]$RelativePathHex) "
        "[Console]::Error.WriteLine(('fixed_phase=entry utc_ticks={0}' -f [DateTime]::UtcNow.Ticks)); "
        + phase("entry_cpu") + "if",
    )
    source = replace_once(
        source,
        "$RelativePath = [Text.Encoding]::UTF8.GetString($Bytes); Get-ChildItem",
        "$RelativePath = [Text.Encoding]::UTF8.GetString($Bytes); " + phase("decoded") + "Get-ChildItem",
    )
    source = replace_once(
        source,
        "| Select-Object Name,Length,Attributes | ConvertTo-Json -Compress }`",
        "| Select-Object Name,Length,Attributes | ConvertTo-Json -Compress; " + phase("completed") + "}`",
    )
    test = replace_once(test, '\t"errors"\n', '\t"errors"\n\t"fmt"\n')
    test = replace_once(test, '\t"path/filepath"\n', '\t"path/filepath"\n\t"regexp"\n')
    test = replace_once(
        test,
        '\t\t\tif err := os.WriteFile(filepath.Join(request.WorkspaceRoot, "fixed-list-marker.txt"), []byte("marker"), 0o600); err != nil {\n'
        '\t\t\t\tt.Fatal(err)\n\t\t\t}\n',
        '\t\t\tfor i := 0; i < 400; i++ {\n'
        '\t\t\t\tname := fmt.Sprintf("page-%03d-%s.txt", i, strings.Repeat("x", 60))\n'
        '\t\t\t\tif err := os.WriteFile(filepath.Join(request.WorkspaceRoot, name), nil, 0o600); err != nil {\n'
        '\t\t\t\t\tt.Fatal(err)\n\t\t\t\t}\n\t\t\t}\n',
    )
    test = replace_once(
        test,
        '\t\t\tif result.watchdog || result.waitErr != nil || result.killErr != nil || result.exitCode != 0 ||\n',
        '\t\t\tphaseLine := regexp.MustCompile(`^fixed_phase=(entry utc_ticks=[0-9]+|(entry_cpu|decoded|completed) utc_ticks=[0-9]+ cpu_ms=[0-9]+)$`)\n'
        '\t\t\tvar phases []string\n'
        '\t\t\tfor _, line := range strings.Split(strings.TrimSpace(string(result.stderr.value)), "\\n") {\n'
        '\t\t\t\tline = strings.TrimSpace(line)\n'
        '\t\t\t\tif line == "" {\n\t\t\t\t\tcontinue\n\t\t\t\t}\n'
        '\t\t\t\tif !phaseLine.MatchString(line) {\n'
        '\t\t\t\t\tt.Fatalf("unexpected diagnostic stderr: bytes=%d", len(result.stderr.value))\n\t\t\t\t}\n'
        '\t\t\t\tphases = append(phases, strings.Fields(line)[0])\n'
        '\t\t\t\tt.Log(line)\n\t\t\t}\n'
        '\t\t\tif result.watchdog || result.waitErr != nil || result.killErr != nil || result.exitCode != 0 ||\n',
    )
    test = replace_once(test, 'result.stderr.err != nil || len(result.stderr.value) != 0 {', 'result.stderr.err != nil {')
    test = replace_once(
        test,
        't.Fatalf("restricted process failed: %s", result)',
        't.Fatalf("restricted process failed: watchdog=%t exit=%d wait_error=%t kill_error=%t stdout_read_error=%t stderr_read_error=%t stdout_bytes=%d stderr_bytes=%d", '
        'result.watchdog, result.exitCode, result.waitErr != nil, result.killErr != nil, result.stdout.err != nil, result.stderr.err != nil, len(result.stdout.value), len(result.stderr.value))',
    )
    test = replace_once(
        test,
        't.Fatalf("fixed output missing %q: %q", want, result.stdout.value)',
        't.Fatalf("fixed output marker missing: stdout_bytes=%d", len(result.stdout.value))',
    )
    test = replace_once(test, 'want = "fixed-list-marker.txt"', 'want = "page-000-" + strings.Repeat("x", 60) + ".txt"')
    test = replace_once(
        test,
        '\t\t\tif reaped, err := waitControlledJobReaped(t.Context(), native.job, time.Second); err != nil || !reaped {',
        '\t\t\tif strings.Join(phases, " ") != "fixed_phase=entry fixed_phase=entry_cpu fixed_phase=decoded fixed_phase=completed" {\n'
        '\t\t\t\tt.Fatalf("diagnostic phases incomplete: %v", phases)\n\t\t\t}\n'
        '\t\t\tif entries := strings.Count(string(result.stdout.value), "page-"); entries != 400 {\n'
        '\t\t\t\tt.Fatalf("diagnostic output entries=%d want=400", entries)\n\t\t\t}\n'
        '\t\t\tif reaped, err := waitControlledJobReaped(t.Context(), native.job, time.Second); err != nil || !reaped {',
    )
    return source, test


def main():
    if os.name != "nt" or os.environ.get("GITHUB_ACTIONS") != "true":
        raise RuntimeError("phase probe is restricted to the approved temporary Windows CI runner")
    repo = Path(__file__).resolve().parents[1]
    paths = [repo / "internal/runner/controlled_command.go", repo / "internal/runner/controlled_execution_windows_test.go"]
    original = [path.read_bytes() for path in paths]
    derived = make_overlay(*(data.decode("utf-8").replace("\r\n", "\n") for data in original))
    # Only small derived source files are written. No checkout, cache or native
    # permissions are changed, and no instrumented result replaces the gate.
    runner_temp = Path(os.environ["RUNNER_TEMP"]).resolve(strict=True)
    with tempfile.TemporaryDirectory(prefix="pr239-fixed-phase-", dir=runner_temp) as directory:
        temporary = Path(directory).resolve(strict=True)
        if temporary.parent != runner_temp:
            raise RuntimeError("phase probe temporary directory escaped RUNNER_TEMP")
        replacements = {}
        digests = []
        for path, data, contents in zip(paths, original, derived):
            target = temporary / path.name
            target.write_text(contents, encoding="utf-8", newline="\n")
            replacements[str(path)] = str(target)
            digests.append({"source": path.name, "original_sha256": sha256(data).hexdigest(), "overlay_sha256": sha256(target.read_bytes()).hexdigest()})
        overlay = temporary / "overlay.json"
        overlay.write_text(json.dumps({"Replace": replacements}), encoding="utf-8")
        print(json.dumps({"diagnostic_only": True, "fixture_files": 400, "budget_ms": 30000, "sources": digests}), flush=True)
        try:
            completed = subprocess.run([
                "go", "test", "-overlay", str(overlay), "-v", "-count=1", "-timeout", "10m", "./internal/runner",
                "-run", "^TestWindowsFixedCommandRuntimeUsesRestrictedNativeProcess$/^powershell-workspace-list$",
            ], cwd=repo, check=False)
        finally:
            if any(path.read_bytes() != data for path, data in zip(paths, original)):
                raise RuntimeError("checkout source changed during phase probe")
        print(json.dumps({"diagnostic_only": True, "exit_code": completed.returncode, "checkout_unchanged": True}), flush=True)
        return completed.returncode


if __name__ == "__main__":
    raise SystemExit(main())
