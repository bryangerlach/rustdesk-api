package api

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lejianwen/rustdesk-api/v2/global"
	"github.com/lejianwen/rustdesk-api/v2/model"
	"github.com/sirupsen/logrus"
)

// RustDesk's built-in updater (1.4.x+) expects:
//   1. POST /api/version/latest -> flat {"url": ".../rustdesk/rustdesk/releases/tag/<release_id>"}
//   2. GET  <prefix>/rustdesk-update.json      (signed metadata)
//   3. GET  <prefix>/rustdesk-update.json.sig  (JSON signature envelope)
//   4. GET  <prefix>/<artifact file>
// where <prefix> is /rustdesk/rustdesk/releases/download/<release_id>/.
const (
	updateReleasePath      = "/rustdesk/rustdesk/releases"
	updateKeyID            = "custom-update"
	updateSignatureAlgo    = "ed25519"
	updateMetadataContext  = "RustDesk update metadata v1\n"
	updateMetadataFilename = "rustdesk-update.json"
	updateSignatureSuffix  = ".sig"
)

type updateArtifact struct {
	Platform string `json:"platform"`
	Arch     string `json:"arch"`
	Format   string `json:"format"`
	URL      string `json:"url"`
	FileName string `json:"file_name"`
	Size     int64  `json:"size"`
	Sha256   string `json:"sha256"`
}

type updateMetadata struct {
	SchemaVersion  int              `json:"schema_version"`
	App            string           `json:"app"`
	PackageID      string           `json:"package_id"`
	Version        string           `json:"version"`
	ReleaseID      string           `json:"release_id"`
	PublishedAt    string           `json:"published_at"`
	SignatureKeyID string           `json:"signature_key_id"`
	Artifacts      []updateArtifact `json:"artifacts"`
}

type updateSignature struct {
	SchemaVersion int    `json:"schema_version"`
	Algorithm     string `json:"algorithm"`
	KeyID         string `json:"key_id"`
	Signature     string `json:"signature"`
}

func apiServerBase() string {
	return strings.TrimRight(global.Config.Rustdesk.ApiServer, "/")
}

// appVersionFor is the version embedded into the rdgen-built client as
// app_version: <version>+<build id padded to 3 digits>. The client compares
// this against the release id returned by /version/latest.
func appVersionFor(cb *model.ClientBuild) string {
	return fmt.Sprintf("%s+%03d", cb.Version, cb.Id)
}

func releaseIDFor(cb *model.ClientBuild) string {
	return "v" + appVersionFor(cb)
}

func releaseTagURL(releaseID string) string {
	return apiServerBase() + updateReleasePath + "/tag/" + releaseID
}

func releaseDownloadBase(releaseID string) string {
	return apiServerBase() + updateReleasePath + "/download/" + releaseID + "/"
}

func parseReleaseID(releaseID string) (version string, id uint, ok bool) {
	s := strings.TrimPrefix(releaseID, "v")
	parts := strings.SplitN(s, "+", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", 0, false
	}
	n, err := strconv.Atoi(parts[1])
	if err != nil || n <= 0 {
		return "", 0, false
	}
	return parts[0], uint(n), true
}

func findBuildByReleaseID(releaseID string) (*model.ClientBuild, error) {
	version, id, ok := parseReleaseID(releaseID)
	if !ok {
		return nil, fmt.Errorf("invalid release id: %s", releaseID)
	}
	cb := &model.ClientBuild{}
	if err := global.DB.Where("id = ? AND version = ? AND status = ?", id, version, "done").
		First(cb).Error; err != nil {
		return nil, err
	}
	cb.DecodeFiles()
	return cb, nil
}

type artifactCandidate struct {
	artifact updateArtifact
	priority int
}

