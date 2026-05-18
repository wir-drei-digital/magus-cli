package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParseVersion(t *testing.T) {
	tests := []struct {
		in      string
		want    SemVer
		wantErr bool
	}{
		{"v0.1.0", SemVer{0, 1, 0}, false},
		{"0.1.0", SemVer{0, 1, 0}, false},
		{"v1.2.3", SemVer{1, 2, 3}, false},
		{"  v2.0.0  ", SemVer{2, 0, 0}, false},
		{"", SemVer{}, true},
		{"v1.2", SemVer{}, true},
		{"v1.2.3.4", SemVer{}, true},
		{"vX.Y.Z", SemVer{}, true},
		{"v1.2.beta", SemVer{}, true},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got, err := ParseVersion(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error for %q, got %+v", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestIsNewer(t *testing.T) {
	tests := []struct {
		current string
		latest  string
		want    bool
		wantErr bool
	}{
		{"0.1.0", "0.1.1", true, false},
		{"0.1.1", "0.1.0", false, false},
		{"0.1.0", "0.1.0", false, false},
		{"0.1.0", "0.2.0", true, false},
		{"0.2.0", "1.0.0", true, false},
		{"1.0.0", "0.99.99", false, false},
		{"v0.1.0", "v0.1.1", true, false},
		// "dev" sentinel always counts as older than any real version.
		{"dev", "0.1.0", true, false},
		{"dev", "v9.9.9", true, false},
		// Invalid inputs error.
		{"dev", "bad", false, true},
		{"junk", "0.1.0", false, true},
		{"0.1.0", "junk", false, true},
	}
	for _, tc := range tests {
		t.Run(tc.current+"->"+tc.latest, func(t *testing.T) {
			got, err := IsNewer(tc.current, tc.latest)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("IsNewer(%q,%q) = %v, want %v", tc.current, tc.latest, got, tc.want)
			}
		})
	}
}

func TestSemVerString(t *testing.T) {
	if got := (SemVer{1, 2, 3}).String(); got != "1.2.3" {
		t.Errorf("got %q, want %q", got, "1.2.3")
	}
}

func TestFetchLatestTag(t *testing.T) {
	stub := Release{
		TagName: "v0.1.1",
		Assets: []Asset{
			{Name: "magus_0.1.1_darwin_arm64.tar.gz", BrowserDownloadURL: "http://example.invalid/a.tgz"},
			{Name: "checksums.txt", BrowserDownloadURL: "http://example.invalid/checksums.txt"},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got == "" {
			t.Errorf("missing User-Agent header")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(stub)
	}))
	defer srv.Close()

	c := &Client{
		HTTPClient:  srv.Client(),
		ReleasesURL: srv.URL,
		UserAgent:   "magus-update/test",
	}
	tag, err := c.FetchLatestTag()
	if err != nil {
		t.Fatal(err)
	}
	if tag != "v0.1.1" {
		t.Errorf("got %q, want %q", tag, "v0.1.1")
	}
}

func TestFetchLatestErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer srv.Close()
	c := &Client{HTTPClient: srv.Client(), ReleasesURL: srv.URL}
	if _, err := c.FetchLatest(); err == nil {
		t.Error("expected error on 500 response")
	}
}

func TestFetchLatestMissingTag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := &Client{HTTPClient: srv.Client(), ReleasesURL: srv.URL}
	if _, err := c.FetchLatest(); err == nil {
		t.Error("expected error when tag_name missing")
	}
}

func TestParseChecksumLine(t *testing.T) {
	good := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	tests := []struct {
		name    string
		line    string
		wantSum string
		wantBin string
		wantErr bool
	}{
		{"two-space goreleaser", good + "  magus_0.1.1_darwin_arm64.tar.gz", good, "magus_0.1.1_darwin_arm64.tar.gz", false},
		{"single space", good + " magus.tgz", good, "magus.tgz", false},
		{"tab separator", good + "\tfile.tgz", good, "file.tgz", false},
		{"uppercase hex normalized", "ABCD" + good[4:] + "  f.tgz", "abcd" + good[4:], "f.tgz", false},
		{"empty", "", "", "", true},
		{"missing file", good, "", "", true},
		{"short sum", "deadbeef  f.tgz", "", "", true},
		{"non-hex", "zzzz567890abcdef0123456789abcdef0123456789abcdef0123456789abcdef  f.tgz", "", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sum, name, err := ParseChecksumLine(tc.line)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error, got sum=%q name=%q", sum, name)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if sum != tc.wantSum {
				t.Errorf("sum: got %q, want %q", sum, tc.wantSum)
			}
			if name != tc.wantBin {
				t.Errorf("name: got %q, want %q", name, tc.wantBin)
			}
		})
	}
}

