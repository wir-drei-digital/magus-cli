// Package update implements in-place self-update for the magus CLI.
//
// It fetches the latest GitHub release, verifies the SHA-256 checksum of
// the published tarball against checksums.txt, and atomically replaces
// the running binary via os.Rename on the same filesystem.
package update

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// DefaultReleasesURL is the GitHub API endpoint for the latest release.
const DefaultReleasesURL = "https://api.github.com/repos/wir-drei-digital/magus-cli/releases/latest"

// devVersion is the sentinel string used for the local build-from-source build.
const devVersion = "dev"

// SemVer is a parsed MAJOR.MINOR.PATCH version. Pre-release suffixes are
// not handled because magus releases use plain three-part tags.
type SemVer struct {
	Major int
	Minor int
	Patch int
}

// ParseVersion parses a tag like "v0.1.1" or "0.1.1" into a SemVer.
// Returns an error for anything that doesn't have exactly three numeric
// dot-separated components.
func ParseVersion(s string) (SemVer, error) {
	clean := strings.TrimPrefix(strings.TrimSpace(s), "v")
	if clean == "" {
		return SemVer{}, errors.New("empty version string")
	}
	parts := strings.Split(clean, ".")
	if len(parts) != 3 {
		return SemVer{}, fmt.Errorf("invalid version %q: expected MAJOR.MINOR.PATCH", s)
	}
	out := SemVer{}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return SemVer{}, fmt.Errorf("invalid version %q: %w", s, err)
		}
		if n < 0 {
			return SemVer{}, fmt.Errorf("invalid version %q: negative component", s)
		}
		switch i {
		case 0:
			out.Major = n
		case 1:
			out.Minor = n
		case 2:
			out.Patch = n
		}
	}
	return out, nil
}

// String renders as MAJOR.MINOR.PATCH without the leading "v".
func (v SemVer) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// IsNewer reports whether latest is strictly greater than current.
// The "dev" sentinel always counts as older than any real version, so
// dev builds will install the latest tagged release without --force.
func IsNewer(current, latest string) (bool, error) {
	if strings.TrimSpace(current) == devVersion {
		// Validate latest still parses so we don't try to install garbage.
		if _, err := ParseVersion(latest); err != nil {
			return false, err
		}
		return true, nil
	}
	c, err := ParseVersion(current)
	if err != nil {
		return false, fmt.Errorf("current: %w", err)
	}
	l, err := ParseVersion(latest)
	if err != nil {
		return false, fmt.Errorf("latest: %w", err)
	}
	if l.Major != c.Major {
		return l.Major > c.Major, nil
	}
	if l.Minor != c.Minor {
		return l.Minor > c.Minor, nil
	}
	return l.Patch > c.Patch, nil
}

// Release is the minimal subset of the GitHub releases payload that we use.
type Release struct {
	TagName string  `json:"tag_name"`
	Assets  []Asset `json:"assets"`
}

// Asset is one downloadable file attached to a release.
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// Client fetches releases and downloads assets. Inject a custom HTTPClient
// and ReleasesURL in tests to point at an httptest server.
type Client struct {
	HTTPClient    *http.Client
	ReleasesURL   string
	UserAgent     string
	CurrentBinary string
}

// NewClient returns a Client with a 60-second HTTP timeout and the
// production GitHub URL.
func NewClient(userAgent string) *Client {
	return &Client{
		HTTPClient:  &http.Client{Timeout: 60 * time.Second},
		ReleasesURL: DefaultReleasesURL,
		UserAgent:   userAgent,
	}
}

