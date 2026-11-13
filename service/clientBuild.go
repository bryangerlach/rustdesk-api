package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lejianwen/rustdesk-api/v2/global"
	"github.com/lejianwen/rustdesk-api/v2/model"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

const (
	DefaultClientBuilderAPIBase = ""
	DefaultClientBuilderAPIToken  = ""
	DefaultRdgenAPIBase          = ""
	ClientBuilderStatusDone      = "done"
)

const (
	SettingKeyClientBuilderAPIBase  = "client_builder_api_base"
	SettingKeyClientBuilderAPIToken = "client_builder_api_token"
	SettingKeyRdgenAPIBase         = "rdgen_api_base"
	SettingKeyRdgenAPIToken        = "rdgen_api_token"
)

// Deprecated: use clientBuilderAPIBase() instead.
var ClientBuilderAPIBase = DefaultClientBuilderAPIBase

// Deprecated: use clientBuilderAPIToken() instead.
var ClientBuilderAPIToken = DefaultClientBuilderAPIToken

// ClientBuilderBaseValue returns effective client builder API base (saved setting, config/env, default).
func ClientBuilderBaseValue() string { return clientBuilderAPIBase() }

// ClientBuilderTokenValue returns effective client builder API token (saved setting, config/env, default).
func ClientBuilderTokenValue() string { return clientBuilderAPIToken() }

func clientBuilderAPIBase() string {
	if v := (&SettingService{}).Get(SettingKeyClientBuilderAPIBase); v != "" {
		return v
	}
	if Config != nil && Config.ClientBuilder.APIBase != "" {
		return Config.ClientBuilder.APIBase
	}
	return DefaultClientBuilderAPIBase
}

func clientBuilderAPIToken() string {
	if v := (&SettingService{}).Get(SettingKeyClientBuilderAPIToken); v != "" {
		return v
	}
	if Config != nil && Config.ClientBuilder.APIToken != "" {
		return Config.ClientBuilder.APIToken
	}
	return DefaultClientBuilderAPIToken
}

// RdgenBaseValue returns effective rdgen API base (saved setting, config/env, default).
func RdgenBaseValue() string { return rdgenAPIBase() }

// RdgenTokenValue returns effective rdgen API token (saved setting, config/env, default).
func RdgenTokenValue() string { return rdgenAPIToken() }

func rdgenAPIBase() string {
	if v := (&SettingService{}).Get(SettingKeyRdgenAPIBase); v != "" {
		return v
	}
	if Config != nil && Config.Rdgen.APIBase != "" {
		return Config.Rdgen.APIBase
	}
	return DefaultRdgenAPIBase
}

func rdgenAPIToken() string {
	if v := (&SettingService{}).Get(SettingKeyRdgenAPIToken); v != "" {
		return v
	}
	if Config != nil && Config.Rdgen.APIToken != "" {
		return Config.Rdgen.APIToken
	}
	return ""
}

var buildHTTPClient = &http.Client{Timeout: 2 * time.Minute}

const (
	rustdeskReleasesURL  = "https://api.github.com/repos/rustdesk/rustdesk/releases"
	rustdeskVersionLimit = 3
	rustdeskVersionTTL   = 10 * time.Minute
)

var (
	releaseVersionsClient = &http.Client{Timeout: 15 * time.Second}
	releaseVersionsMu     sync.Mutex
	releaseVersionsCache  []string
	releaseVersionsAt     time.Time
)

type ClientBuildService struct{}

// Versions returns the most recent official RustDesk release versions, newest
// first. Results are cached briefly to stay well within GitHub's API limits.
func (s *ClientBuildService) Versions() ([]string, error) {
	releaseVersionsMu.Lock()
	defer releaseVersionsMu.Unlock()

	if len(releaseVersionsCache) > 0 && time.Since(releaseVersionsAt) < rustdeskVersionTTL {
		return releaseVersionsCache, nil
	}

	versions, err := fetchRustdeskReleaseVersions()
	if err != nil {
		// Fall back to stale data rather than breaking the build dialog when
		// GitHub is temporarily unreachable.
		if len(releaseVersionsCache) > 0 {
			return releaseVersionsCache, nil
		}
		return nil, err
	}
	releaseVersionsCache = versions
	releaseVersionsAt = time.Now()
	return versions, nil
}

