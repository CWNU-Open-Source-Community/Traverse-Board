package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const (
	metricsProtocolVersion = "traverse_visual_diff.v1"
	maxImageBytes          = 128 << 20
	maxImageDimension      = 16384
	maxImagePixels         = 100_000_000
)

var safeOutputName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type rectSpec struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

func (r rectSpec) rectangle() image.Rectangle {
	return image.Rect(r.X, r.Y, r.X+r.Width, r.Y+r.Height)
}

type rectFlags []rectSpec

func (values *rectFlags) String() string {
	parts := make([]string, 0, len(*values))
	for _, value := range *values {
		parts = append(parts, fmt.Sprintf("%d,%d,%d,%d", value.X, value.Y,
			value.Width, value.Height))
	}
	return strings.Join(parts, ";")
}

func (values *rectFlags) Set(raw string) error {
	value, err := parseRectSpec(raw)
	if err != nil {
		return err
	}
	*values = append(*values, value)
	return nil
}

func parseRectSpec(raw string) (rectSpec, error) {
	parts := strings.Split(raw, ",")
	if len(parts) != 4 {
		return rectSpec{}, errors.New("rectangle must be x,y,width,height")
	}
	parsed := [4]int{}
	for index, part := range parts {
		value, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil {
			return rectSpec{}, fmt.Errorf("rectangle component %d is invalid", index+1)
		}
		parsed[index] = value
	}
	result := rectSpec{X: parsed[0], Y: parsed[1], Width: parsed[2], Height: parsed[3]}
	if result.X < 0 || result.Y < 0 || result.Width <= 0 || result.Height <= 0 {
		return rectSpec{}, errors.New("rectangle coordinates must be non-negative and size positive")
	}
	return result, nil
}

type thresholds struct {
	ChannelThreshold     int     `json:"channel_threshold"`
	MaxDiffRatio         float64 `json:"max_diff_ratio"`
	MaxMeanAbsoluteError float64 `json:"max_mean_absolute_error"`
}

func (value thresholds) validate() error {
	if value.ChannelThreshold < 0 || value.ChannelThreshold > 255 {
		return errors.New("channel threshold must be within 0..255")
	}
	if math.IsNaN(value.MaxDiffRatio) || math.IsInf(value.MaxDiffRatio, 0) ||
		value.MaxDiffRatio < 0 || value.MaxDiffRatio > 1 {
		return errors.New("maximum diff ratio must be within 0..1")
	}
	if math.IsNaN(value.MaxMeanAbsoluteError) || math.IsInf(value.MaxMeanAbsoluteError, 0) ||
		value.MaxMeanAbsoluteError < 0 || value.MaxMeanAbsoluteError > 255 {
		return errors.New("maximum mean absolute error must be within 0..255")
	}
	return nil
}

