package official_115

import (
	"errors"
	"github.com/alist-org/alist/v3/drivers/base"
	"github.com/alist-org/alist/v3/pkg/utils"
	driver115 "github.com/xiaoyaliu00/115driver/pkg/driver"
	log "github.com/sirupsen/logrus"
)

var (
	md5Salt = "Qclm8MGWUv59TnrR0XPg"
	appVer  = "35.6.0.3"
)

func (d *Pan115) getAppVersion() (string, error) {
	result := VersionResp{}
	res, err := base.RestyClient.R().Get(driver115.ApiGetVersion)
	if err != nil {
		return "", err
	}
	err = utils.Json.Unmarshal(res.Body(), &result)
	if err != nil {
		return "", err
	}
	if len(result.Error) > 0 {
		return "", errors.New(result.Error)
	}
	return result.Data.Win.Version, nil
}

func (d *Pan115) getAppVer() string {
	ver, err := d.getAppVersion()
	if err != nil {
		log.Warnf("[115] get app version failed: %v", err)
		return appVer
	}
	if len(ver) > 0 {
		return ver
	}
	return appVer
}

func (d *Pan115) initAppVer() {
	appVer = d.getAppVer()
	log.Debugf("use app version: %v", appVer)
}

// GetAppVer returns the current app version (exported for alishare_115).
func GetAppVer() string {
	return appVer
}

// InitAppVerOnce fetches the latest app version from 115 API (package-level).
func InitAppVerOnce() {
	tmp := &Pan115{}
	tmp.appVerOnce.Do(tmp.initAppVer)
}

type VersionResp struct {
	Error string   `json:"error,omitempty"`
	Data  Versions `json:"data"`
}

type Versions struct {
	Win Version `json:"win"`
}

type Version struct {
	Version string `json:"version_code"`
}
