package admin

import (
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

// Create 创建并启动构建
// @Tags 客户端构建
// @Summary 创建客户端构建
// @Router /admin/client_build/create [post]
func (ct *ClientBuild) Create(c *gin.Context) {
	target := c.PostForm("target")
	builder := c.PostForm("builder")
	version := c.PostForm("version")
	name := c.PostForm("name")
	if builder != "rdgen" && target == "" {
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
	if builder == "" {
		builder = "dvforge"
	}

	// Embed the update signing public key into the uploaded builder JSON so the
	// built client can verify update metadata.
	if builder == "dvforge" || builder == "rdgen" {
		if injected, injErr := api.InjectUpdatePublicKey(data); injErr == nil {
			data = injected
		}
	}

	cb := &model.ClientBuild{
		Builder:  builder,
		Target:   target,
		Version:  version,
		Name:     name,
		Status:   "pending",
		JsonFile: file.Filename,
		Files:    "[]",
		FileList: []string{},
	}
	if err := cbSvc().Create(cb); err != nil {
		response.Fail(c, 101, response.TranslateMsg(c, "OperationFailed")+err.Error())
		return
	}
	if err := cbSvc().StartBuild(cb, data); err != nil {
		cb.Status = "error"
		cb.Error = err.Error()
		_ = cbSvc().Update(cb)
		response.Fail(c, 101, response.TranslateMsg(c, "OperationFailed")+err.Error())
		return
	}
	cb = cbSvc().InfoById(cb.Id)
	response.Success(c, cb)
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
