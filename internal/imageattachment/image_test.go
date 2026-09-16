package imageattachment

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

func TestValidateImageRejectsMismatchedTruncatedOversizedAndUnsafeName(t *testing.T) {
	var pngData, jpegData bytes.Buffer
	pixels := image.NewNRGBA(image.Rect(0, 0, 3, 4))
	if err := png.Encode(&pngData, pixels); err != nil {
		t.Fatal(err)
	}
	if err := jpeg.Encode(&jpegData, pixels, nil); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		mime string
		data []byte
	}{{"image/png", pngData.Bytes()}, {"image/jpeg", jpegData.Bytes()}} {
		image, err := Validate(item.data, item.mime, "截图.png")
		if err != nil || image.Width != 3 || image.Height != 4 || image.ByteSize != len(item.data) || len(image.SHA256) != 64 {
			t.Fatalf("valid image rejected: %#v %v", image, err)
		}
	}
	for _, item := range []struct {
		mime, name string
		data       []byte
	}{{"image/jpeg", "a", pngData.Bytes()}, {"image/png", "a", pngData.Bytes()[:len(pngData.Bytes())-12]}, {"image/png", "../path", pngData.Bytes()}, {"image/png", strings.Repeat("中", 161), pngData.Bytes()}, {"image/png", "a", make([]byte, MaxBytes+1)}, {"image/svg+xml", "a", []byte(`<svg></svg>`)}} {
		if _, err := Validate(item.data, item.mime, item.name); err == nil {
			t.Fatal("invalid image was accepted")
		}
	}
}

func TestValidateImageDecodesActualWebP(t *testing.T) {
	data, err := base64.StdEncoding.DecodeString("UklGRiIAAABXRUJQVlA4IBYAAAAwAQCdASoBAAEADsD+JaQAA3AAAAAA")
	if err != nil {
		t.Fatal(err)
	}
	image, err := Validate(data, "image/webp", "pixel.webp")
	if err != nil || image.Width != 1 || image.Height != 1 || image.ByteSize != len(data) {
		t.Fatalf("WebP input did not decode: %#v %v", image, err)
	}
	if _, err := Validate(data[:len(data)-8], "image/webp", "truncated.webp"); err == nil {
		t.Fatal("truncated WebP was accepted")
	}
}
