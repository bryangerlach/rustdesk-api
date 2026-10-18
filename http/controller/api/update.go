package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/lejianwen/rustdesk-api/v2/global"
	"github.com/lejianwen/rustdesk-api/v2/http/response"
	"github.com/lejianwen/rustdesk-api/v2/model"
)

// VersionCheckRequest mirrors the RustDesk client's update check body.
// POST /version/latest
type VersionCheckRequest struct {
	Os        string `json:"os"`
	OsVersion string `json:"os_version"`
	Arch      string `json:"arch"`
	DeviceId  string `json:"device_id"`
	Type      string `json:"typ"`
}

// VersionCheckResponse is returned to the client. The client appends
// /rustdesk-update.json to the returned Url to fetch signed metadata.
type VersionCheckResponse struct {
	Url    string `json:"url"`
	Size   int64  `json:"size,omitempty"`
	Sha256 string `json:"sha256,omitempty"`
}

type Update struct{}

var (
	updateSigningKey ed25519.PrivateKey
	updateKeyOnce    sync.Once
	updateKeyErr     error
)

// loadUpdateSigningKey loads the ed25519 private key used to sign update metadata.
// If RUSTDESK_API_RUSTDESK_UPDATE_KEY_FILE is not set, or the file does not exist,
// a new key is auto-generated at ./data/update_signing_key.pem and logged.
func loadUpdateSigningKey() (ed25519.PrivateKey, error) {
	updateKeyOnce.Do(func() {
		path := global.Config.Rustdesk.UpdateKeyFile
		if path == "" {
			path = filepath.Join("data", "update_signing_key.pem")
			global.Logger.Info("RUSTDESK_API_RUSTDESK_UPDATE_KEY_FILE not set, auto-generating update key at " + path)
		}

		if _, err := os.Stat(path); os.IsNotExist(err) {
			global.Logger.Info("Update key file not found, generating new ed25519 key at " + path)
			key, err := generateAndSaveEd25519Key(path)
			if err != nil {
				updateKeyErr = fmt.Errorf("generate update key: %w", err)
				return
			}
			pubKeyBase64 := base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey))
			global.Logger.Info("Update public key (add to TRUSTED_UPDATE_KEYS): " + pubKeyBase64)
			updateSigningKey = key
			return
		}

		data, err := os.ReadFile(path)
		if err != nil {
			updateKeyErr = fmt.Errorf("read update key file: %w", err)
			return
		}
		key, err := parseEd25519PrivateKey(data)
		if err != nil {
			updateKeyErr = fmt.Errorf("parse update key: %w", err)
			return
		}
		updateSigningKey = key
	})
	return updateSigningKey, updateKeyErr
}

// GetUpdatePublicKeyBase64 returns the base64-encoded ed25519 public key for the
// update signing key, initializing it if needed. Used by the client builder to
// embed the public key into generated client configs.
func GetUpdatePublicKeyBase64() (string, error) {
	key, err := loadUpdateSigningKey()
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey)), nil
}

func generateAndSaveEd25519Key(path string) (ed25519.PrivateKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}

	privBytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, err
	}
	privPem := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privBytes})

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, privPem, 0600); err != nil {
		return nil, err
	}
	_ = pub // ensure pub is used conceptually; key returned is priv
	return priv, nil
}

func parseEd25519PrivateKey(data []byte) (ed25519.PrivateKey, error) {
	// Try PEM block first
	block, _ := pem.Decode(data)
	if block != nil {
		switch block.Type {
		case "PRIVATE KEY", "ED25519 PRIVATE KEY", "ANY PRIVATE KEY":
			parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
			if err != nil {
				return nil, err
			}
			k, ok := parsed.(ed25519.PrivateKey)
			if !ok {
				return nil, errors.New("not an ed25519 private key")
			}
			return k, nil
		}
	}

	// Try raw 32-byte seed
	trimmed := strings.TrimSpace(string(data))
	if b, err := hex.DecodeString(trimmed); err == nil && len(b) == 32 {
		return ed25519.NewKeyFromSeed(b), nil
	}

	// Try raw binary 32-byte seed
	if len(data) == 32 {
		return ed25519.NewKeyFromSeed(data), nil
	}

	return nil, errors.New("unsupported private key format (use PEM PKCS8, raw 32-byte seed, or hex seed)")
}

