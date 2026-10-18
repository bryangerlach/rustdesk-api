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
	"time"

	"github.com/lejianwen/rustdesk-api/v2/global"
	"github.com/lejianwen/rustdesk-api/v2/model"
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

type ClientBuildService struct{}

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
	status, _ := result["status"].(string)
	switch status {
	case "completed", "success":
		cb.Status = "done"
	case "in_progress":
		cb.Status = "running"
	default: // failure, failed, error, cancelled, timed_out
		cb.Status = "error"
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