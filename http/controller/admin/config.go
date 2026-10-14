package admin

import (
	"github.com/gin-gonic/gin"
	"github.com/lejianwen/rustdesk-api/v2/global"
	"github.com/lejianwen/rustdesk-api/v2/http/response"
	"github.com/lejianwen/rustdesk-api/v2/model"
	"github.com/lejianwen/rustdesk-api/v2/service"
	"os"
	"strings"
)

type Config struct {
}

// ServerConfig RUSTDESK服务配置
// @Tags ADMIN
// @Summary RUSTDESK服务配置
// @Description 服务配置,给webclient提供api-server
// @Accept  json
// @Produce  json
// @Success 200 {object} response.Response
// @Failure 500 {object} response.Response
// @Router /admin/config/server [get]
// @Security token
func (co *Config) ServerConfig(c *gin.Context) {
	cf := &response.ServerConfigResponse{
		IdServer:    global.Config.Rustdesk.IdServer,
		Key:         global.Config.Rustdesk.Key,
		RelayServer: global.Config.Rustdesk.RelayServer,
		ApiServer:   global.Config.Rustdesk.ApiServer,
	}
	response.Success(c, cf)
}

// AppConfig APP服务配置
// @Tags ADMIN
// @Summary APP服务配置
// @Description APP服务配置
// @Accept  json
// @Produce  json
// @Success 200 {object} response.Response
// @Failure 500 {object} response.Response
// @Router /admin/config/app [get]
// @Security token
func (co *Config) AppConfig(c *gin.Context) {
	response.Success(c, &gin.H{
		"web_client": global.Config.App.WebClient,
	})
}

// AdminConfig ADMIN服务配置
// @Tags ADMIN
// @Summary ADMIN服务配置
// @Description ADMIN服务配置
// @Accept  json
// @Produce  json
// @Success 200 {object} response.Response
// @Failure 500 {object} response.Response
// @Router /admin/config/admin [get]
// @Security token
func (co *Config) AdminConfig(c *gin.Context) {

	u := &model.User{}
	token := c.GetHeader("api-token")
	if token != "" {
		u, _ = service.AllService.UserService.InfoByAccessToken(token)
		if !service.AllService.UserService.CheckUserEnable(u) {
			u.Id = 0
		}
	}

	if u.Id == 0 {
		response.Success(c, &gin.H{
			"title": global.Config.Admin.Title,
		})
		return
	}

	hello := global.Config.Admin.Hello
	if hello == "" {
		helloFile := global.Config.Admin.HelloFile
		if helloFile != "" {
			b, err := os.ReadFile(helloFile)
			if err == nil && len(b) > 0 {
				hello = string(b)
			}
		}
	}

	//replace {{username}} to username
	hello = strings.Replace(hello, "{{username}}", u.Username, -1)
	response.Success(c, &gin.H{
		"title": global.Config.Admin.Title,
		"hello": hello,
	})
}

// ClientBuilder GET/UPDATE persisted client builder settings.
// @Tags ADMIN
// @Summary Client builder settings
// @Router /admin/config/client_builder [get]
func (co *Config) ClientBuilder(c *gin.Context) {
	response.Success(c, &gin.H{
		"api_base":  service.ClientBuilderBaseValue(),
		"api_token": service.ClientBuilderTokenValue(),
	})
}

// @Tags ADMIN
// @Summary Update client builder settings
// @Router /admin/config/client_builder [post]
func (co *Config) ClientBuilderUpdate(c *gin.Context) {
	f := &struct {
		APIBase  string `json:"api_base"`
		APIToken string `json:"api_token"`
	}{}
	if err := c.ShouldBindJSON(f); err != nil {
		response.Fail(c, 101, response.TranslateMsg(c, "ParamsError")+err.Error())
		return
	}
	svc := &service.SettingService{}
	if f.APIBase != "" {
		_ = svc.Set(service.SettingKeyClientBuilderAPIBase, f.APIBase)
	} else {
		_ = svc.Delete(service.SettingKeyClientBuilderAPIBase)
	}
	if f.APIToken != "" {
		_ = svc.Set(service.SettingKeyClientBuilderAPIToken, f.APIToken)
	} else {
		_ = svc.Delete(service.SettingKeyClientBuilderAPIToken)
	}
	response.Success(c, nil)
}
