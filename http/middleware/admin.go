package middleware

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/lejianwen/rustdesk-api/v2/http/response"
	"github.com/lejianwen/rustdesk-api/v2/service"
)

// backendUserAuth validates the api-token header. It returns false when the
// request has already been rejected.
func backendUserAuth(c *gin.Context) bool {
	//测试先关闭
	token := c.GetHeader("api-token")
	if token == "" {
		response.Fail(c, 403, response.TranslateMsg(c, "NeedLogin"))
		c.Abort()
		return false
	}
	user, ut := service.AllService.UserService.InfoByAccessToken(token)
	if user.Id == 0 {
		response.Fail(c, 403, response.TranslateMsg(c, "NeedLogin"))
		c.Abort()
		return false
	}

	if !service.AllService.UserService.CheckUserEnable(user) {
		c.JSON(401, gin.H{
			"error": "Unauthorized",
		})
		c.Abort()
		return false
	}

	c.Set("curUser", user)
	c.Set("token", token)
	//如果时间小于1天,token自动续期
	service.AllService.UserService.AutoRefreshAccessToken(ut)
	return true
}

// BackendUserAuth 后台权限验证中间件
func BackendUserAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !backendUserAuth(c) {
			return
		}
		c.Next()
	}
}

// ClientBuildDownloadAuth allows client build files to be fetched directly by
// the browser with a short-lived `download_token` query parameter. Falling back
// to the normal api-token header keeps existing API clients working.
func ClientBuildDownloadAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		if token := c.Query("download_token"); token != "" {
			id, file, err := service.VerifyClientBuildDownloadToken(token)
			if err == nil &&
				strconv.FormatUint(uint64(id), 10) == c.Param("id") &&
				file == c.Param("file") {
				c.Next()
				return
			}
		}
		if !backendUserAuth(c) {
			return
		}
		c.Next()
	}
}
