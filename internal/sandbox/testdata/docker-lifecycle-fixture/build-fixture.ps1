# build-fixture.ps1
# Builds the environment-free scratch lifecycle fixture and prints its exact
# digest for CYBERAGENT_DOCKER_LIFECYCLE_TEST_IMAGE_DIGEST.
#
# Both BuildKit and the legacy builder inject a default PATH into the image
# config of FROM scratch builds. The product contract requires images with
# zero environment entries, so this script rewrites the saved image config
# (dropping config.Env / container_config.Env), recomputes the OCI config
# digest, and reloads the image. Docker Desktop re-synthesizes the tag
# RepoDigest for the reloaded image, so no registry round trip is needed.
#
# Usage: powershell -ExecutionPolicy Bypass -File build-fixture.ps1

$ErrorActionPreference = "Stop"
$fixtureDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$tag = "cyberagent-docker-lifecycle-fixture:issue40"

function Step($name, [scriptblock]$body) {
  Write-Host ("== " + $name)
  & $body
  if ($LASTEXITCODE -ne 0) {
    throw ("fixture step failed: " + $name)
  }
}

# Windows PowerShell 5.1 converts redirected native stderr into error records,
# which $ErrorActionPreference="Stop" then makes terminating. Run optional
# Docker housekeeping without that trap.
function Docker-Quiet([scriptblock]$command) {
  $saved = $ErrorActionPreference
  $ErrorActionPreference = "Continue"
  try {
    & $command | Out-Null
  } finally {
    $ErrorActionPreference = $saved
  }
}