func TestVerifyChecksum(t *testing.T) {
	payload := []byte("hello world\n")
	h := sha256.Sum256(payload)
	hexSum := hex.EncodeToString(h[:])
	checksums := []byte(fmt.Sprintf("%s  magus_0.1.1_darwin_arm64.tar.gz\n%s  other-file.tar.gz\n",
		hexSum,
		"0000000000000000000000000000000000000000000000000000000000000000"))

	tests := []struct {
		name    string
		file    string
		got     string
		wantErr bool
	}{
		{"correct", "magus_0.1.1_darwin_arm64.tar.gz", hexSum, false},
		{"correct uppercase", "magus_0.1.1_darwin_arm64.tar.gz", toUpper(hexSum), false},
		{"mismatch", "magus_0.1.1_darwin_arm64.tar.gz", "1111111111111111111111111111111111111111111111111111111111111111", true},
		{"missing file", "not-in-list.tar.gz", hexSum, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := VerifyChecksum(checksums, tc.file, tc.got)
			if tc.wantErr {
				if err == nil {
					t.Error("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected: %v", err)
			}
		})
	}
}

func TestFindAsset(t *testing.T) {
	rel := &Release{
		TagName: "v0.1.1",
		Assets: []Asset{
			{Name: "magus_0.1.1_linux_amd64.tar.gz"},
			{Name: "checksums.txt"},
		},
	}
	if a, err := FindAsset(rel, "checksums.txt"); err != nil || a.Name != "checksums.txt" {
		t.Errorf("checksums lookup: %v %+v", err, a)
	}
	if _, err := FindAsset(rel, "missing.tgz"); err == nil {
		t.Error("expected error for missing asset")
	}
}

func TestAssetNames(t *testing.T) {
	a, c := AssetNames("0.1.1", "darwin", "arm64")
	if a != "magus_0.1.1_darwin_arm64.tar.gz" {
		t.Errorf("archive name: %q", a)
	}
	if c != "checksums.txt" {
		t.Errorf("checksums name: %q", c)
	}
}

func TestExtractBinary(t *testing.T) {
	dir := t.TempDir()
	tarPath := filepath.Join(dir, "bundle.tar.gz")
	body := []byte("#!/bin/sh\necho hello\n")
	writeTarball(t, tarPath, "magus", body)

	dest := filepath.Join(dir, "out", "magus.new")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ExtractBinary(tarPath, dest); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Error("extracted contents differ")
	}
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Errorf("extracted file not executable: %v", info.Mode().Perm())
	}
}

func TestExtractBinaryMissing(t *testing.T) {
	dir := t.TempDir()
	tarPath := filepath.Join(dir, "bundle.tar.gz")
	writeTarball(t, tarPath, "README", []byte("nope"))
	dest := filepath.Join(dir, "magus.new")
	if err := ExtractBinary(tarPath, dest); err == nil {
		t.Error("expected error when magus binary missing from tar")
	}
}

func TestAtomicReplace(t *testing.T) {
	dir := t.TempDir()
	current := filepath.Join(dir, "magus")
	if err := os.WriteFile(current, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	newBin := filepath.Join(dir, "magus.fresh")
	if err := os.WriteFile(newBin, []byte("NEW"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := AtomicReplace(current, newBin); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(current)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "NEW" {
		t.Errorf("got %q, want %q", got, "NEW")
	}
	// No orphan staging files should remain in dir.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "magus-update-") {
			t.Errorf("orphan staging file: %s", e.Name())
		}
	}
}

// TestAtomicReplaceConcurrent runs two concurrent replacements against
// the same target. With the unique-staging-name fix both calls succeed
// (the second-to-rename wins); without the fix, one would clobber the
// other's staging file mid-rename and return an error.
func TestAtomicReplaceConcurrent(t *testing.T) {
	dir := t.TempDir()
	current := filepath.Join(dir, "magus")
	if err := os.WriteFile(current, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	srcA := filepath.Join(dir, "src-a")
	srcB := filepath.Join(dir, "src-b")
	if err := os.WriteFile(srcA, []byte("AAA"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(srcB, []byte("BBB"), 0o755); err != nil {
		t.Fatal(err)
	}

	errs := make(chan error, 2)
	go func() { errs <- AtomicReplace(current, srcA) }()
	go func() { errs <- AtomicReplace(current, srcB) }()
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Errorf("concurrent AtomicReplace failed: %v", err)
		}
	}

	final, err := os.ReadFile(current)
	if err != nil {
		t.Fatal(err)
	}
	if s := string(final); s != "AAA" && s != "BBB" {
		t.Errorf("unexpected final content: %q", s)
	}
	// Both staging files should be cleaned up by defer.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "magus-update-") {
			t.Errorf("orphan staging file: %s", e.Name())
		}
	}
}

