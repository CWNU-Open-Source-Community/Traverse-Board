package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestParseRectSpec(t *testing.T) {
	value, err := parseRectSpec(" 1,2,30,40 ")
	if err != nil || value != (rectSpec{X: 1, Y: 2, Width: 30, Height: 40}) {
		t.Fatalf("parse rectangle = %#v err=%v", value, err)
	}
	for _, invalid := range []string{"", "1,2,3", "1,2,3,0", "-1,2,3,4", "a,2,3,4"} {
		if _, err := parseRectSpec(invalid); err == nil {
			t.Fatalf("invalid rectangle %q was accepted", invalid)
		}
	}
}

func TestCompareIdenticalImagePassesStrictThresholds(t *testing.T) {
	baseline := testLoadedPNG(t, 3, 2, color.NRGBA{R: 10, G: 20, B: 30, A: 255})
	result, err := compare(compareRequest{name: "identical", baseline: baseline, actual: baseline,
		thresholds: thresholds{}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.metrics.Passed || !result.metrics.DimensionsEqual ||
		result.metrics.ComparedPixels != 6 || result.metrics.ChangedPixels != 0 ||
		result.metrics.DiffPixelRatio != 0 || result.metrics.MeanAbsoluteError != 0 {
		t.Fatalf("unexpected identical metrics: %#v", result.metrics)
	}
}

func TestCompareAppliesROIAndMaskBeforeThresholds(t *testing.T) {
	baseline := testLoadedPNG(t, 4, 4, color.NRGBA{A: 255})
	actual := cloneLoadedPNG(t, baseline)
	actual.image.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255}) // outside ROI
	actual.image.SetNRGBA(2, 2, color.NRGBA{R: 255, A: 255}) // masked
	actual.image.SetNRGBA(3, 3, color.NRGBA{R: 12, A: 255})  // compared
	refreshLoadedPNG(t, &actual)
	result, err := compare(compareRequest{name: "regions", baseline: baseline, actual: actual,
		rois:  []rectSpec{{X: 1, Y: 1, Width: 3, Height: 3}},
		masks: []rectSpec{{X: 2, Y: 2, Width: 1, Height: 1}},
		thresholds: thresholds{ChannelThreshold: 10, MaxDiffRatio: 0.2,
			MaxMeanAbsoluteError: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.metrics.Passed || result.metrics.ComparedPixels != 8 ||
		result.metrics.MaskedPixels != 1 || result.metrics.ChangedPixels != 1 ||
		result.metrics.DiffPixelRatio != 0.125 || result.metrics.MaximumChannelDelta != 12 {
		t.Fatalf("unexpected regional metrics: %#v", result.metrics)
	}
}

func TestCompareDimensionMismatchStillProducesStructuredFailure(t *testing.T) {
	baseline := testLoadedPNG(t, 2, 2, color.NRGBA{R: 100, A: 255})
	actual := testLoadedPNG(t, 3, 1, color.NRGBA{R: 100, A: 255})
	result, err := compare(compareRequest{name: "size", baseline: baseline, actual: actual,
		thresholds: thresholds{MaxDiffRatio: 1, MaxMeanAbsoluteError: 255}})
	if err != nil {
		t.Fatal(err)
	}
	if result.metrics.Passed || result.metrics.DimensionsEqual ||
		len(result.metrics.FailureReasons) != 1 ||
		result.metrics.FailureReasons[0] != "dimension_mismatch" ||
		result.diff.Bounds().Dx() != 3 || result.diff.Bounds().Dy() != 2 {
		t.Fatalf("unexpected dimension failure: %#v bounds=%v", result.metrics,
			result.diff.Bounds())
	}
}

func TestCompareRejectsRegionsThatSelectNoPixels(t *testing.T) {
	baseline := testLoadedPNG(t, 2, 2, color.NRGBA{A: 255})
	_, err := compare(compareRequest{name: "empty", baseline: baseline, actual: baseline,
		rois:  []rectSpec{{Width: 2, Height: 2}},
		masks: []rectSpec{{Width: 2, Height: 2}}, thresholds: thresholds{}})
	if err == nil {
		t.Fatal("fully masked comparison was accepted")
	}
}

func TestRunWritesActualDiffAndMetricsAndChecksBaselineDigest(t *testing.T) {
	root := t.TempDir()
	baselinePath := filepath.Join(root, "baseline.png")
	actualPath := filepath.Join(root, "actual-input.png")
	writeTestPNG(t, baselinePath, 3, 2, color.NRGBA{R: 20, G: 30, B: 40, A: 255})
	writeTestPNG(t, actualPath, 3, 2, color.NRGBA{R: 20, G: 30, B: 40, A: 255})
	raw, err := os.ReadFile(baselinePath)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	output := filepath.Join(root, "artifacts")
	stdout, stderr := bytes.Buffer{}, bytes.Buffer{}
	code := run([]string{"-baseline", baselinePath, "-baseline-sha256",
		hex.EncodeToString(digest[:]), "-actual", actualPath, "-output-dir", output,
		"-name", "main-workbench"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	for _, name := range []string{"main-workbench-actual.png", "main-workbench-diff.png",
		"main-workbench-metrics.json"} {
		if _, err := os.Stat(filepath.Join(output, name)); err != nil {
			t.Fatalf("missing output %s: %v", name, err)
		}
	}
	metricsRaw, err := os.ReadFile(filepath.Join(output, "main-workbench-metrics.json"))
	if err != nil {
		t.Fatal(err)
	}
	completed := metrics{}
	if err := json.Unmarshal(metricsRaw, &completed); err != nil || !completed.Passed ||
		completed.Outputs.Diff != "main-workbench-diff.png" {
		t.Fatalf("completed metrics = %#v err=%v", completed, err)
	}
	firstArtifacts := map[string][]byte{}
	for _, name := range []string{"main-workbench-actual.png", "main-workbench-diff.png",
		"main-workbench-metrics.json"} {
		firstArtifacts[name], err = os.ReadFile(filepath.Join(output, name))
		if err != nil {
			t.Fatal(err)
		}
	}
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"-baseline", baselinePath, "-baseline-sha256",
		hex.EncodeToString(digest[:]), "-actual", actualPath, "-output-dir", output,
		"-name", "main-workbench"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("repeat run code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	for name, first := range firstArtifacts {
		repeated, readErr := os.ReadFile(filepath.Join(output, name))
		if readErr != nil || !bytes.Equal(first, repeated) {
			t.Fatalf("artifact %s was not repeatable: err=%v", name, readErr)
		}
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"-baseline", baselinePath, "-baseline-sha256",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"-actual", actualPath, "-output-dir", output, "-name", "bad-hash"},
		&stdout, &stderr)
	if code != 2 || !bytes.Contains(stderr.Bytes(), []byte("baseline SHA-256 mismatch")) {
		t.Fatalf("bad digest code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestRunWritesStructuredArtifactsOnDimensionFailure(t *testing.T) {
	root := t.TempDir()
	baselinePath := filepath.Join(root, "baseline.png")
	actualPath := filepath.Join(root, "actual.png")
	output := filepath.Join(root, "artifacts")
	writeTestPNG(t, baselinePath, 2, 2, color.NRGBA{R: 10, A: 255})
	writeTestPNG(t, actualPath, 3, 1, color.NRGBA{R: 10, A: 255})
	stdout, stderr := bytes.Buffer{}, bytes.Buffer{}
	code := run([]string{"-baseline", baselinePath, "-actual", actualPath,
		"-output-dir", output, "-name", "different-size", "-max-diff-ratio", "1",
		"-max-mean-error", "255"}, &stdout, &stderr)
	if code != 1 || stderr.Len() != 0 {
		t.Fatalf("dimension run code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	raw, err := os.ReadFile(filepath.Join(output, "different-size-metrics.json"))
	if err != nil {
		t.Fatal(err)
	}
	completed := metrics{}
	if err := json.Unmarshal(raw, &completed); err != nil || completed.Passed ||
		completed.DimensionsEqual || len(completed.FailureReasons) != 1 ||
		completed.FailureReasons[0] != "dimension_mismatch" {
		t.Fatalf("dimension metrics = %#v err=%v", completed, err)
	}
	for _, name := range []string{"different-size-actual.png", "different-size-diff.png"} {
		if _, err := os.Stat(filepath.Join(output, name)); err != nil {
			t.Fatalf("missing failure artifact %s: %v", name, err)
		}
	}
}

func testLoadedPNG(t *testing.T, width, height int, value color.NRGBA) loadedPNG {
	t.Helper()
	canvas := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			canvas.SetNRGBA(x, y, value)
		}
	}
	result := loadedPNG{image: canvas, file: "fixture.png"}
	refreshLoadedPNG(t, &result)
	return result
}

func cloneLoadedPNG(t *testing.T, source loadedPNG) loadedPNG {
	t.Helper()
	bounds := source.image.Bounds()
	canvas := image.NewNRGBA(bounds)
	copy(canvas.Pix, source.image.Pix)
	result := loadedPNG{image: canvas, file: "actual.png"}
	refreshLoadedPNG(t, &result)
	return result
}

func refreshLoadedPNG(t *testing.T, value *loadedPNG) {
	t.Helper()
	buffer := bytes.Buffer{}
	if err := png.Encode(&buffer, value.image); err != nil {
		t.Fatal(err)
	}
	value.raw = buffer.Bytes()
	digest := sha256.Sum256(value.raw)
	value.digest = hex.EncodeToString(digest[:])
}

func writeTestPNG(t *testing.T, path string, width, height int, value color.NRGBA) {
	t.Helper()
	fixture := testLoadedPNG(t, width, height, value)
	if err := os.WriteFile(path, fixture.raw, 0o600); err != nil {
		t.Fatal(err)
	}
}