func fetchRustdeskReleaseVersions() ([]string, error) {
	req, err := http.NewRequest(http.MethodGet, rustdeskReleasesURL+"?per_page=10", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "rustdesk-api")

	resp, err := releaseVersionsClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github releases returned status %d", resp.StatusCode)
	}

	var releases []rustdeskRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, err
	}
	return releaseVersionsFromJSON(releases, rustdeskVersionLimit), nil
}

type rustdeskRelease struct {
	TagName    string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

func releaseVersionsFromJSON(releases []rustdeskRelease, limit int) []string {
	versions := make([]string, 0, limit)
	for _, r := range releases {
		if r.Draft || r.Prerelease {
			continue
		}
		v := strings.TrimSpace(strings.TrimPrefix(r.TagName, "v"))
		if v == "" {
			continue
		}
		versions = append(versions, v)
		if len(versions) >= limit {
			break
		}
	}
	return versions
}

func clientBuildDir(cb *model.ClientBuild) string {
	return filepath.Join(global.Config.Gin.ResourcesPath, "public", "upload", "client_build", strconv.Itoa(int(cb.Id)))
}

func (s *ClientBuildService) InfoById(id uint) *model.ClientBuild {
	cb := &model.ClientBuild{}
	DB.First(cb, id)
	cb.DecodeFiles()
	return cb
}

func (s *ClientBuildService) List(page, pageSize uint, where func(tx *gorm.DB)) (res *model.ClientBuildList) {
	res = &model.ClientBuildList{}
	res.Page = int64(page)
	res.PageSize = int64(pageSize)
	tx := DB.Model(&model.ClientBuild{})
	if where != nil {
		where(tx)
	}
	tx.Count(&res.Total)
	tx = tx.Scopes(Paginate(page, pageSize)).Order("id desc")
	tx.Find(&res.ClientBuilds)
	for _, cb := range res.ClientBuilds {
		cb.DecodeFiles()
	}
	return
}

func (s *ClientBuildService) Create(cb *model.ClientBuild) error {
	cb.FileList = []string{}
	cb.EncodeFiles()
	if cb.Status == "" {
		cb.Status = "pending"
	}
	err := DB.Create(cb).Error
	if err != nil {
		return err
	}
	cb.DecodeFiles()
	return nil
}

func (s *ClientBuildService) Update(cb *model.ClientBuild) error {
	cb.EncodeFiles()
	return DB.Model(cb).Updates(cb).Error
}

func (s *ClientBuildService) Delete(cb *model.ClientBuild) error {
	dir := clientBuildDir(cb)
	_ = os.RemoveAll(dir)
	return DB.Delete(cb).Error
}

func (s *ClientBuildService) BatchDelete(ids []uint) error {
	for _, id := range ids {
		cb := s.InfoById(id)
		if cb.Id > 0 {
			_ = s.Delete(cb)
		}
	}
	return nil
}

// StartBuild calls external api to start a build, returns remote job id.
func (s *ClientBuildService) startDVForgeBuild(cb *model.ClientBuild, jsonData []byte) error {
	target := cb.Target
	if target == "" {
		return fmt.Errorf("target required")
	}
	version := ""
	if cb.Version != "" {
		version = cb.Version
	}
	endpoint := clientBuilderAPIBase() + "/job?targets=" + httpQueryEscape(target)
	if version != "" {
		endpoint += "&version=" + httpQueryEscape(version)
	}
	url := endpoint
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(jsonData))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+clientBuilderAPIToken())
	req.Header.Set("Content-Type", "application/json")
	resp, err := buildHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("start build failed: %s", strings.TrimSpace(string(body)))
	}
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return err
	}
	id, _ := result["id"].(string)
	if id == "" {
		// maybe number
		if v, ok := result["id"]; ok {
			id = fmt.Sprintf("%v", v)
		}
	}
	if id == "" {
		return fmt.Errorf("no job id returned")
	}
	cb.JobId = id
	if ok, _ := result["ok"].(bool); !ok {
		// still proceed if id exists
	}
	cb.Status = "running"
	return s.Update(cb)
}