func TestRunCheckOnly(t *testing.T) {
	srv := releaseServer(t, "v0.1.0", nil)
	defer srv.Close()

	c := &Client{
		HTTPClient:    srv.Client(),
		ReleasesURL:   srv.URL,
		UserAgent:     "magus-update/test",
		CurrentBinary: filepath.Join(t.TempDir(), "magus"),
	}
	res, err := c.Run("0.1.0", Options{CheckOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.UpToDate {
		t.Errorf("expected UpToDate, got %+v", res)
	}
}

func TestRunFullUpdate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("self-update not supported on windows")
	}

	dir := t.TempDir()
	currentBin := filepath.Join(dir, "magus")
	if err := os.WriteFile(currentBin, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Build a release tarball + checksums payload.
	tarBytes := buildTarballBytes(t, "magus", []byte("FRESH-BINARY-CONTENT"))
	h := sha256.Sum256(tarBytes)
	hexSum := hex.EncodeToString(h[:])

	goos, goarch := runtime.GOOS, runtime.GOARCH
	archiveName, _ := AssetNames("0.1.1", goos, goarch)
	checksums := fmt.Sprintf("%s  %s\n", hexSum, archiveName)

	srv := releaseServer(t, "v0.1.1", map[string][]byte{
		archiveName:     tarBytes,
		"checksums.txt": []byte(checksums),
	})
	defer srv.Close()

	c := &Client{
		HTTPClient:    srv.Client(),
		ReleasesURL:   srv.URL + "/latest",
		UserAgent:     "magus-update/test",
		CurrentBinary: currentBin,
	}
	res, err := c.Run("0.1.0", Options{GOOS: goos, GOARCH: goarch})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if !res.Updated {
		t.Fatalf("expected Updated=true: %+v", res)
	}
	got, err := os.ReadFile(currentBin)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "FRESH-BINARY-CONTENT" {
		t.Errorf("binary not replaced; got %q", got)
	}
}

func TestRunWindowsRefused(t *testing.T) {
	c := &Client{
		HTTPClient:    &http.Client{},
		ReleasesURL:   "http://example.invalid/never-called",
		CurrentBinary: filepath.Join(t.TempDir(), "magus"),
	}
	if _, err := c.Run("0.1.0", Options{GOOS: "windows", GOARCH: "amd64"}); err == nil {
		t.Fatal("expected Windows refusal")
	}
}

func TestRunChecksumMismatchAborts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip()
	}
	dir := t.TempDir()
	currentBin := filepath.Join(dir, "magus")
	if err := os.WriteFile(currentBin, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	tarBytes := buildTarballBytes(t, "magus", []byte("PAYLOAD"))
	goos, goarch := runtime.GOOS, runtime.GOARCH
	archiveName, _ := AssetNames("0.1.1", goos, goarch)
	// Wrong sha intentionally.
	checksums := "0000000000000000000000000000000000000000000000000000000000000000  " + archiveName + "\n"
	srv := releaseServer(t, "v0.1.1", map[string][]byte{
		archiveName:     tarBytes,
		"checksums.txt": []byte(checksums),
	})
	defer srv.Close()

	c := &Client{
		HTTPClient:    srv.Client(),
		ReleasesURL:   srv.URL + "/latest",
		UserAgent:     "magus-update/test",
		CurrentBinary: currentBin,
	}
	_, err := c.Run("0.1.0", Options{GOOS: goos, GOARCH: goarch})
	if err == nil {
		t.Fatal("expected checksum mismatch error")
	}
	// Current binary must still hold the old contents.
	got, err := os.ReadFile(currentBin)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "OLD" {
		t.Errorf("binary was replaced despite checksum failure: %q", got)
	}
}

// helpers

func writeTarball(t *testing.T, path, name string, body []byte) {
	t.Helper()
	if err := os.WriteFile(path, buildTarballBytes(t, name, body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func buildTarballBytes(t *testing.T, name string, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	hdr := &tar.Header{
		Name:     name,
		Mode:     0o755,
		Size:     int64(len(body)),
		Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// releaseServer returns an httptest server that serves /latest as a
// stub releases JSON whose asset URLs point back to /assets/<name>.
// Asset bodies come from the assets map.
func releaseServer(t *testing.T, tag string, assets map[string][]byte) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/latest", func(w http.ResponseWriter, r *http.Request) {
		rel := Release{TagName: tag}
		for name := range assets {
			rel.Assets = append(rel.Assets, Asset{
				Name:               name,
				BrowserDownloadURL: srv.URL + "/assets/" + name,
			})
		}
		_ = json.NewEncoder(w).Encode(rel)
	})
	mux.HandleFunc("/assets/", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Path[len("/assets/"):]
		body, ok := assets[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = io.Copy(w, bytes.NewReader(body))
	})
	// Default handler (used when ReleasesURL has no /latest suffix).
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		rel := Release{TagName: tag}
		_ = json.NewEncoder(w).Encode(rel)
	})
	srv = httptest.NewServer(mux)
	return srv
}

func toUpper(s string) string {
	out := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' {
			c -= 32
		}
		out[i] = c
	}
	return string(out)
}
