package admin

type ClientBuildQuery struct {
	UserId uint `json:"user_id" form:"user_id"`
	PageQuery
}

type ClientBuildForm struct {
	Id     uint   `json:"id" form:"id"`
	UserId uint   `json:"user_id" form:"user_id"`
	Target string `json:"target" form:"target"`
	Name   string `json:"name" form:"name"`
}

type ClientBuildUpdateForm struct {
	Id                uint `json:"id" form:"id"`
	ExcludeFromUpdate bool `json:"exclude_from_update" form:"exclude_from_update"`
}
