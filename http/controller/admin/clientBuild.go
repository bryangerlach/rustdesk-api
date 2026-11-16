package admin

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lejianwen/rustdesk-api/v2/global"
	"github.com/lejianwen/rustdesk-api/v2/http/controller/api"
	"github.com/lejianwen/rustdesk-api/v2/http/request/admin"
	"github.com/lejianwen/rustdesk-api/v2/http/response"
	"github.com/lejianwen/rustdesk-api/v2/model"
	"github.com/lejianwen/rustdesk-api/v2/service"
	"gorm.io/gorm"
)

type ClientBuild struct {
}

func cbSvc() *service.ClientBuildService {
	return &service.ClientBuildService{}
}

// List 列表
// @Tags 客户端构建
// @Summary 客户端构建列表
// @Router /admin/client_build/list [get]
func (ct *ClientBuild) List(c *gin.Context) {
	query := &admin.ClientBuildQuery{}
	if err := c.ShouldBindQuery(query); err != nil {
		response.Fail(c, 101, response.TranslateMsg(c, "ParamsError")+err.Error())
		return
	}
	res := cbSvc().List(query.Page, query.PageSize, func(tx *gorm.DB) {
		if query.UserId > 0 {
			tx.Where("user_id = ?", query.UserId)
		}
	})
	response.Success(c, res)
}

// Create 创建并启动构建，支持一次创建多个target
// @Tags 客户端构建
// @Summary 创建客户端构建（支持多target）
// @Router /admin/client_build/create [post]
func (ct *ClientBuild) Create(c *gin.Context) {
	targets := c.PostFormArray("targets")
	builder := c.PostForm("builder")
	version := c.PostForm("version")
	name := c.PostForm("name")
	excludeFromUpdate, _ := strconv.ParseBool(c.PostForm("exclude_from_update"))
	if builder == "" {
		builder = "dvforge"
	}
	if len(targets) == 0 {
		response.Fail(c, 101, response.TranslateMsg(c, "ParamsError"))
		return
	}

	file, err := c.FormFile("file")
	if err != nil {
		response.Fail(c, 101, response.TranslateMsg(c, "ParamsError")+err.Error())
		return
	}
	f, err := file.Open()
	if err != nil {
		response.Fail(c, 101, response.TranslateMsg(c, "ParamsError")+err.Error())
		return
	}
	data, err := io.ReadAll(f)
	f.Close()
	if err != nil {
		response.Fail(c, 101, response.TranslateMsg(c, "ParamsError")+err.Error())
		return
	}
	if name == "" {
		name = file.Filename
	}

	// Embed the self-hosted update settings into the uploaded builder JSON so
	// the built client can verify update metadata. The API server address is
	// injected as both `downloadLink` (rdgen's update base) and `update_url`
	// (DVForge's explicit update check URL). The signing key is generated on
	// first use when the configured file does not exist yet.
	if builder == "dvforge" || builder == "rdgen" {
		if injected, injErr := api.InjectUpdateConfig(data, global.Config.Rustdesk.ApiServer); injErr != nil {
			global.Logger.WithError(injErr).Warn("client build: update config injection failed")
		} else {
			data = injected
		}
	}

	// 为每个target创建独立的构建记录并启动构建
	var createdBuilds []*model.ClientBuild
	var firstErr error
	for _, target := range targets {
		cb := &model.ClientBuild{
			Builder:           builder,
			Target:            target,
			Version:           version,
			Name:              name + " - " + target,
			Status:            "pending",
			ExcludeFromUpdate: excludeFromUpdate,
			JsonFile:          file.Filename,
			Files:             "[]",
			FileList:          []string{},
		}
		if err := cbSvc().Create(cb); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		// Both builders embed app_version: <version>+<id padded to 3 digits>,
		// the semver build metadata format required by Cargo.toml (e.g.
		// 1.5.1+016). The client compares it with the release id returned by
		// /version/latest so each build is a distinct, ordered update.
		buildData := data
		if version != "" {
			buildData = injectAppVersion(buildData, version, cb.Id)
		}

		if err := cbSvc().StartBuild(cb, buildData); err != nil {
			cb.Status = "error"
			cb.Error = err.Error()
			_ = cbSvc().Update(cb)
			if firstErr == nil {
				firstErr = err
			}
			createdBuilds = append(createdBuilds, cb)
			continue
		}
		cb = cbSvc().InfoById(cb.Id)
		createdBuilds = append(createdBuilds, cb)
	}

	if firstErr != nil && len(createdBuilds) == 0 {
		response.Fail(c, 101, response.TranslateMsg(c, "OperationFailed")+firstErr.Error())
		return
	}
	response.Success(c, createdBuilds)
}

