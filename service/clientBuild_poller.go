package service

import (
	"time"

	"github.com/lejianwen/rustdesk-api/v2/model"
)

// StartClientBuildPoller runs a background loop that periodically refreshes
// unfinished client builds so their status is checked and files are
// downloaded automatically when they complete.
func StartClientBuildPoller() {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			var builds []model.ClientBuild
			if DB == nil {
				continue
			}
			DB.Where("status NOT IN ?", []string{"done", "error"}).Find(&builds)
			for i := range builds {
				if builds[i].JobId == "" {
					continue
				}
				cb := &builds[i]
				_ = (&ClientBuildService{}).RefreshStatus(cb)
			}
		}
	}()
}