// inferArtifact maps a build target + filename to the (platform, arch, format)
// tuple that the RustDesk updater queries for.
func inferArtifact(target, file string) (platform, arch, format string, priority int, ok bool) {
	t := strings.ToLower(target)
	f := strings.ToLower(file)
	ext := strings.ToLower(filepath.Ext(file))

	switch {
	case strings.HasPrefix(t, "windows"), ext == ".exe", ext == ".msi":
		platform = "windows"
	case strings.HasPrefix(t, "macos"), ext == ".dmg":
		platform = "macos"
	case strings.HasPrefix(t, "android"), ext == ".apk":
		platform = "android"
	case strings.HasPrefix(t, "linux"), ext == ".deb", ext == ".rpm", ext == ".appimage",
		ext == ".flatpak", strings.HasSuffix(f, ".pkg.tar.zst"):
		platform = "linux"
	default:
		return "", "", "", 0, false
	}

	switch {
	case strings.Contains(f, "aarch64"), strings.Contains(f, "arm64-v8a"), strings.Contains(f, "arm64"):
		arch = "aarch64"
	case strings.Contains(f, "x86_64"), strings.Contains(f, "amd64"):
		arch = "x86_64"
	case strings.Contains(f, "armv7"), strings.Contains(f, "armeabi"), strings.Contains(f, "armhf"):
		arch = "armv7"
	case strings.Contains(f, "i386"), strings.Contains(f, "i686"), strings.Contains(f, "x86"):
		arch = "x86"
	default:
		arch = defaultArch(platform, t)
	}

	switch platform {
	case "windows":
		switch ext {
		case ".exe":
			return platform, arch, "exe", 2, true
		case ".msi":
			return platform, arch, "msi", 1, true
		}
		return "", "", "", 0, false
	case "macos":
		if ext != ".dmg" {
			return "", "", "", 0, false
		}
		return platform, arch, "dmg", 1, true
	case "linux":
		// The client asks for format "unknown" on Linux; pick one package per
		// arch to avoid "multiple matching update artifacts".
		switch {
		case ext == ".deb":
			p := 5
			// Prefer the plain package over unattended/wayland variants.
			if strings.Contains(f, "unattended") || strings.Contains(f, "wayland") {
				p = 4
			}
			return platform, arch, "unknown", p, true
		case ext == ".appimage":
			return platform, arch, "unknown", 3, true
		case ext == ".rpm":
			return platform, arch, "unknown", 2, true
		case ext == ".flatpak":
			return platform, arch, "unknown", 1, true
		case strings.HasSuffix(f, ".pkg.tar.zst"):
			return platform, arch, "unknown", 0, true
		}
		return "", "", "", 0, false
	case "android":
		if ext != ".apk" {
			return "", "", "", 0, false
		}
		return platform, arch, "unknown", 1, true
	}
	return "", "", "", 0, false
}

func defaultArch(platform, target string) string {
	switch {
	case strings.Contains(target, "aarch64"), strings.Contains(target, "arm64"):
		return "aarch64"
	case strings.Contains(target, "x86_64"), strings.Contains(target, "amd64"):
		return "x86_64"
	case strings.Contains(target, "windows-x86"),
		strings.Contains(target, "windows-386"),
		strings.Contains(target, "windows-i386"),
		strings.Contains(target, "android-x86"),
		strings.Contains(target, "-i386"),
		strings.Contains(target, "-386"):
		return "x86"
	case strings.Contains(target, "armeabi"), strings.Contains(target, "armv7"), strings.Contains(target, "armhf"):
		return "armv7"
	case platform == "android":
		return "aarch64"
	default:
		return "x86_64"
	}
}

func fileSizeAndSHA256(path string) (int64, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return 0, "", err
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return 0, "", err
	}
	return info.Size(), hex.EncodeToString(h.Sum(nil)), nil
}