// injectAppVersion adds app_version = <version>+<id padded to 3 digits> to the
// builder JSON. Both builders write this into Cargo.toml so the built client's
// crate::VERSION carries the build number used for self-hosted updates.
func injectAppVersion(data []byte, version string, id uint) []byte {
	var jsonMap map[string]interface{}
	if err := json.Unmarshal(data, &jsonMap); err != nil {
		return data
	}
	jsonMap["app_version"] = fmt.Sprintf("%s+%03d", version, id)
	modified, err := json.Marshal(jsonMap)
	if err != nil {
		return data
	}
	return modified
}

// dvforgeTargets mirrors the target IDs in DVForge's builder/detect.py.
// Keep this list in sync with DVForge; the farm queue passes the ID through
// unchanged and DVForge rejects unknown IDs before starting a build.
var dvforgeTargets = []string{
	"windows-x86_64-exe",
	"windows-x86_64-msi",
	"linux-x86_64-deb",
	"linux-x86_64-rpm",
	"linux-x86_64-appimage",
	"linux-aarch64-deb",
	"macos-arm64-dmg",
	"macos-x86_64-dmg",
	"macos-universal-dmg",
	"android-arm64",
	"android-armv7",
	"android-x86_64",
	"android-universal",
}

// TargetOptions 根据builder返回可用的target选项
// @Tags 客户端构建
// @Summary 获取构建目标选项
// @Router /admin/client_build/target_options [get]
func (ct *ClientBuild) TargetOptions(c *gin.Context) {
	builder := c.Query("builder")
	if builder == "" {
		builder = "dvforge"
	}

	var targets []string
	switch builder {
	case "rdgen":
		// rdgen按OS构建所有架构，所以target是OS级别
		targets = []string{"windows", "windows-x86", "linux", "android", "macos"}
	case "dvforge":
		// These IDs must match builder/detect.py TARGETS in DVForge. The farm
		// queue forwards them verbatim to POST /api/build/start, which rejects
		// anything else with "Select a non-empty list of known targets".
		targets = dvforgeTargets
	default:
		targets = []string{}
	}

	response.Success(c, gin.H{"targets": targets})
}

// Versions 获取官方RustDesk最新发布版本
// @Tags 客户端构建
// @Summary 获取客户端构建可用的版本选项
// @Router /admin/client_build/versions [get]
func (ct *ClientBuild) Versions(c *gin.Context) {
	versions, err := cbSvc().Versions()
	if err != nil {
		response.Fail(c, 101, response.TranslateMsg(c, "OperationFailed")+err.Error())
		return
	}
	response.Success(c, gin.H{"versions": versions})
}

// RefreshStatus 刷新构建状态
// @Tags 客户端构建
// @Summary 刷新客户端构建状态
// @Router /admin/client_build/refresh_status [post]
func (ct *ClientBuild) RefreshStatus(c *gin.Context) {
	f := &admin.ClientBuildForm{}
	if err := c.ShouldBindJSON(f); err != nil {
		response.Fail(c, 101, response.TranslateMsg(c, "ParamsError")+err.Error())
		return
	}
	cb := cbSvc().InfoById(f.Id)
	if cb.Id == 0 {
		response.Fail(c, 101, response.TranslateMsg(c, "ItemNotFound"))
		return
	}
	if err := cbSvc().RefreshStatus(cb); err != nil {
		response.Fail(c, 101, response.TranslateMsg(c, "OperationFailed")+err.Error())
		return
	}
	cb = cbSvc().InfoById(cb.Id)
	response.Success(c, cb)
}

// Update 更新构建配置（目前仅Auto Update）
// @Tags 客户端构建
// @Summary 修改构建是否参与自动更新
// @Router /admin/client_build/update [post]
func (ct *ClientBuild) Update(c *gin.Context) {
	f := &admin.ClientBuildUpdateForm{}
	if err := c.ShouldBindJSON(f); err != nil {
		response.Fail(c, 101, response.TranslateMsg(c, "ParamsError")+err.Error())
		return
	}
	if f.Id == 0 {
		response.Fail(c, 101, response.TranslateMsg(c, "ItemNotFound"))
		return
	}
	cb := cbSvc().InfoById(f.Id)
	if cb.Id == 0 {
		response.Fail(c, 101, response.TranslateMsg(c, "ItemNotFound"))
		return
	}
	cb.ExcludeFromUpdate = f.ExcludeFromUpdate
	if err := cbSvc().SetExcludeFromUpdate(cb.Id, f.ExcludeFromUpdate); err != nil {
		response.Fail(c, 101, response.TranslateMsg(c, "OperationFailed")+err.Error())
		return
	}
	response.Success(c, cb)
}

