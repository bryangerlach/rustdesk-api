package api

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/lejianwen/rustdesk-api/v2/global"
	"github.com/lejianwen/rustdesk-api/v2/model"
	"github.com/sirupsen/logrus"
)

func TestCandidateTargets(t *testing.T) {
	cases := []struct {
		os       string
		arch     string
		contains []string
	}{
		// rdgen OS-level target must be a candidate for every arch
		{"windows", "x86_64", []string{"windows", "windows-x86_64-exe"}},
		{"windows", "amd64", []string{"windows", "windows-amd64-exe"}},
		{"windows", "386", []string{"windows-x86"}},
		{"linux", "amd64", []string{"linux", "linux-x86_64-deb"}},
		{"linux", "x86_64", []string{"linux", "linux-x86_64-deb"}},
		{"linux", "arm64", []string{"linux"}},
		{"linux", "aarch64", []string{"linux"}},
		{"macos", "arm64", []string{"macos", "macos-arm64-dmg"}},
		{"macos", "x86_64", []string{"macos", "macos-x86_64-dmg"}},
		{"android", "arm64-v8a", []string{"android", "android-arm64-v8a"}},
		{"android", "armeabi-v7a", []string{"android", "android-armeabi-v7a"}},
		// older rdgen builds used these targets
		{"android", "arm64", []string{"android-arm64"}},
	}
	for _, c := range cases {
		got := candidateTargets(c.os, c.arch)
		for _, want := range c.contains {
			found := false
			for _, g := range got {
				if g == want {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("candidateTargets(%q,%q) = %v, missing %q", c.os, c.arch, got, want)
			}
		}
	}

	if got := candidateTargets("plan9", "mips"); got != nil {
		t.Errorf("expected nil for unsupported platform, got %v", got)
	}
}

func TestSelectUpdateFile(t *testing.T) {
	windowsFiles := []string{"rustdesk-normal-1_5_0.exe", "rustdesk-normal-1_5_0.msi"}
	linuxFiles := []string{
		"rustdesk-normal-1_5_0-aarch64.deb",
		"rustdesk-normal-1_5_0-x86_64.deb",
		"rustdesk-normal-1_5_0-aarch64.AppImage",
	}
	androidFiles := []string{
		"rustdesk-normal-1_5_0-armv7.apk",
		"rustdesk-normal-1_5_0-aarch64.apk",
	}
	macosFiles := []string{
		"rustdesk-normal-1_5_0-x86_64.dmg",
		"rustdesk-normal-1_5_0-arm64.dmg",
	}

	cases := []struct {
		name  string
		files []string
		os    string
		arch  string
		want  string
	}{
		{"windows prefers exe", windowsFiles, "windows", "x86_64", "rustdesk-normal-1_5_0.exe"},
		{"linux amd64", linuxFiles, "linux", "amd64", "rustdesk-normal-1_5_0-x86_64.deb"},
		{"linux arm64", linuxFiles, "linux", "arm64", "rustdesk-normal-1_5_0-aarch64.deb"},
		{"android arm64-v8a", androidFiles, "android", "arm64-v8a", "rustdesk-normal-1_5_0-aarch64.apk"},
		{"android armeabi-v7a", androidFiles, "android", "armeabi-v7a", "rustdesk-normal-1_5_0-armv7.apk"},
		{"macos arm64", macosFiles, "macos", "arm64", "rustdesk-normal-1_5_0-arm64.dmg"},
		{"macos intel", macosFiles, "macos", "x86_64", "rustdesk-normal-1_5_0-x86_64.dmg"},
	}
	for _, c := range cases {
		if got := selectUpdateFile(c.files, c.os, c.arch); got != c.want {
			t.Errorf("%s: selectUpdateFile() = %q, want %q", c.name, got, c.want)
		}
	}

	if got := selectUpdateFile(nil, "windows", "amd64"); got != "" {
		t.Errorf("expected empty string for empty list, got %q", got)
	}
}

func TestCandidateTargetsStableOrder(t *testing.T) {
	// dvforge-specific targets should be preferred (first) over OS-level ones.
	got := candidateTargets("windows", "x86_64")
	want := []string{"windows-x86_64-exe", "windows-amd64-exe", "windows"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("candidateTargets = %v, want %v", got, want)
	}
}

func TestVersionCheckRequestDeviceID(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"string", `{"device_id":"abc123"}`, "abc123"},
		{"array", `{"device_id":["abc123"]}`, "abc123"},
		{"empty array", `{"device_id":[]}`, ""},
		{"null", `{"device_id":null}`, ""},
		{"full body array", `{"os":"windows","arch":"x86_64","device_id":["d1","d2"],"typ":"stable"}`, "d1"},
	}
	for _, c := range cases {
		var req VersionCheckRequest
		if err := json.Unmarshal([]byte(c.body), &req); err != nil {
			t.Errorf("%s: unexpected error: %v", c.name, err)
			continue
		}
		if got := req.DeviceId.String(); got != c.want {
			t.Errorf("%s: device_id = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestReleaseIDRoundTrip(t *testing.T) {
	cb := &model.ClientBuild{}
	cb.Id = 57
	cb.Version = "1.5.1"

	want := "1.5.1+057"
	if got := appVersionFor(cb); got != want {
		t.Errorf("appVersionFor = %q, want %q", got, want)
	}
	if got := releaseIDFor(cb); got != "v"+want {
		t.Errorf("releaseIDFor = %q, want %q", got, "v"+want)
	}
	version, id, ok := parseReleaseID("v1.5.1+057")
	if !ok || version != "1.5.1" || id != 57 {
		t.Errorf("parseReleaseID = (%q,%d,%v), want (1.5.1,57,true)", version, id, ok)
	}
	if _, _, ok := parseReleaseID("v1.5.1"); ok {
		t.Error("parseReleaseID accepted a release id without build metadata")
	}
}

func TestInferArtifact(t *testing.T) {
	cases := []struct {
		target   string
		file     string
		platform string
		arch     string
		format   string
		ok       bool
	}{
		{"windows", "rustdesk-normal-1_5_1.exe", "windows", "x86_64", "exe", true},
		{"windows", "rustdesk-normal-1_5_1.msi", "windows", "x86_64", "msi", true},
		{"windows-x86", "rustdesk-normal-1_5_1.exe", "windows", "x86", "exe", true},
		{"windows-x86_64-exe", "rustdesk.exe", "windows", "x86_64", "exe", true},
		{"macos", "rustdesk-normal-1_5_1-aarch64.dmg", "macos", "aarch64", "dmg", true},
		{"macos", "rustdesk-normal-1_5_1-x86_64.dmg", "macos", "x86_64", "dmg", true},
		{"linux", "rustdesk-normal-1_5_1-x86_64.deb", "linux", "x86_64", "unknown", true},
		{"linux", "rustdesk-normal-1_5_1-aarch64.AppImage", "linux", "aarch64", "unknown", true},
		{"linux", "rustdesk-normal-1_5_1-x86_64.pkg.tar.zst", "linux", "x86_64", "unknown", true},
		{"android", "rustdesk-normal-1_5_1-aarch64.apk", "android", "aarch64", "unknown", true},
		{"android", "rustdesk-normal-1_5_1-armv7.apk", "android", "armv7", "unknown", true},
		{"android", "rustdesk-normal-1_5_1-x86_64.apk", "android", "x86_64", "unknown", true},
		{"linux", "unrelated.txt", "", "", "", false},
	}
	for _, c := range cases {
		platform, arch, format, _, ok := inferArtifact(c.target, c.file)
		if ok != c.ok || platform != c.platform || arch != c.arch || format != c.format {
			t.Errorf("inferArtifact(%q,%q) = (%q,%q,%q,%v), want (%q,%q,%q,%v)",
				c.target, c.file, platform, arch, format, ok, c.platform, c.arch, c.format, c.ok)
		}
	}
}

func TestUpdateMetadataSignature(t *testing.T) {
	tmp := t.TempDir()
	resources := filepath.Join(tmp, "resources")
	buildDir := filepath.Join(resources, "public", "upload", "client_build", "57")
	if err := os.MkdirAll(buildDir, 0755); err != nil {
		t.Fatal(err)
	}
	payload := []byte("fake rustdesk artifact payload")
	fileName := "rustdesk-normal-1_5_1.exe"
	if err := os.WriteFile(filepath.Join(buildDir, fileName), payload, 0644); err != nil {
		t.Fatal(err)
	}

	oldConfig := global.Config
	oldLogger := global.Logger
	defer func() {
		global.Config = oldConfig
		global.Logger = oldLogger
	}()
	global.Logger = logrus.New()
	global.Config.Gin.ResourcesPath = resources
	global.Config.Rustdesk.ApiServer = "https://updates.example.com"
	global.Config.Rustdesk.UpdateKeyFile = filepath.Join(tmp, "update_signing_key.pem")

	cb := &model.ClientBuild{}
	cb.Id = 57
	cb.Version = "1.5.1"
	cb.Target = "windows"
	cb.FileList = []string{fileName}
	cb.EncodeFiles()

	metaBytes, err := buildUpdateMetadataBytes(cb)
	if err != nil {
		t.Fatalf("buildUpdateMetadataBytes: %v", err)
	}

	var meta updateMetadata
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		t.Fatalf("metadata JSON: %v", err)
	}
	if meta.SchemaVersion != 1 || meta.App != "rustdesk" || meta.PackageID != "rustdesk" {
		t.Errorf("unexpected metadata header: %+v", meta)
	}
	if meta.Version != "1.5.1+057" {
		t.Errorf("version = %q, want 1.5.1+057", meta.Version)
	}
	if meta.ReleaseID != "v1.5.1+057" {
		t.Errorf("release_id = %q, want v1.5.1+057", meta.ReleaseID)
	}
	if meta.SignatureKeyID != "custom-update" {
		t.Errorf("signature_key_id = %q, want custom-update", meta.SignatureKeyID)
	}
	if len(meta.Artifacts) != 1 {
		t.Fatalf("artifacts = %d, want 1", len(meta.Artifacts))
	}
	art := meta.Artifacts[0]
	wantURL := "https://updates.example.com/rustdesk/rustdesk/releases/download/v1.5.1+057/" + fileName
	if art.Platform != "windows" || art.Arch != "x86_64" || art.Format != "exe" {
		t.Errorf("artifact query = %s/%s/%s, want windows/x86_64/exe", art.Platform, art.Arch, art.Format)
	}
	if art.URL != wantURL {
		t.Errorf("artifact url = %q, want %q", art.URL, wantURL)
	}
	if art.Size != int64(len(payload)) {
		t.Errorf("artifact size = %d, want %d", art.Size, len(payload))
	}

	sigBytes, err := signUpdateMetadataBytes(metaBytes)
	if err != nil {
		t.Fatalf("signUpdateMetadataBytes: %v", err)
	}
	var env updateSignature
	if err := json.Unmarshal(sigBytes, &env); err != nil {
		t.Fatalf("signature JSON: %v", err)
	}
	if env.SchemaVersion != 1 || env.Algorithm != "ed25519" || env.KeyID != "custom-update" {
		t.Errorf("unexpected signature envelope: %+v", env)
	}
	rawSig, err := base64.StdEncoding.DecodeString(env.Signature)
	if err != nil {
		t.Fatalf("signature base64: %v", err)
	}

	key, err := loadUpdateSigningKey()
	if err != nil {
		t.Fatalf("loadUpdateSigningKey: %v", err)
	}
	signed := append([]byte(updateMetadataContext), metaBytes...)
	if !ed25519.Verify(key.Public().(ed25519.PublicKey), signed, rawSig) {
		t.Error("signature does not verify over the context-prefixed metadata")
	}
}