// FetchLatest fetches the latest release JSON from GitHub.
func (c *Client) FetchLatest() (*Release, error) {
	req, err := http.NewRequest(http.MethodGet, c.ReleasesURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch latest release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("github returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var rel Release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("decode releases JSON: %w", err)
	}
	if rel.TagName == "" {
		return nil, errors.New("release payload missing tag_name")
	}
	return &rel, nil
}

// FetchLatestTag is a convenience wrapper that returns just the tag.
func (c *Client) FetchLatestTag() (string, error) {
	rel, err := c.FetchLatest()
	if err != nil {
		return "", err
	}
	return rel.TagName, nil
}

// AssetNames returns the archive and checksum file names for a version.
func AssetNames(version, goos, goarch string) (archive, checksums string) {
	archive = fmt.Sprintf("magus_%s_%s_%s.tar.gz", version, goos, goarch)
	checksums = "checksums.txt"
	return
}

// FindAsset returns the asset with the given name, or an error if missing.
func FindAsset(rel *Release, name string) (*Asset, error) {
	for i := range rel.Assets {
		if rel.Assets[i].Name == name {
			return &rel.Assets[i], nil
		}
	}
	return nil, fmt.Errorf("release %s has no asset %q", rel.TagName, name)
}

// DownloadTo streams an asset URL to a file, returning the SHA-256 of the
// downloaded bytes.
func (c *Client) DownloadTo(url, dest string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s returned %d", url, resp.StatusCode)
	}

	f, err := os.Create(dest)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	tee := io.TeeReader(resp.Body, h)
	if _, err := io.Copy(f, tee); err != nil {
		return "", fmt.Errorf("write %s: %w", dest, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// FetchBytes downloads a small asset entirely into memory. Used for the
// checksums file.
func (c *Client) FetchBytes(url string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s returned %d", url, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// ParseChecksumLine parses one goreleaser-style line:
//
//	<sha256-hex>  <filename>
//
// Returns the hex digest and filename. Whitespace between the two
// fields is tolerated as one-or-more runs of spaces.
func ParseChecksumLine(line string) (sum, name string, err error) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return "", "", errors.New("empty line")
	}
	fields := strings.Fields(trimmed)
	if len(fields) != 2 {
		return "", "", fmt.Errorf("expected 2 fields, got %d", len(fields))
	}
	sum = strings.ToLower(fields[0])
	name = fields[1]
	if len(sum) != 64 {
		return "", "", fmt.Errorf("invalid sha256 length: %d", len(sum))
	}
	if _, err := hex.DecodeString(sum); err != nil {
		return "", "", fmt.Errorf("invalid sha256: %w", err)
	}
	return sum, name, nil
}

// FindChecksum scans checksums.txt content for the given filename and
// returns its hex digest. Errors if the row is missing or malformed.
func FindChecksum(checksums []byte, filename string) (string, error) {
	for _, line := range strings.Split(string(checksums), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		sum, name, err := ParseChecksumLine(line)
		if err != nil {
			// Skip lines that don't parse; goreleaser sometimes adds a
			// signed-checksums header in future versions. We only fail
			// at the end if no match was found.
			continue
		}
		if name == filename {
			return sum, nil
		}
	}
	return "", fmt.Errorf("checksum not found for %s", filename)
}

// VerifyChecksum confirms that gotHex (already lowercase hex) matches
// the expected digest for filename in the given checksums.txt body.
func VerifyChecksum(checksums []byte, filename, gotHex string) error {
	want, err := FindChecksum(checksums, filename)
	if err != nil {
		return err
	}
	if !strings.EqualFold(want, gotHex) {
		return fmt.Errorf("checksum mismatch for %s: want %s, got %s", filename, want, gotHex)
	}
	return nil
}

// ExtractBinary scans a gzipped tar for an entry named "magus" (or with
// that basename) and writes it to destPath with 0o755 perms.
func ExtractBinary(tarballPath, destPath string) error {
	f, err := os.Open(tarballPath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gunzip %s: %w", tarballPath, err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("no magus binary found in %s", tarballPath)
		}
		if err != nil {
			return fmt.Errorf("read tar: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		base := filepath.Base(hdr.Name)
		if base != "magus" {
			continue
		}
		out, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			return fmt.Errorf("write binary: %w", err)
		}
		if err := out.Close(); err != nil {
			return err
		}
		return os.Chmod(destPath, 0o755)
	}
}

// ResolveBinaryPath returns the on-disk path of the running binary,
// following symlinks so that symlinked installs are updated in place
// at the real location.
func ResolveBinaryPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate executable: %w", err)
	}
	real, err := filepath.EvalSymlinks(exe)
	if err != nil {
		// Fall back to the unresolved path. EvalSymlinks can fail on
		// some Go test harnesses where the binary lives in /tmp.
		return exe, nil
	}
	return real, nil
}

