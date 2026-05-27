package base

import (
	"github.com/go-resty/resty/v2"
	"time"
)

type Json map[string]interface{}

type TokenResp struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

type ReqCallback func(req *resty.Request)

// 115 rate limit tracking
var (
	V115lasttime   time.Time
	V115countwindow = 24 * time.Hour // 24小时窗口
	V115count      int
	V115novip      int // 0=VIP, 1=非会员
)

// Ali token cache for alishare_115 and aliyundrive_share2open
var (
	AccessToken  string
	RefreshToken string
)

// Shared 115 cookie (used by alishare_115)
var Pan115Cookie string

// Ali drive ID used by alishare_115
var MyAliDriveId string