func collectArtifacts(cb *model.ClientBuild, releaseID string) []updateArtifact {
	idStr := strconv.Itoa(int(cb.Id))
	base := releaseDownloadBase(releaseID)
	best := make(map[string]artifactCandidate)

	for _, file := range cb.FileList {
		platform, arch, format, priority, ok := inferArtifact(cb.Target, file)
		if !ok {
			continue
		}
		key := platform + "|" + arch + "|" + format
		if cur, exists := best[key]; exists && cur.priority >= priority {
			continue
		}
		localPath := filepath.Join(global.Config.Gin.ResourcesPath, "public", "upload", "client_build", idStr, file)
		size, hash, err := fileSizeAndSHA256(localPath)
		if err != nil {
			global.Logger.WithFields(logrus.Fields{
				"build_id": cb.Id,
				"file":     file,
				"path":     localPath,
			}).WithError(err).Warn("update metadata: artifact missing on disk, skipping")
			continue
		}
		best[key] = artifactCandidate{
			priority: priority,
			artifact: updateArtifact{
				Platform: platform,
				Arch:     arch,
				Format:   format,
				URL:      base + file,
				FileName: file,
				Size:     size,
				Sha256:   hash,
			},
		}
	}

	artifacts := make([]updateArtifact, 0, len(best))
	for _, c := range best {
		artifacts = append(artifacts, c.artifact)
	}
	sort.Slice(artifacts, func(i, j int) bool {
		a, b := artifacts[i], artifacts[j]
		if a.Platform != b.Platform {
			return a.Platform < b.Platform
		}
		if a.Arch != b.Arch {
			return a.Arch < b.Arch
		}
		return a.Format < b.Format
	})
	return artifacts
}

func buildUpdateMetadataBytes(cb *model.ClientBuild) ([]byte, error) {
	cb.DecodeFiles()
	releaseID := releaseIDFor(cb)

	publishedAt := "1970-01-01T00:00:00Z"
	if t := time.Time(cb.CreatedAt); !t.IsZero() {
		publishedAt = t.UTC().Format(time.RFC3339)
	}

	meta := updateMetadata{
		SchemaVersion:  1,
		App:            "rustdesk",
		PackageID:      "rustdesk",
		Version:        appVersionFor(cb),
		ReleaseID:      releaseID,
		PublishedAt:    publishedAt,
		SignatureKeyID: updateKeyID,
		Artifacts:      collectArtifacts(cb, releaseID),
	}
	return json.Marshal(meta)
}

func signUpdateMetadataBytes(metadataBytes []byte) ([]byte, error) {
	key, err := loadUpdateSigningKey()
	if err != nil {
		return nil, err
	}
	signed := make([]byte, 0, len(updateMetadataContext)+len(metadataBytes))
	signed = append(signed, []byte(updateMetadataContext)...)
	signed = append(signed, metadataBytes...)
	sig := ed25519.Sign(key, signed)

	env := updateSignature{
		SchemaVersion: 1,
		Algorithm:     updateSignatureAlgo,
		KeyID:         updateKeyID,
		Signature:     base64.StdEncoding.EncodeToString(sig),
	}
	return json.Marshal(env)
}

// DownloadReleaseFile serves the updater's metadata, signature, and artifacts.
// Route: GET /rustdesk/rustdesk/releases/download/:releaseId/:filename
func (u *Update) DownloadReleaseFile(c *gin.Context) {
	releaseID := c.Param("releaseId")
	fileName := c.Param("filename")

	cb, err := findBuildByReleaseID(releaseID)
	if err != nil {
		global.Logger.WithFields(logrus.Fields{
			"release_id": releaseID,
			"file":       fileName,
		}).WithError(err).Warn("update release: build not found")
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}

	switch fileName {
	case updateMetadataFilename:
		metadataBytes, err := buildUpdateMetadataBytes(cb)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", metadataBytes)
	case updateMetadataFilename + updateSignatureSuffix:
		metadataBytes, err := buildUpdateMetadataBytes(cb)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		sigBytes, err := signUpdateMetadataBytes(metadataBytes)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", sigBytes)
	default:
		if fileName == "" || filepath.Base(fileName) != fileName {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		cb.DecodeFiles()
		allowed := false
		for _, f := range cb.FileList {
			if f == fileName {
				allowed = true
				break
			}
		}
		if !allowed {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		localPath := filepath.Join(global.Config.Gin.ResourcesPath, "public", "upload", "client_build",
			strconv.Itoa(int(cb.Id)), fileName)
		if _, err := os.Stat(localPath); err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		c.File(localPath)
	}
}