type imageArtifact struct {
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
	Bytes  int    `json:"bytes"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type outputArtifacts struct {
	Actual  string `json:"actual"`
	Diff    string `json:"diff"`
	Metrics string `json:"metrics"`
}

type metrics struct {
	ProtocolVersion      string          `json:"protocol_version"`
	Name                 string          `json:"name"`
	Baseline             imageArtifact   `json:"baseline"`
	Actual               imageArtifact   `json:"actual"`
	DimensionsEqual      bool            `json:"dimensions_equal"`
	ROIs                 []rectSpec      `json:"rois"`
	Masks                []rectSpec      `json:"masks"`
	ComparedPixels       uint64          `json:"compared_pixels"`
	MaskedPixels         uint64          `json:"masked_pixels"`
	ChangedPixels        uint64          `json:"changed_pixels"`
	DiffPixelRatio       float64         `json:"diff_pixel_ratio"`
	MeanAbsoluteError    float64         `json:"mean_absolute_error"`
	RootMeanSquaredError float64         `json:"root_mean_squared_error"`
	MaximumChannelDelta  int             `json:"maximum_channel_delta"`
	Thresholds           thresholds      `json:"thresholds"`
	Passed               bool            `json:"passed"`
	FailureReasons       []string        `json:"failure_reasons"`
	Outputs              outputArtifacts `json:"outputs"`
}

type loadedPNG struct {
	image  *image.NRGBA
	raw    []byte
	digest string
	file   string
}

type compareRequest struct {
	name       string
	baseline   loadedPNG
	actual     loadedPNG
	rois       []rectSpec
	masks      []rectSpec
	thresholds thresholds
}

type compareResult struct {
	metrics metrics
	diff    *image.NRGBA
}

type commandOptions struct {
	baselinePath       string
	baselineSHA256     string
	actualPath         string
	outputDirectory    string
	name               string
	rois               rectFlags
	masks              rectFlags
	channelThreshold   int
	maxDiffRatio       float64
	maxMeanAbsoluteErr float64
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(arguments []string, stdout, stderr io.Writer) int {
	options := commandOptions{}
	set := flag.NewFlagSet("visualdiff", flag.ContinueOnError)
	set.SetOutput(stderr)
	set.StringVar(&options.baselinePath, "baseline", "", "authoritative baseline PNG")
	set.StringVar(&options.baselineSHA256, "baseline-sha256", "", "expected baseline SHA-256")
	set.StringVar(&options.actualPath, "actual", "", "actual PNG to compare")
	set.StringVar(&options.outputDirectory, "output-dir", "", "artifact output directory")
	set.StringVar(&options.name, "name", "", "safe output case name")
	set.Var(&options.rois, "roi", "comparison ROI x,y,width,height; repeatable")
	set.Var(&options.masks, "mask", "masked rectangle x,y,width,height; repeatable")
	set.IntVar(&options.channelThreshold, "channel-threshold", 0,
		"maximum per-channel delta ignored when counting changed pixels")
	set.Float64Var(&options.maxDiffRatio, "max-diff-ratio", 0,
		"maximum changed-pixel ratio")
	set.Float64Var(&options.maxMeanAbsoluteErr, "max-mean-error", 0,
		"maximum mean absolute channel error")
	if err := set.Parse(arguments); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if set.NArg() != 0 {
		fmt.Fprintln(stderr, "visualdiff accepts no positional arguments")
		return 2
	}
	if err := execute(options, stdout); err != nil {
		fmt.Fprintf(stderr, "visualdiff: %v\n", err)
		return 2
	}
	metricsPath := filepath.Join(options.outputDirectory, options.name+"-metrics.json")
	raw, err := os.ReadFile(metricsPath)
	if err != nil {
		fmt.Fprintf(stderr, "visualdiff: read completed metrics: %v\n", err)
		return 2
	}
	completed := metrics{}
	if err := json.Unmarshal(raw, &completed); err != nil {
		fmt.Fprintf(stderr, "visualdiff: decode completed metrics: %v\n", err)
		return 2
	}
	fmt.Fprintf(stdout, "visual_diff_metrics: %s\n", metricsPath)
	fmt.Fprintf(stdout, "visual_diff_passed: %t\n", completed.Passed)
	if !completed.Passed {
		return 1
	}
	return 0
}

func execute(options commandOptions, stdout io.Writer) error {
	if strings.TrimSpace(options.baselinePath) == "" ||
		strings.TrimSpace(options.actualPath) == "" ||
		strings.TrimSpace(options.outputDirectory) == "" {
		return errors.New("baseline, actual, and output-dir are required")
	}
	if !safeOutputName.MatchString(options.name) {
		return errors.New("name must use only letters, digits, dot, underscore, or hyphen")
	}
	limits := thresholds{ChannelThreshold: options.channelThreshold,
		MaxDiffRatio: options.maxDiffRatio, MaxMeanAbsoluteError: options.maxMeanAbsoluteErr}
	if err := limits.validate(); err != nil {
		return err
	}
	baseline, err := loadPNG(options.baselinePath)
	if err != nil {
		return fmt.Errorf("load baseline: %w", err)
	}
	if expected := strings.ToLower(strings.TrimSpace(options.baselineSHA256)); expected != "" {
		decoded, decodeErr := hex.DecodeString(expected)
		if decodeErr != nil || len(decoded) != sha256.Size {
			return errors.New("expected baseline SHA-256 is invalid")
		}
		if baseline.digest != expected {
			return fmt.Errorf("baseline SHA-256 mismatch: got %s", baseline.digest)
		}
	}
	actual, err := loadPNG(options.actualPath)
	if err != nil {
		return fmt.Errorf("load actual: %w", err)
	}
	comparison, err := compare(compareRequest{name: options.name, baseline: baseline,
		actual: actual, rois: options.rois, masks: options.masks, thresholds: limits})
	if err != nil {
		return err
	}
	outputDirectory, err := filepath.Abs(options.outputDirectory)
	if err != nil {
		return fmt.Errorf("resolve output directory: %w", err)
	}
	if err := os.MkdirAll(outputDirectory, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	actualName := options.name + "-actual.png"
	diffName := options.name + "-diff.png"
	metricsName := options.name + "-metrics.json"
	actualPath := filepath.Join(outputDirectory, actualName)
	diffPath := filepath.Join(outputDirectory, diffName)
	metricsPath := filepath.Join(outputDirectory, metricsName)
	if err := writeAtomic(actualPath, actual.raw, 0o644); err != nil {
		return fmt.Errorf("write actual artifact: %w", err)
	}
	diffRaw := bytes.Buffer{}
	encoder := png.Encoder{CompressionLevel: png.DefaultCompression}
	if err := encoder.Encode(&diffRaw, comparison.diff); err != nil {
		return fmt.Errorf("encode diff artifact: %w", err)
	}
	if err := writeAtomic(diffPath, diffRaw.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write diff artifact: %w", err)
	}
	comparison.metrics.Outputs = outputArtifacts{Actual: actualName, Diff: diffName,
		Metrics: metricsName}
	metricsRaw, err := json.MarshalIndent(comparison.metrics, "", "  ")
	if err != nil {
		return fmt.Errorf("encode metrics artifact: %w", err)
	}
	metricsRaw = append(metricsRaw, '\n')
	if err := writeAtomic(metricsPath, metricsRaw, 0o644); err != nil {
		return fmt.Errorf("write metrics artifact: %w", err)
	}
	if stdout != nil {
		fmt.Fprintf(stdout, "visual_diff_actual: %s\nvisual_diff_diff: %s\n", actualPath, diffPath)
	}
	return nil
}

func loadPNG(path string) (loadedPNG, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return loadedPNG{}, err
	}
	item, err := os.Lstat(absolute)
	if err != nil {
		return loadedPNG{}, err
	}
	if !item.Mode().IsRegular() || item.Mode()&os.ModeSymlink != 0 {
		return loadedPNG{}, errors.New("PNG input must be a regular non-symlink file")
	}
	if item.Size() <= 0 || item.Size() > maxImageBytes {
		return loadedPNG{}, errors.New("PNG input size is outside the fixed bound")
	}
	raw, err := os.ReadFile(absolute)
	if err != nil {
		return loadedPNG{}, err
	}
	configuration, err := png.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return loadedPNG{}, fmt.Errorf("decode PNG configuration: %w", err)
	}
	if err := validateImageDimensions(configuration.Width, configuration.Height); err != nil {
		return loadedPNG{}, err
	}
	decoded, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		return loadedPNG{}, fmt.Errorf("decode PNG: %w", err)
	}
	bounds := decoded.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width != configuration.Width || height != configuration.Height {
		return loadedPNG{}, errors.New("decoded PNG dimensions differ from its configuration")
	}
	normalized := image.NewNRGBA(image.Rect(0, 0, width, height))
	draw.Draw(normalized, normalized.Bounds(), decoded, bounds.Min, draw.Src)
	digest := sha256.Sum256(raw)
	return loadedPNG{image: normalized, raw: raw, digest: hex.EncodeToString(digest[:]),
		file: filepath.Base(absolute)}, nil
}

func validateImageDimensions(width, height int) error {
	if width <= 0 || height <= 0 || width > maxImageDimension || height > maxImageDimension ||
		int64(width)*int64(height) > maxImagePixels {
		return errors.New("PNG dimensions are outside the fixed bound")
	}
	return nil
}

func compare(request compareRequest) (compareResult, error) {
	if request.baseline.image == nil || request.actual.image == nil {
		return compareResult{}, errors.New("comparison images are required")
	}
	if err := request.thresholds.validate(); err != nil {
		return compareResult{}, err
	}
	baselineBounds := request.baseline.image.Bounds()
	actualBounds := request.actual.image.Bounds()
	rois := append([]rectSpec(nil), request.rois...)
	if len(rois) == 0 {
		rois = []rectSpec{{Width: baselineBounds.Dx(), Height: baselineBounds.Dy()}}
	}
	masks := append([]rectSpec{}, request.masks...)
	if err := validateRectSpecs("ROI", rois, baselineBounds); err != nil {
		return compareResult{}, err
	}
	if err := validateRectSpecs("mask", masks, baselineBounds); err != nil {
		return compareResult{}, err
	}

	state := make([]byte, baselineBounds.Dx()*baselineBounds.Dy())
	for _, value := range rois {
		rectangle := value.rectangle()
		for y := rectangle.Min.Y; y < rectangle.Max.Y; y++ {
			row := y * baselineBounds.Dx()
			for x := rectangle.Min.X; x < rectangle.Max.X; x++ {
				state[row+x] = 1
			}
		}
	}
	maskedPixels := uint64(0)
	for _, value := range masks {
		rectangle := value.rectangle()
		for y := rectangle.Min.Y; y < rectangle.Max.Y; y++ {
			row := y * baselineBounds.Dx()
			for x := rectangle.Min.X; x < rectangle.Max.X; x++ {
				index := row + x
				if state[index] == 1 {
					state[index] = 2
					maskedPixels++
				}
			}
		}
	}

	canvasWidth := max(baselineBounds.Dx(), actualBounds.Dx())
	canvasHeight := max(baselineBounds.Dy(), actualBounds.Dy())
	diff := image.NewNRGBA(image.Rect(0, 0, canvasWidth, canvasHeight))
	comparedPixels := uint64(0)
	changedPixels := uint64(0)
	sumAbsolute := uint64(0)
	sumSquared := uint64(0)
	maximumDelta := 0
	for y := 0; y < baselineBounds.Dy(); y++ {
		for x := 0; x < baselineBounds.Dx(); x++ {
			switch state[y*baselineBounds.Dx()+x] {
			case 0:
				continue
			case 2:
				diff.SetNRGBA(x, y, color.NRGBA{R: 0, G: 128, B: 255, A: 96})
				continue
			}
			comparedPixels++
			baselinePixel := request.baseline.image.NRGBAAt(x, y)
			actualPresent := x < actualBounds.Dx() && y < actualBounds.Dy()
			actualPixel := color.NRGBA{}
			if actualPresent {
				actualPixel = request.actual.image.NRGBAAt(x, y)
			}
			deltas := [4]int{}
			if actualPresent {
				deltas = [4]int{absoluteByteDelta(baselinePixel.R, actualPixel.R),
					absoluteByteDelta(baselinePixel.G, actualPixel.G),
					absoluteByteDelta(baselinePixel.B, actualPixel.B),
					absoluteByteDelta(baselinePixel.A, actualPixel.A)}
			} else {
				deltas = [4]int{255, 255, 255, 255}
			}
			pixelDelta := 0
			for _, delta := range deltas {
				sumAbsolute += uint64(delta)
				sumSquared += uint64(delta * delta)
				pixelDelta = max(pixelDelta, delta)
				maximumDelta = max(maximumDelta, delta)
			}
			if pixelDelta > request.thresholds.ChannelThreshold {
				changedPixels++
				diff.SetNRGBA(x, y, color.NRGBA{R: 255, G: uint8(255 - pixelDelta), A: 255})
			} else {
				gray := uint8((uint16(baselinePixel.R) + uint16(baselinePixel.G) +
					uint16(baselinePixel.B)) / 3)
				diff.SetNRGBA(x, y, color.NRGBA{R: gray, G: gray, B: gray, A: 80})
			}
		}
	}
	if comparedPixels == 0 {
		return compareResult{}, errors.New("ROIs and masks left no pixels to compare")
	}
	if actualBounds.Dx() > baselineBounds.Dx() || actualBounds.Dy() > baselineBounds.Dy() {
		for y := 0; y < actualBounds.Dy(); y++ {
			for x := 0; x < actualBounds.Dx(); x++ {
				if x >= baselineBounds.Dx() || y >= baselineBounds.Dy() {
					diff.SetNRGBA(x, y, color.NRGBA{R: 200, B: 255, A: 255})
				}
			}
		}
	}

	channelSamples := float64(comparedPixels * 4)
	diffRatio := float64(changedPixels) / float64(comparedPixels)
	meanError := float64(sumAbsolute) / channelSamples
	rmse := math.Sqrt(float64(sumSquared) / channelSamples)
	dimensionsEqual := baselineBounds.Size() == actualBounds.Size()
	failures := make([]string, 0, 3)
	if !dimensionsEqual {
		failures = append(failures, "dimension_mismatch")
	}
	if diffRatio > request.thresholds.MaxDiffRatio+1e-12 {
		failures = append(failures, "diff_ratio_exceeded")
	}
	if meanError > request.thresholds.MaxMeanAbsoluteError+1e-12 {
		failures = append(failures, "mean_absolute_error_exceeded")
	}
	resultMetrics := metrics{
		ProtocolVersion: metricsProtocolVersion,
		Name:            request.name,
		Baseline: imageArtifact{File: request.baseline.file, SHA256: request.baseline.digest,
			Bytes: len(request.baseline.raw), Width: baselineBounds.Dx(), Height: baselineBounds.Dy()},
		Actual: imageArtifact{File: request.actual.file, SHA256: request.actual.digest,
			Bytes: len(request.actual.raw), Width: actualBounds.Dx(), Height: actualBounds.Dy()},
		DimensionsEqual: dimensionsEqual, ROIs: rois,
		Masks: masks, ComparedPixels: comparedPixels,
		MaskedPixels: maskedPixels, ChangedPixels: changedPixels, DiffPixelRatio: diffRatio,
		MeanAbsoluteError: meanError, RootMeanSquaredError: rmse,
		MaximumChannelDelta: maximumDelta, Thresholds: request.thresholds,
		Passed: len(failures) == 0, FailureReasons: failures,
	}
	return compareResult{metrics: resultMetrics, diff: diff}, nil
}

func validateRectSpecs(kind string, values []rectSpec, bounds image.Rectangle) error {
	for _, value := range values {
		if value.X < 0 || value.Y < 0 || value.Width <= 0 || value.Height <= 0 ||
			value.X > bounds.Dx()-value.Width || value.Y > bounds.Dy()-value.Height {
			return fmt.Errorf("%s rectangle is outside the baseline: %d,%d,%d,%d", kind,
				value.X, value.Y, value.Width, value.Height)
		}
	}
	return nil
}

func absoluteByteDelta(left, right uint8) int {
	if left >= right {
		return int(left - right)
	}
	return int(right - left)
}

func writeAtomic(path string, content []byte, mode os.FileMode) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".visualdiff-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(temporaryPath, path)
}