func (s *ClientBuildService) startRdgenBuild(cb *model.ClientBuild, jsonData []byte) error {
	base := rdgenAPIBase()
	platform := "windows"
	if cb.Target != "" {
		parts := strings.SplitN(cb.Target, "-", 2)
		if parts[0] != "" {
			platform = parts[0]
		}
	}
	endpoint := base + "/api/generate?platform_override=" + url.QueryEscape(platform)
	if cb.Version != "" {
		endpoint += "&version=" + url.QueryEscape(cb.Version)
	}

	// Visibility for self-hosted update builds: confirm both keys made it into
	// the JSON that rdgen receives.
	global.Logger.WithFields(logrus.Fields{
		"build_id":              cb.Id,
		"target":                cb.Target,
		"version":               cb.Version,
		"has_update_public_key": strings.Contains(string(jsonData), `"update_public_key"`),
		"has_app_version":       strings.Contains(string(jsonData), `"app_version"`),
	}).Info("rdgen: starting build")

	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(jsonData))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if tok := rdgenAPIToken(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := buildHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("start build failed: %s", strings.TrimSpace(string(body)))
	}
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return err
	}
	ok, _ := result["success"].(bool)
	if !ok {
		if msg, _ := result["error"].(string); msg != "" {
			return fmt.Errorf("start build failed: %s", msg)
		}
		return fmt.Errorf("start build failed: %s", strings.TrimSpace(string(body)))
	}
	id, _ := result["uuid"].(string)
	if id == "" {
		return fmt.Errorf("no job id returned")
	}
	cb.Builder = "rdgen"
	cb.JobId = id
	remotePlatform, _ := result["platform"].(string)
	filename, _ := result["filename"].(string)
	if logUrl, _ := result["log_url"].(string); logUrl != "" {
		cb.LogUrl = logUrl
	}
	if remotePlatform != "" {
		platform = remotePlatform
	}
	if filename != "" {
		cb.StatusUrl = "/api/status?uuid=" + url.QueryEscape(id) + "&platform=" + url.QueryEscape(platform) + "&filename=" + url.QueryEscape(filename)
	}
	cb.Status = "running"
	return s.Update(cb)
}

func (s *ClientBuildService) builderOf(cb *model.ClientBuild) string {
	if cb.Builder == "" {
		return "dvforge"
	}
	return cb.Builder
}

// StartBuild calls external api to start a build, returns remote job id.
func (s *ClientBuildService) StartBuild(cb *model.ClientBuild, jsonData []byte) error {
	if cb.Builder == "" {
		cb.Builder = "dvforge"
	}
	if cb.Builder == "rdgen" {
		return s.startRdgenBuild(cb, jsonData)
	}
	return s.startDVForgeBuild(cb, jsonData)
}

// RefreshStatus polls external job status and downloads files when done.
func (s *ClientBuildService) RefreshStatus(cb *model.ClientBuild) error {
	if cb.JobId == "" {
		return nil
	}
	cb = s.InfoById(cb.Id)
	if cb.Id == 0 {
		return fmt.Errorf("not found")
	}
	if cb.Builder == "rdgen" {
		return s.refreshRdgenStatus(cb)
	}
	return s.refreshDVForgeStatus(cb)
}

