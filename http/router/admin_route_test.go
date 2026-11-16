package router

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lejianwen/rustdesk-api/v2/http/middleware"
	"github.com/lejianwen/rustdesk-api/v2/service"
)

func TestClientBuildDownloadTicketRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	g := gin.New()
	g.GET("/api/admin/client_build/download/:id/:file",
		middleware.ClientBuildDownloadAuth(),
		func(c *gin.Context) { c.Status(http.StatusNoContent) })

	token, err := service.CreateClientBuildDownloadToken(7, "rustdesk 1.5.1.exe", time.Minute)
	if err != nil {
		t.Fatalf("CreateClientBuildDownloadToken: %v", err)
	}
	path := "/api/admin/client_build/download/7/" +
		url.PathEscape("rustdesk 1.5.1.exe") +
		"?download_token=" + url.QueryEscape(token)
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d (%s)", rec.Code, http.StatusNoContent, rec.Body.String())
	}
}
