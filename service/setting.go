package service

import (
	"github.com/lejianwen/rustdesk-api/v2/model"
)

type SettingService struct{}

func (s *SettingService) Get(key string) string {
	if DB == nil {
		return ""
	}
	m := &model.Setting{}
	DB.Where("key = ?", key).First(m)
	return m.Value
}

func (s *SettingService) Set(key, value string) error {
	if DB == nil {
		return nil
	}
	m := &model.Setting{}
	DB.Where("key = ?", key).First(m)
	if m.Id == 0 {
		return DB.Create(&model.Setting{Key: key, Value: value}).Error
	}
	m.Value = value
	return DB.Model(m).Update("value", value).Error
}

func (s *SettingService) Delete(key string) error {
	if DB == nil {
		return nil
	}
	return DB.Where("key = ?", key).Delete(&model.Setting{}).Error
}