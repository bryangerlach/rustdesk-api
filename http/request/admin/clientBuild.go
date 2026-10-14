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