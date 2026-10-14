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

	"github.com/lejianwen/rustdesk-api/v2/global"
	"github.com/lejianwen/rustdesk-api/v2/model"
	"gorm.io/gorm"
)

const (
	DefaultClientBuilderAPIBase = ""
	DefaultClientBuilderAPIToken  = ""
	ClientBuilderStatusDone      = "done"
)

const (
	SettingKeyClientBuilderAPIBase  = "client_builder_api_base"
	SettingKeyClientBuilderAPIToken = "client_builder_api_token"
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
func (s *ClientBuildService) StartBuild(cb *model.ClientBuild, jsonData []byte) error {
	target := cb.Target
	if target == "" {
		return fmt.Errorf("target required")
	}
	url := fmt.Sprintf("%s/job?targets=%s", clientBuilderAPIBase(), httpQueryEscape(target))
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(jsonData))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+clientBuilderAPIToken())
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
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

// RefreshStatus polls external job status and downloads files when done.
func (s *ClientBuildService) RefreshStatus(cb *model.ClientBuild) error {
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
	resp, err := http.DefaultClient.Do(req)
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
		if err := s.DownloadFiles(cb, files); err != nil {
			cb.Error = err.Error()
		}
	}
	return s.Update(cb)
}

func (s *ClientBuildService) DownloadFiles(cb *model.ClientBuild, files []string) error {
	dir := clientBuildDir(cb)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	for _, f := range files {
		dest := filepath.Join(dir, f)
		if _, err := os.Stat(dest); err == nil {
			continue
		}
		url := fmt.Sprintf("%s/dl/%s/%s", clientBuilderAPIBase(), cb.JobId, httpPathEscape(f))
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req)
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