// Delete 删除
// @Tags 客户端构建
// @Summary 删除客户端构建
// @Router /admin/client_build/delete [post]
func (ct *ClientBuild) Delete(c *gin.Context) {
	f := &admin.ClientBuildForm{}
	if err := c.ShouldBindJSON(f); err != nil {
		response.Fail(c, 101, response.TranslateMsg(c, "ParamsError")+err.Error())
		return
	}
	id := f.Id
	errList := global.Validator.ValidVar(c, id, "required,gt=0")
	if len(errList) > 0 {
		response.Fail(c, 101, errList[0])
		return
	}
	cb := cbSvc().InfoById(f.Id)
	if cb.Id > 0 {
		if err := cbSvc().Delete(cb); err != nil {
			response.Fail(c, 101, response.TranslateMsg(c, "OperationFailed")+err.Error())
			return
		}
		response.Success(c, nil)
		return
	}
	response.Fail(c, 101, response.TranslateMsg(c, "ItemNotFound"))
}

// BatchDelete 批量删除
// @Tags 客户端构建
// @Summary 批量删除客户端构建
// @Router /admin/client_build/batchDelete [post]
func (ct *ClientBuild) BatchDelete(c *gin.Context) {
	f := &struct {
		Ids []uint `json:"ids" validate:"required"`
	}{}
	if err := c.ShouldBindJSON(f); err != nil {
		response.Fail(c, 101, response.TranslateMsg(c, "ParamsError"))
		return
	}
	if len(f.Ids) == 0 {
		response.Fail(c, 101, response.TranslateMsg(c, "ParamsError"))
		return
	}
	if err := cbSvc().BatchDelete(f.Ids); err != nil {
		response.Fail(c, 101, response.TranslateMsg(c, "OperationFailed")+err.Error())
		return
	}
	response.Success(c, nil)
}

// DownloadTicket returns a short-lived direct download URL for a build file.
// The browser can follow it without an api-token header, which lets the file
// stream straight to disk instead of being buffered as a JavaScript Blob.
// @Tags 客户端构建
// @Summary 获取客户端文件下载链接
// @Router /admin/client_build/download_ticket [post]
func (ct *ClientBuild) DownloadTicket(c *gin.Context) {
	f := &admin.ClientBuildDownloadForm{}
	if err := c.ShouldBindJSON(f); err != nil {
		response.Fail(c, 101, response.TranslateMsg(c, "ParamsError")+err.Error())
		return
	}
	cb := cbSvc().InfoById(f.Id)
	if cb.Id == 0 {
		response.Fail(c, 101, response.TranslateMsg(c, "ItemNotFound"))
		return
	}
	file := strings.TrimSpace(f.File)
	if file == "" || filepath.Base(file) != file {
		response.Fail(c, 101, response.TranslateMsg(c, "ItemNotFound"))
		return
	}
	allowed := false
	for _, name := range cb.FileList {
		if name == file {
			allowed = true
			break
		}
	}
	if !allowed {
		response.Fail(c, 101, response.TranslateMsg(c, "ItemNotFound"))
		return
	}
	token, err := service.CreateClientBuildDownloadToken(cb.Id, file, 0)
	if err != nil {
		response.Fail(c, 101, response.TranslateMsg(c, "OperationFailed")+err.Error())
		return
	}
	response.Success(c, gin.H{
		"url": fmt.Sprintf("/client_build/download/%d/%s?download_token=%s",
			cb.Id, url.PathEscape(file), url.QueryEscape(token)),
	})
}

// Download 下载已保存的客户端文件
// @Tags 客户端构建
// @Summary 下载客户端文件
// @Router /admin/client_build/download/:id/:file [get]
func (ct *ClientBuild) Download(c *gin.Context) {
	idStr := c.Param("id")
	fileName := c.Param("file")
	if fileName == "" || filepath.Base(fileName) != fileName {
		response.Fail(c, 101, response.TranslateMsg(c, "ItemNotFound"))
		return
	}
	iid, err := strconv.Atoi(idStr)
	if err != nil {
		response.Fail(c, 101, response.TranslateMsg(c, "ParamsError"))
		return
	}
	cb := cbSvc().InfoById(uint(iid))
	if cb.Id == 0 {
		response.Fail(c, 101, response.TranslateMsg(c, "ItemNotFound"))
		return
	}
	if cb.Status != "done" {
		// try refresh
		_ = cbSvc().RefreshStatus(cb)
		cb = cbSvc().InfoById(cb.Id)
	}
	allowed := false
	for _, name := range cb.FileList {
		if name == fileName {
			allowed = true
			break
		}
	}
	if !allowed {
		response.Fail(c, 101, response.TranslateMsg(c, "ItemNotFound"))
		return
	}
	localPath := filepath.Join(global.Config.Gin.ResourcesPath, "public", "upload", "client_build", strconv.Itoa(int(cb.Id)), fileName)
	if _, err := os.Stat(localPath); err != nil {
		// try download now
		_ = cbSvc().DownloadFiles(cb, []string{fileName})
	}
	if _, err := os.Stat(localPath); err != nil {
		response.Fail(c, 101, response.TranslateMsg(c, "ItemNotFound"))
		return
	}
	c.FileAttachment(localPath, fileName)
}