// AtomicReplace writes newBinary to <currentBinary>.new in the same
// directory and renames it over currentBinary. On Unix this is rename(2),
// which atomically replaces the dest entry; the running process keeps
// its open file handle for the old inode, so the running command keeps
// executing fine.
//
// Windows is rejected at a higher layer; this function would also fail
// there because rename can't replace a file that is open for execute.
func AtomicReplace(currentBinary, newBinary string) error {
	target := currentBinary + ".new"
	// Move (or copy) the new file into the same directory so rename is
	// guaranteed to stay on one filesystem.
	if err := moveOrCopy(newBinary, target); err != nil {
		return err
	}
	if err := os.Chmod(target, 0o755); err != nil {
		_ = os.Remove(target)
		return err
	}
	if err := os.Rename(target, currentBinary); err != nil {
		_ = os.Remove(target)
		return fmt.Errorf("rename %s -> %s: %w", target, currentBinary, err)
	}
	return nil
}

func moveOrCopy(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	// Cross-device rename — fall back to copy + remove.
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(dst)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(dst)
		return err
	}
	return os.Remove(src)
}

// Result describes the outcome of a Run call. Exactly one of UpToDate
// or Updated is true on success.
type Result struct {
	CurrentVersion string
	LatestVersion  string
	UpToDate       bool
	Updated        bool
	BinaryPath     string
}

// Options control Run behavior.
type Options struct {
	CheckOnly bool
	Force     bool
	GOOS      string
	GOARCH    string
}

// Run performs the full self-update sequence: fetch, compare, optionally
// download, verify, extract, and atomically replace.
func (c *Client) Run(currentVersion string, opts Options) (*Result, error) {
	goos := opts.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	goarch := opts.GOARCH
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	if goos == "windows" {
		return nil, errors.New("self-update is not supported on Windows yet. Re-run install.sh or download a new binary from GitHub Releases.")
	}

	binPath := c.CurrentBinary
	if binPath == "" {
		var err error
		binPath, err = ResolveBinaryPath()
		if err != nil {
			return nil, err
		}
	}

	rel, err := c.FetchLatest()
	if err != nil {
		return nil, err
	}
	latestVer, err := ParseVersion(rel.TagName)
	if err != nil {
		return nil, err
	}
	latestStr := latestVer.String()

	newer, err := IsNewer(currentVersion, latestStr)
	if err != nil {
		return nil, err
	}

	res := &Result{
		CurrentVersion: currentVersion,
		LatestVersion:  latestStr,
		BinaryPath:     binPath,
	}

	if !newer && !opts.Force {
		res.UpToDate = true
		return res, nil
	}
	if opts.CheckOnly {
		return res, nil
	}

	archiveName, checksumsName := AssetNames(latestStr, goos, goarch)
	archiveAsset, err := FindAsset(rel, archiveName)
	if err != nil {
		return nil, err
	}
	checksumsAsset, err := FindAsset(rel, checksumsName)
	if err != nil {
		return nil, err
	}

	tmpDir, err := os.MkdirTemp("", "magus-update-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	tarballPath := filepath.Join(tmpDir, archiveName)
	gotHex, err := c.DownloadTo(archiveAsset.BrowserDownloadURL, tarballPath)
	if err != nil {
		return nil, err
	}

	checksumsBytes, err := c.FetchBytes(checksumsAsset.BrowserDownloadURL)
	if err != nil {
		return nil, err
	}
	if err := VerifyChecksum(checksumsBytes, archiveName, gotHex); err != nil {
		return nil, err
	}

	newBin := filepath.Join(tmpDir, "magus.new")
	if err := ExtractBinary(tarballPath, newBin); err != nil {
		return nil, err
	}

	// Place the staging file in the destination directory so rename is
	// guaranteed to be on the same filesystem.
	stagingDir := filepath.Dir(binPath)
	staging := filepath.Join(stagingDir, "magus.update.tmp")
	if err := moveOrCopy(newBin, staging); err != nil {
		return nil, err
	}
	if err := os.Chmod(staging, 0o755); err != nil {
		_ = os.Remove(staging)
		return nil, err
	}
	if err := os.Rename(staging, binPath); err != nil {
		_ = os.Remove(staging)
		return nil, fmt.Errorf("atomic replace failed: %w", err)
	}

	res.Updated = true
	return res, nil
}
