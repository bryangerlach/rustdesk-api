package admin

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/lejianwen/rustdesk-api/v2/global"
	"github.com/lejianwen/rustdesk-api/v2/http/controller/api"
	"github.com/sirupsen/logrus"
)

// TestCreateBuildJSONHasUpdateKeyAndAppVersion verifies the exact JSON that
// ClientBuild.Create hands to a builder contains the self-hosted update
// settings and the per-build app_version.
func TestCreateBuildJSONHasUpdateKeyAndAppVersion(t *testing.T) {
	tmp := t.TempDir()
	oldConfig := global.Config
	oldLogger := global.Logger
	defer func() {
		global.Config = oldConfig
		global.Logger = oldLogger
	}()
	global.Logger = logrus.New()
	global.Config.Rustdesk.UpdateKeyFile = filepath.Join(tmp, "update_signing_key.pem")
	global.Config.Rustdesk.ApiServer = "https://updates.example.com"

	input := []byte(`{"serverIP":"example.com","custom":"value"}`)

	withKey, err := api.InjectUpdateConfig(input, global.Config.Rustdesk.ApiServer)
	if err != nil {
		t.Fatalf("InjectUpdateConfig: %v", err)
	}
	out := injectAppVersion(withKey, "1.5.1", 60)

	var m map[string]interface{}
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	wantKey, err := api.GetUpdatePublicKeyBase64()
	if err != nil {
		t.Fatalf("GetUpdatePublicKeyBase64: %v", err)
	}
	if m["update_public_key"] != wantKey {
		t.Errorf("update_public_key = %v, want %q", m["update_public_key"], wantKey)
	}
	if m["app_version"] != "1.5.1+060" {
		t.Errorf("app_version = %v, want 1.5.1+060", m["app_version"])
	}
	if m["downloadLink"] != "https://updates.example.com" {
		t.Errorf("downloadLink = %v, want https://updates.example.com", m["downloadLink"])
	}
	if m["update_url"] != "https://updates.example.com/api/version/latest" {
		t.Errorf("update_url = %v, want https://updates.example.com/api/version/latest", m["update_url"])
	}
	if m["removeNewVersionNotif"] != false {
		t.Errorf("removeNewVersionNotif = %v, want false", m["removeNewVersionNotif"])
	}
	if m["serverIP"] != "example.com" {
		t.Errorf("existing config was lost: %v", m)
	}
}
