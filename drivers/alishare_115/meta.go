package alishare_115

import (
	"github.com/alist-org/alist/v3/internal/driver"
	"github.com/alist-org/alist/v3/internal/op"
)

type Addition struct {
	// === Aliyun share config (mirrors aliyundrive_share2open) ===
	RefreshToken        string `json:"RefreshToken" required:"true"`
	RefreshTokenOpen    string `json:"RefreshTokenOpen" required:"true"`
	ShareId             string `json:"share_id" required:"true"`
	SharePwd            string `json:"share_pwd"`
	TempTransferFolderID string `json:"TempTransferFolderID" default:"root"`
	OrderBy             string `json:"order_by" type:"select" options:"name,size,updated_at,created_at"`
	OrderDirection      string `json:"order_direction" type:"select" options:"ASC,DESC"`

	// === 115 config ===
	Cookie          string `json:"cookie" type:"text" required:"true"`
	TargetFolder115 string `json:"target_folder_115" default:"0"`

	driver.RootID
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