$work = Join-Path ([System.IO.Path]::GetTempPath()) ("dsh-docker-lifecycle-fixture-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $work | Out-Null
try {
  Push-Location $fixtureDir
  try {
    Step "build static linux fixture binary" {
      $env:GOOS = "linux"
      $env:GOARCH = "amd64"
      $env:CGO_ENABLED = "0"
      go build -o lifecycle-fixture .
    }
    Step "build raw scratch image" {
      Docker-Quiet { docker image rm -f $tag }
      docker build --provenance=false --no-cache --platform linux/amd64 -t $tag .
    }
    Step "export image for config surgery" {
      docker image save $tag -o (Join-Path $work "raw.tar")
      tar -xf (Join-Path $work "raw.tar") -C $work
    }
    Step "drop environment entries and recompute the config digest" {
      $strip = Join-Path $work "strip.go"
      @'
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func read(path string) []byte {
	raw, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	return raw
}

func write(path string, data []byte) {
	if err := os.WriteFile(path, data, 0o600); err != nil {
		panic(err)
	}
}

func digestOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func main() {
	if len(os.Args) != 2 {
		panic("usage: strip <image-dir>")
	}
	dir := os.Args[1]
	blobDir := filepath.Join(dir, "blobs", "sha256")

	// docker-save manifest.json points Config at the image config blob.
	var entries []struct {
		Config   string   `json:"Config"`
		RepoTags []string `json:"RepoTags"`
		Layers   []string `json:"Layers"`
	}
	if err := json.Unmarshal(read(filepath.Join(dir, "manifest.json")), &entries); err != nil {
		panic(err)
	}
	if len(entries) != 1 {
		panic("fixture save must contain exactly one manifest entry")
	}
	oldConfigPath := filepath.Join(dir, filepath.FromSlash(entries[0].Config))

	// 1. Rewrite the image config blob without environment entries.
	var config map[string]any
	if err := json.Unmarshal(read(oldConfigPath), &config); err != nil {
		panic(err)
	}
	if imageConfig, ok := config["config"].(map[string]any); ok {
		delete(imageConfig, "Env")
	}
	if containerConfig, ok := config["container_config"].(map[string]any); ok {
		delete(containerConfig, "Env")
	}
	editedConfig, err := json.Marshal(config)
	if err != nil {
		panic(err)
	}
	configDigest := digestOf(editedConfig)

	// 2. Locate the OCI image manifest through the OCI index.
	var index map[string]any
	if err := json.Unmarshal(read(filepath.Join(dir, "index.json")), &index); err != nil {
		panic(err)
	}
	manifests, ok := index["manifests"].([]any)
	if !ok || len(manifests) != 1 {
		panic("fixture index must contain exactly one manifest")
	}
	manifestEntry, ok := manifests[0].(map[string]any)
	if !ok {
		panic("fixture index manifest is invalid")
	}
	manifestDigest, ok := manifestEntry["digest"].(string)
	if !ok || len(manifestDigest) != 71 || manifestDigest[:7] != "sha256:" {
		panic("fixture index manifest digest is invalid")
	}
	manifestBlobPath := filepath.Join(blobDir, manifestDigest[7:])

	// 3. Rewrite the OCI manifest config descriptor (digest + size).
	var manifest map[string]any
	if err := json.Unmarshal(read(manifestBlobPath), &manifest); err != nil {
		panic(err)
	}
	configRef, ok := manifest["config"].(map[string]any)
	if !ok {
		panic("fixture image manifest has no config descriptor")
	}
	configRef["digest"] = "sha256:" + configDigest
	configRef["size"] = len(editedConfig)
	editedManifest, err := json.Marshal(manifest)
	if err != nil {
		panic(err)
	}
	newManifestDigest := digestOf(editedManifest)

	// 4. Rewrite the OCI index entry and annotations.
	manifestEntry["digest"] = "sha256:" + newManifestDigest
	manifestEntry["size"] = len(editedManifest)
	if annotations, ok := manifestEntry["annotations"].(map[string]any); ok {
		annotations["config.digest"] = "sha256:" + configDigest
	}
	editedIndex, err := json.Marshal(index)
	if err != nil {
		panic(err)
	}
	write(filepath.Join(dir, "index.json"), editedIndex)

	// 5. Rewrite the docker-save manifest.json config path.
	entries[0].Config = filepath.ToSlash(filepath.Join("blobs", "sha256", configDigest))
	editedEntries, err := json.Marshal(entries)
	if err != nil {
		panic(err)
	}
	write(filepath.Join(dir, "manifest.json"), editedEntries)

	// 6. Write the new blobs and drop the stale ones.
	write(filepath.Join(blobDir, configDigest), editedConfig)
	write(filepath.Join(blobDir, newManifestDigest), editedManifest)
	if filepath.Clean(oldConfigPath) != filepath.Join(blobDir, configDigest) {
		_ = os.Remove(oldConfigPath)
	}
	if filepath.Clean(manifestBlobPath) != filepath.Join(blobDir, newManifestDigest) {
		_ = os.Remove(manifestBlobPath)
	}
	fmt.Fprintln(os.Stderr, "env-free image config digest: sha256:"+configDigest)
}
'@ | Set-Content -Path $strip -Encoding UTF8
      $env:GOOS = ""
      $env:GOARCH = ""
      $env:CGO_ENABLED = ""
      $env:GO111MODULE = "off"
      $stripExe = Join-Path $work "strip.exe"
      go build -o $stripExe $strip
      & $stripExe $work
    }
    Step "reload the environment-free image" {
      $finalTar = Join-Path $env:TEMP ("dsh-docker-lifecycle-fixture-final-" + [guid]::NewGuid().ToString("N") + ".tar")
      tar -cf $finalTar -C $work .
      Docker-Quiet { docker image rm $tag }
      docker image load -i $finalTar | Out-Null
      Remove-Item $finalTar -Force -ErrorAction SilentlyContinue
    }
    $digest = docker image inspect $tag --format "{{index .RepoDigests 0}}"
    if ($LASTEXITCODE -ne 0 -or $digest -notmatch "@sha256:[0-9a-f]{64}$") {
      throw "fixture image did not bind an exact digest"
    }
    Write-Host "CYBERAGENT_DOCKER_LIFECYCLE_TEST_IMAGE_DIGEST=$digest"
  } finally {
    Pop-Location
  }
} finally {
  Remove-Item -Path (Join-Path $fixtureDir "lifecycle-fixture") -Force -ErrorAction SilentlyContinue
  Remove-Item -Path $work -Recurse -Force -ErrorAction SilentlyContinue
}
