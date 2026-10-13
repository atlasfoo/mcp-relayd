//go:build linux

package release_test

import (
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var releaseTargets = []struct {
	goos, goarch, suffix, extension string
}{
	{"linux", "amd64", "tar.gz", ""},
	{"linux", "arm64", "tar.gz", ""},
	{"darwin", "amd64", "tar.gz", ""},
	{"darwin", "arm64", "tar.gz", ""},
	{"windows", "amd64", "zip", ".exe"},
	{"windows", "arm64", "zip", ".exe"},
}

func TestReleasePackagingSixTargetsAndChecksums(t *testing.T) {
	for _, tc := range releaseTargets {
		t.Run(tc.goos+"-"+tc.goarch, func(t *testing.T) {
			f := newFixture(t)
			out := filepath.Join(t.TempDir(), "assets")
			mockGo := installMockGo(t, &f, tc.goos, tc.goarch)
			if _, err := f.command(t, "mkdir", "-p", out); err != nil {
				t.Fatal(err)
			}
			invokePackaging(t, f, "release-package", "1.2.3", "source-abc", "run-42", out)
			calls := read(t, filepath.Join(filepath.Dir(mockGo), "calls"))
			if !strings.Contains(calls, "-X main.version=1.2.3") || !strings.Contains(calls, "-X main.sourceSHA=source-abc") {
				t.Errorf("build did not embed candidate version and source SHA: %s", calls)
			}
			if !strings.Contains(calls, "CGO_ENABLED=0") {
				t.Errorf("build did not disable cgo: %s", calls)
			}
			if len(strings.Split(strings.TrimSpace(calls), "\n")) != len(releaseTargets) || !strings.Contains(calls, tc.goos+" "+tc.goarch+" CGO_ENABLED=0 build -trimpath") {
				t.Errorf("missing cross-build target or trimpath: %s", calls)
			}
			name := fmt.Sprintf("mcp-relayd_1.2.3_%s_%s.%s", tc.goos, tc.goarch, tc.suffix)
			asset := filepath.Join(out, name)
			if _, err := os.Stat(asset); err != nil {
				t.Fatalf("missing package %s: %v", name, err)
			}
			if tc.goos == "windows" {
				assertZipExecutable(t, asset, "mcp-relayd.exe")
			} else {
				assertGzipExecutable(t, asset, "mcp-relayd")
			}
		})
	}
}

func TestReleasePackagingManifestAndReproducibleChecksums(t *testing.T) {
	f := newFixture(t)
	out := filepath.Join(t.TempDir(), "assets")
	if _, err := f.command(t, "mkdir", "-p", out); err != nil {
		t.Fatal(err)
	}
	installMockGo(t, &f, "linux", "amd64")
	invokePackaging(t, f, "release-package", "1.2.3", "source-abc", "run-42", out)
	manifestPath := filepath.Join(out, "manifest.json")
	first, err := os.ReadFile(manifestPath) // #nosec G304 -- manifestPath is in t.TempDir.
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	var manifest struct {
		Source  string `json:"source_sha"`
		Run     string `json:"run_id"`
		Version string `json:"version"`
		Assets  []struct {
			Name   string `json:"name"`
			SHA256 string `json:"sha256"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(first, &manifest); err != nil {
		t.Fatalf("manifest JSON: %v", err)
	}
	if manifest.Source != "source-abc" || manifest.Run != "run-42" || manifest.Version != "1.2.3" || len(manifest.Assets) != len(releaseTargets) {
		t.Fatalf("manifest metadata/assets = %+v", manifest)
	}
	for _, asset := range manifest.Assets {
		data, err := os.ReadFile(filepath.Join(out, filepath.Base(asset.Name))) // #nosec G304 -- manifest entries resolve only to assets in the temporary output directory.
		if err != nil {
			t.Fatalf("asset %s: %v", asset.Name, err)
		}
		sum := sha256.Sum256(data)
		if asset.SHA256 != hex.EncodeToString(sum[:]) {
			t.Errorf("checksum for %s = %s", asset.Name, asset.SHA256)
		}
	}
	var verified struct {
		Verified bool `json:"verified"`
	}
	f.recipe(t, &verified, "release-verify", out)
	if !verified.Verified {
		t.Fatal("verification did not confirm intact assets")
	}
	second := filepath.Join(t.TempDir(), "again")
	if _, err := f.command(t, "mkdir", "-p", second); err != nil {
		t.Fatal(err)
	}
	invokePackaging(t, f, "release-package", "1.2.3", "source-abc", "run-42", second)
	secondManifest, err := os.ReadFile(filepath.Join(second, "manifest.json")) // #nosec G304 -- path is in t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(secondManifest) {
		t.Errorf("manifest differs between identical builds")
	}
}

func TestReleaseVerifyRejectsModifiedAsset(t *testing.T) {
	f := newFixture(t)
	out := filepath.Join(t.TempDir(), "assets")
	if _, err := f.command(t, "mkdir", "-p", out); err != nil {
		t.Fatal(err)
	}
	installMockGo(t, &f, "linux", "amd64")
	invokePackaging(t, f, "release-package", "1.2.3", "source-abc", "run-42", out)
	asset := filepath.Join(out, "mcp-relayd_1.2.3_linux_amd64.tar.gz")
	file, err := os.OpenFile(asset, os.O_APPEND|os.O_WRONLY, 0) // #nosec G304 -- asset path is fixed under t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("tampered"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if output, err := f.invoke(t, "release-verify", out); err == nil {
		t.Fatalf("verification accepted tampered asset: %s", output)
	}
}

func TestReleaseVerifyRejectsUnsafeOrIncompleteManifest(t *testing.T) {
	for _, tc := range []string{"traversal", "duplicate", "missing", "symlink", "unsafe-zip"} {
		t.Run(tc, func(t *testing.T) {
			f := newFixture(t)
			installMockGo(t, &f, "linux", "amd64")
			out := filepath.Join(t.TempDir(), "assets")
			invokePackaging(t, f, "release-package", "1.2.3", "source-abc", "run-42", out)
			manifestPath := filepath.Join(out, "manifest.json")
			var manifest struct {
				Source  string              `json:"source_sha"`
				Run     string              `json:"run_id"`
				Version string              `json:"version"`
				Assets  []map[string]string `json:"assets"`
			}
			decode(t, read(t, manifestPath), &manifest)
			switch tc {
			case "traversal":
				manifest.Assets[0]["name"] = "../outside.tar.gz"
			case "duplicate":
				manifest.Assets[1] = manifest.Assets[0]
			case "missing":
				manifest.Assets = manifest.Assets[:5]
			case "symlink":
				asset := filepath.Join(out, manifest.Assets[0]["name"])
				outside := filepath.Join(t.TempDir(), "outside")
				if err := os.Rename(asset, outside); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, asset); err != nil {
					t.Fatal(err)
				}
			case "unsafe-zip":
				asset := manifest.Assets[4]
				var data strings.Builder
				archive := zip.NewWriter(&data)
				member, err := archive.Create("../mcp-relayd.exe")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := io.WriteString(member, "unsafe"); err != nil {
					t.Fatal(err)
				}
				if err := archive.Close(); err != nil {
					t.Fatal(err)
				}
				write(t, filepath.Join(out, asset["name"]), data.String(), 0o600)
				sum := sha256.Sum256([]byte(data.String()))
				asset["sha256"] = hex.EncodeToString(sum[:])
			}
			data, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			write(t, manifestPath, string(data), 0o600)
			if output, err := f.invoke(t, "release-verify", out); err == nil {
				t.Fatalf("verification accepted %s: %s", tc, output)
			}
		})
	}
}

func installMockGo(t *testing.T, f *fixture, goos, goarch string) string {
	t.Helper()
	dir := t.TempDir()
	executable := filepath.Join(dir, "go")
	script := fmt.Sprintf("#!/bin/sh\nset -eu\nprintf '%%s %%s CGO_ENABLED=%%s %%s\\n' \"$GOOS\" \"$GOARCH\" \"$CGO_ENABLED\" \"$*\" >> %q\nout=\nwhile [ $# -gt 0 ]; do if [ \"$1\" = -o ]; then shift; out=$1; fi; shift; done\nmkdir -p \"$(dirname \"$out\")\"\nprintf 'mcp-relayd %s source-abc\\n' > \"$out\"\n", filepath.Join(dir, "calls"), goos+"-"+goarch)
	write(t, executable, script, 0o700)
	// Mutate the caller's environment; a value copy silently used real Go.
	f.env = append(f.env, "GO="+executable)
	return executable
}

func invokePackaging(t *testing.T, f fixture, args ...string) {
	t.Helper()
	if output, err := f.invoke(t, args[0], args[1:]...); err != nil {
		t.Fatalf("%s: %v: %s", args[0], err, output)
	}
}

func assertZipExecutable(t *testing.T, path, expected string) {
	t.Helper()
	archive, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := archive.Close(); err != nil {
			t.Errorf("close zip: %v", err)
		}
	}()
	for _, file := range archive.File {
		if file.Name == expected {
			return
		}
	}
	t.Fatalf("zip %s lacks %s", path, expected)
}

func assertGzipExecutable(t *testing.T, path, expected string) {
	t.Helper()
	file, err := os.Open(path) // #nosec G304 -- package path is generated beneath t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Errorf("close gzip file: %v", err)
		}
	}()
	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := reader.Close(); err != nil {
			t.Errorf("close gzip reader: %v", err)
		}
	}()
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), expected) {
		t.Fatalf("tar.gz %s lacks %s", path, expected)
	}
}
