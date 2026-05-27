package alishare_115

import (
	"github.com/alist-org/alist/v3/internal/driver"
	"github.com/alist-org/alist/v3/internal/op"
)

type Addition struct {
	// 阿里云盘分享配置（复用 aliyundrive_share2open）
	RefreshToken         string `json:"RefreshToken" required:"true"`
	RefreshTokenOpen     string `json:"RefreshTokenOpen" required:"true"`
	TempTransferFolderID string `json:"TempTransferFolderID" default:"root"`
	ShareId              string `json:"share_id" required:"true"`
	SharePwd             string `json:"share_pwd"`
	driver.RootID
	OrderBy              string `json:"order_by" type:"select" options:"name,size,updated_at,created_at"`
	OrderDirection       string `json:"order_direction" type:"select" options:"ASC,DESC"`
	OauthTokenURL        string `json:"oauth_token_url" default:"https://api.nn.ci/alist/ali_open/token"`
	ClientID             string `json:"client_id" required:"false" help:"Keep it empty if you don't have one"`
	ClientSecret         string `json:"client_secret" required:"false" help:"Keep it empty if you don't have one"`

	// 115 配置
	TargetFolder115      string `json:"target_folder_115" default:"0" help:"115 目标文件夹 ID，用于保存转存的文件"`
}

var config = driver.Config{
	Name:        "AliShare115",
	LocalSort:   false,
	OnlyProxy:   false,
	NoUpload:    true,
	OnlyLocal:   false,
	NoCache:     false,
	NeedMs:      false,
	DefaultRoot: "root",
}

func init() {
	op.RegisterDriver(func() driver.Driver {
		return &AliShare115{}
	})
}
