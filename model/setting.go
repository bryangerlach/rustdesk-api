package model

// Setting 通用键值设置
type Setting struct {
	IdModel
	Key   string `gorm:"uniqueIndex;size:191" json:"key"`
	Value string `gorm:"type:text" json:"value"`
	TimeModel
}