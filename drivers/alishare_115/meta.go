package alishare_115

import (
	"github.com/alist-org/alist/v3/internal/driver"
	"github.com/alist-org/alist/v3/internal/op"
)

type Addition struct {
	// Aliyun Open API
	RefreshToken     string `json:"RefreshToken" required:"true"`
	RefreshTokenOpen string `json:"RefreshTokenOpen" required:"true"`
	ShareId          string `json:"share_id" required:"true"`
	SharePwd         string `json:"share_pwd"`

	// Temp folder for copying from share to own Aliyun drive
	TempTransferFolderID string `json:"temp_transfer_folder_id" default:"root"`

	// 115 account
	Cookie115 string `json:"cookie_115" type:"text" required:"true" help:"115网盘cookie"`

	// 115 target folder (0 = root)
	TargetFolder115 string `json:"target_folder_115" default:"0"`

	driver.RootID
	OrderBy        string `json:"order_by" type:"select" options:"name,size,updated_at,created_at"`
	OrderDirection string `json:"order_direction" type:"select" options:"ASC,DESC"`
	OauthTokenURL  string `json:"oauth_token_url" default:"https://api.nn.ci/alist/ali_open/token"`
	ClientID       string `json:"client_id" required:"false" help:"Keep it empty if you don't have one"`
	ClientSecret   string `json:"client_secret" required:"false" help:"Keep it empty if you don't have one"`
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
