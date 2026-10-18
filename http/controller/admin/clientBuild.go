package admin

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

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

	// Embed the update signing public key into the uploaded builder JSON so the
	// built client can verify update metadata.
	if builder == "dvforge" || builder == "rdgen" {
		if injected, injErr := api.InjectUpdatePublicKey(data); injErr == nil {
			data = injected
		}
	}

	// 为每个target创建独立的构建记录并启动构建
	var createdBuilds []*model.ClientBuild
	var firstErr error
	for _, target := range targets {
		cb := &model.ClientBuild{
			Builder:  builder,
			Target:   target,
			Version:  version,
			Name:     name + " - " + target,
			Status:   "pending",
			JsonFile: file.Filename,
			Files:    "[]",
			FileList: []string{},
		}
		if err := cbSvc().Create(cb); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		// 为 rdgen 构建注入 app_version: <version>.<id padded to 3 digits>
		buildData := data
		if builder == "rdgen" {
			var jsonMap map[string]interface{}
			if jsonErr := json.Unmarshal(buildData, &jsonMap); jsonErr == nil {
				jsonMap["app_version"] = version + "." + fmt.Sprintf("%03d", cb.Id)
				if modified, mErr := json.Marshal(jsonMap); mErr == nil {
					buildData = modified
				}
			}
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
		// dvforge使用具体的target字符串
		targets = []string{
			"windows-386-exe",
			"windows-amd64-exe",
			"windows-arm64-exe",
			"linux-x86_64-deb",
			"linux-arm64-deb",
			"macos-x86_64-dmg",
			"macos-arm64-dmg",
			"android-arm64-v8a",
			"android-armeabi-v7a",
			"android-x86_64",
		}
	default:
		targets = []string{}
	}

	response.Success(c, gin.H{"targets": targets})
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

// Download 下载已保存的客户端文件
// @Tags 客户端构建
// @Summary 下载客户端文件
// @Router /admin/client_build/download/:id/:file [get]
func (ct *ClientBuild) Download(c *gin.Context) {
	idStr := c.Param("id")
	fileName := c.Param("file")
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