// Latest handles the client's software-update check.
// @Tags API
// @Summary Software update check
// @Description Return the download url for the latest built client matching os/arch
// @Accept  json
// @Produce  json
// @Param body body VersionCheckRequest true "version check request"
// @Success 200 {object} VersionCheckResponse
// @Router /version/latest [post]
func (u *Update) Latest(c *gin.Context) {
	var req VersionCheckRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 101, "ParamsError: "+err.Error())
		return
	}

	target := mapTarget(req.Os, req.Arch)
	if target == "" {
		response.Fail(c, 101, "Unsupported platform")
		return
	}

	cb := &model.ClientBuild{}
	if err := global.DB.Where("status = ? AND target = ?", "done", target).
		Order("created_at desc").First(cb).Error; err != nil {
		response.Fail(c, 101, "No update available")
		return
	}

	cb.DecodeFiles()

	if len(cb.FileList) == 0 {
		response.Fail(c, 101, "No files available")
		return
	}

	idStr := strconv.Itoa(int(cb.Id))
	file := selectUpdateFile(cb.FileList, req.Os, req.Arch)
	downloadURL := global.Config.Rustdesk.ApiServer + "/upload/client_build/" + idStr + "/" + file

	// Compute size and sha256 of the artifact.
	localPath := filepath.Join(global.Config.Gin.ResourcesPath, "public", "upload", "client_build", idStr, file)
	var size int64
	var fileHash string
	if f, err := os.Open(localPath); err == nil {
		defer f.Close()
		if info, err := f.Stat(); err == nil {
			size = info.Size()
		}
		h := sha256.New()
		if _, err := io.Copy(h, f); err == nil {
			fileHash = hex.EncodeToString(h.Sum(nil))
		}
	}

	// Build metadata JSON.
	metadata := map[string]interface{}{
		"version": cb.Version,
		"url":     downloadURL,
		"size":    size,
		"sha256":  fileHash,
		"note":    "Auto-generated by rustdesk-api",
	}
	metadataBytes, err := json.Marshal(metadata)
	if err != nil {
		response.Fail(c, 101, "Failed to build metadata: "+err.Error())
		return
	}

	// Sign metadata with ed25519 private key.
	key, err := loadUpdateSigningKey()
	if err != nil {
		response.Fail(c, 101, "Update signing not configured: "+err.Error())
		return
	}
	sig := ed25519.Sign(key, metadataBytes)

	// Write rustdesk-update.json and .sig next to the artifact.
	dir := filepath.Join(global.Config.Gin.ResourcesPath, "public", "upload", "client_build", idStr)
	if err := os.MkdirAll(dir, 0755); err == nil {
		_ = os.WriteFile(filepath.Join(dir, "rustdesk-update.json"), metadataBytes, 0644)
		_ = os.WriteFile(filepath.Join(dir, "rustdesk-update.json.sig"), sig, 0644)
	}

	// Return base directory URL; client appends /rustdesk-update.json
	baseURL := global.Config.Rustdesk.ApiServer + "/upload/client_build/" + idStr

	response.Success(c, VersionCheckResponse{
		Url:    baseURL,
		Size:   size,
		Sha256: fileHash,
	})
}

// mapTarget converts the client-reported os/arch into a ClientBuild target value
// that matches the dropdown options used by the client builder.
func mapTarget(os, arch string) string {
	switch os {
	case "windows":
		return "windows-" + arch + "-exe"
	case "linux":
		switch arch {
		case "amd64", "x86_64":
			return "linux-x86_64-deb"
		}
	case "macos":
		if arch == "arm64" {
			return "macos-arm64-dmg"
		}
		return "macos-x86_64-dmg"
	case "android":
		return "android-" + arch
	}
	return ""
}

// archPatterns returns filename substrings that match the client's reported arch.
// rdgen builds multiple arch variants for a single OS target, so we need to
// match the actual file to the client's architecture.
func archPatterns(arch string) []string {
	switch arch {
	case "amd64", "x86_64":
		return []string{"x86_64", "amd64"}
	case "arm64", "aarch64":
		return []string{"aarch64", "arm64"}
	case "armv7", "armhf":
		return []string{"armv7", "armhf"}
	case "386", "i386":
		return []string{"i386", "386", "x86"}
	default:
		return []string{arch}
	}
}

// selectUpdateFile picks the file from FileList that best matches the client's
// requested os/arch. rdgen builds all arch variants for an OS target, so
// FileList[0] may be aarch64 when the client asked for x86_64 (or vice versa).
// Windows files don't include arch in the name, so .exe is preferred over .msi.
func selectUpdateFile(files []string, os, arch string) string {
	if len(files) == 0 {
		return ""
	}

	// First pass: find a file containing the arch pattern
	for _, pattern := range archPatterns(arch) {
		for _, f := range files {
			if strings.Contains(strings.ToLower(f), strings.ToLower(pattern)) {
				return f
			}
		}
	}

	// For Windows, arch isn't in the filename; prefer .exe over .msi
	if os == "windows" {
		for _, f := range files {
			if strings.HasSuffix(strings.ToLower(f), ".exe") {
				return f
			}
		}
	}

	// Fallback to first file
	return files[0]
}

// InjectUpdatePublicKey adds the update signing public key to a client builder
// JSON config so the built client can verify update metadata.
func InjectUpdatePublicKey(data []byte) ([]byte, error) {
	pubKey, err := GetUpdatePublicKeyBase64()
	if err != nil || pubKey == "" {
		return data, nil
	}
	var jsonMap map[string]interface{}
	if err := json.Unmarshal(data, &jsonMap); err != nil {
		return data, nil
	}
	jsonMap["update_public_key"] = pubKey
	return json.Marshal(jsonMap)
}