func (s *ClientBuildService) refreshRdgenStatus(cb *model.ClientBuild) error {
	base := rdgenAPIBase()
	statusUrl := cb.StatusUrl
	if statusUrl == "" {
		statusUrl = "/api/status?uuid=" + url.QueryEscape(cb.JobId) + "&platform=windows&filename="
	}
	resp, err := buildHTTPClient.Get(base + statusUrl)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		cb.Status = "error"
		cb.Error = strings.TrimSpace(string(body))
		return s.Update(cb)
	}
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return err
	}
	if logUrl, _ := result["log_url"].(string); logUrl != "" {
		cb.LogUrl = logUrl
	}
	status, _ := result["status"].(string)
	switch strings.ToLower(status) {
	case "completed", "success":
		cb.Status = "done"
	case "in_progress", "queued", "waiting", "requested", "pending":
		cb.Status = "running"
	default:
		if strings.Contains(strings.ToLower(status), "starting") || strings.Contains(strings.ToLower(status), "progress") {
			cb.Status = "running"
		} else {
			cb.Status = "error"
		}
	}

	// Always capture whatever files rdgen reports, regardless of status,
	// so partial results (e.g. android armv7/aarch64/x86_64) are available.
	if filesRaw, ok := result["files"]; ok {
		files := []string{}
		switch v := filesRaw.(type) {
		case []interface{}:
			for _, f := range v {
				files = append(files, fmt.Sprintf("%v", f))
			}
		}
		if len(files) > 0 {
			if cb.Status == "error" {cb.Status = "done"}
			cb.FileList = files
			cb.EncodeFiles()
			if err := s.DownloadFiles(cb, files, "rdgen"); err != nil {
				cb.Error = err.Error()
			}
		}
	}
	return s.Update(cb)
}

func (s *ClientBuildService) refreshDVForgeStatus(cb *model.ClientBuild) error {
	if cb.JobId == "" {
		return nil
	}
	cb = s.InfoById(cb.Id)
	if cb.Id == 0 {
		return fmt.Errorf("not found")
	}
	url := fmt.Sprintf("%s/job/%s", clientBuilderAPIBase(), cb.JobId)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := buildHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		cb.Status = "error"
		cb.Error = strings.TrimSpace(string(body))
		return s.Update(cb)
	}
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return err
	}
	state, _ := result["state"].(string)
	if state != "" {
		cb.Status = state
	}
	if state == ClientBuilderStatusDone {
		// files
		files := []string{}
		if raw, ok := result["files"]; ok {
			switch v := raw.(type) {
			case []interface{}:
				for _, f := range v {
					files = append(files, fmt.Sprintf("%v", f))
				}
			}
		}
		cb.FileList = files
		cb.EncodeFiles()
		// download files
		if err := s.DownloadFiles(cb, files, "dvforge"); err != nil {
			cb.Error = err.Error()
		}
	}
	return s.Update(cb)
}

func (s *ClientBuildService) DownloadFiles(cb *model.ClientBuild, files []string, provider ...string) error {
	dir := clientBuildDir(cb)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	prov := cb.Builder
	if len(provider) > 0 && provider[0] != "" {
		prov = provider[0]
	}
	if prov == "" {
		prov = "dvforge"
	}
	for _, f := range files {
		dest := filepath.Join(dir, f)
		if _, err := os.Stat(dest); err == nil {
			continue
		}
		var url string
		if prov == "rdgen" {
			url = fmt.Sprintf("%s/download?uuid=%s&filename=%s", rdgenAPIBase(), cb.JobId, httpQueryEscape(f))
		} else {
			url = fmt.Sprintf("%s/dl/%s/%s", clientBuilderAPIBase(), cb.JobId, httpPathEscape(f))
		}
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := buildHTTPClient.Do(req)
		if err != nil {
			return err
		}
		func() {
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				b, _ := io.ReadAll(resp.Body)
				cb.Error = fmt.Sprintf("download %s failed: %s", f, strings.TrimSpace(string(b)))
				return
			}
			out, err := os.Create(dest)
			if err != nil {
				cb.Error = err.Error()
				return
			}
			defer out.Close()
			_, _ = io.Copy(out, resp.Body)
		}()
	}
	return nil
}

func httpQueryEscape(s string) string {
	return url.QueryEscape(s)
}

func httpPathEscape(s string) string {
	return url.PathEscape(s)
}
