package model

import "encoding/json"

// ClientBuild 客户端构建记录
type ClientBuild struct {
	IdModel
	UserId     uint   `json:"user_id"`
	Builder    string `json:"builder" gorm:"default:dvforge"`
	Name       string `json:"name"`
	Target     string `json:"target"`
	Version    string `json:"version"`
	JobId      string `json:"job_id"`
	StatusUrl  string `json:"status_url"`
	Status     string `json:"status"`
	JsonFile   string `json:"json_file"`
	Files      string `json:"-" gorm:"type:text"`
	FileList   []string `gorm:"-" json:"files"`
	Error      string `json:"error"`
	TimeModel
}

// ClientBuildList 客户端构建列表
type ClientBuildList struct {
	ClientBuilds []*ClientBuild `json:"list,omitempty"`
	Pagination
}

func (cb *ClientBuild) DecodeFiles() {
	if cb.Files == "" {
		cb.FileList = []string{}
		return
	}
	var arr []string
	if err := json.Unmarshal([]byte(cb.Files), &arr); err != nil {
		cb.FileList = []string{}
		return
	}
	if arr == nil {
		arr = []string{}
	}
	cb.FileList = arr
}

func (cb *ClientBuild) EncodeFiles() {
	if cb.FileList == nil {
		cb.FileList = []string{}
	}
	b, _ := json.Marshal(cb.FileList)
	cb.Files = string(b)
}