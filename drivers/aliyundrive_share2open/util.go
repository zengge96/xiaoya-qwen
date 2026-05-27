package aliyundrive_share2open

import (
	"errors"
	"fmt"
	"net/http"
	"github.com/alist-org/alist/v3/drivers/base"
    	"github.com/alist-org/alist/v3/internal/conf"
	"github.com/alist-org/alist/v3/pkg/utils"
	"github.com/alist-org/alist/v3/internal/op"
	log "github.com/sirupsen/logrus"
)

func (d *AliyundriveShare2Open) DoRefreshToken() error {
    if base.AccessToken != ""{
        d.RefreshToken, d.AccessToken = base.RefreshToken, base.AccessToken
        return nil
    }

	url := "https://auth.aliyundrive.com/v2/account/token"
	var resp base.TokenResp
	var e ErrorResp
	_, err := base.RestyClient.R().
	SetBody(base.Json{"refresh_token": d.RefreshToken, "grant_type": "refresh_token"}).
		SetHeader("Content-Type", "application/json").SetHeader("Rererer", "https://www.aliyundrive.com/").
		SetResult(&resp).
		SetError(&e).
		Post(url)
	if err != nil {
		return err
	}
	
	if e.Code != "" {
		return fmt.Errorf("failed to refresh token: %s", e.Message)
	}
	
	base.RefreshToken, base.AccessToken = resp.RefreshToken, resp.AccessToken
	d.RefreshToken, d.AccessToken = resp.RefreshToken, resp.AccessToken
	op.MustSaveDriverStorage(d)
	return nil
}

func (d *AliyundriveShare2Open) DoGetShareToken() error {
	data := base.Json{
		"share_id": d.ShareId,
	}
	if d.SharePwd != "" {
		data["share_pwd"] = d.SharePwd
	}
	var e ErrorResp
	var resp ShareTokenResp
	_, err := base.RestyClient.R().
		SetResult(&resp).SetError(&e).SetBody(data).
		Post("https://api.aliyundrive.com/v2/share_link/get_share_token")
	if err != nil {
		return err
	}
	if e.Code != "" {
		return errors.New(e.Message)
	}
	d.ShareToken = resp.ShareToken
	return nil
}

func (d *AliyundriveShare2Open) Request(url, method string, callback base.ReqCallback) ([]byte, error) {
        var e ErrorResp
        req := base.RestyClient.R().
                SetError(&e).
                SetHeader("content-type", "application/json").
                SetHeader("Authorization", "Bearer\t"+d.AccessToken).
                SetHeader("X-Share-Token", d.ShareToken)
        if callback != nil {
                callback(req)
        } else {
                req.SetBody("{}")
        }
        resp, err := req.Execute(method, url)
        if err != nil {
                return nil, err
        }
        if e.Code != "" {
		fmt.Println(e.Code,": ",e.Message)
                if utils.SliceContains([]string{"AccessTokenInvalid", "AccessTokenExpired", "I400JD"}, e.Code) || e.Code == "ShareLinkTokenInvalid" {
                        if  utils.SliceContains([]string{"AccessTokenInvalid", "AccessTokenExpired", "I400JD"}, e.Code) {
								base.AccessToken = ""
                                err = d.DoRefreshToken()
                        } else {
                                err = d.DoGetShareToken()
                        }
                        if err != nil {
                                return nil, err
                        }
                        return d.Request(url, method, callback)
                } else {
                        return nil, errors.New(e.Code + ": " + e.Message)
                }
        }
        return resp.Body(), nil
}

func (d *AliyundriveShare2Open) GetFiles(fileId string) ([]File, error) {
    files := make([]File, 0, 200)
    var marker string

    for {
        data := map[string]interface{}{
            "limit":           200,
            "order_by":        d.OrderBy,
            "order_direction": d.OrderDirection,
            "parent_file_id":  fileId,
            "share_id":        d.ShareId,
            "marker":          marker,
        }

        var e ErrorResp
        var resp ListResp

        res, err := base.RestyClient.R().
            SetHeader("x-share-token", d.ShareToken).
            SetResult(&resp).
            SetError(&e).
            SetBody(data).
            Post("https://api.aliyundrive.com/adrive/v3/file/list")

        if err != nil {
            return nil, err
        }
        log.Debugf("aliyundrive share get files: %s", res.String())

        if e.Code != "" {
            if (utils.SliceContains([]string{"AccessTokenInvalid","AccessTokenExpired","I400JD"},e.Code) || e.Code == "ShareLinkTokenInvalid") {
                err = d.DoGetShareToken()
                if err != nil {
                    return nil, err
                }
                return d.GetFiles(fileId) 
            }
            return nil, errors.New(e.Message)
        }

        if resp.NextMarker != "" && len(files) == 0 {
            newFiles := make([]File, 0, 2000)
            files = newFiles
        }

        if len(resp.Items) > 0 {
            files = append(files, resp.Items...)
        }

        marker = resp.NextMarker
        if marker == "" {
            break
        }
    }

    if len(files) > 0 && d.MyAliDriveId == "" {
        d.MyAliDriveId = files[0].DriveId
    }

    return files, nil
}

func (d *AliyundriveShare2Open) DoRefreshTokenOpen() error {
    if base.AliOpenAccessToken != ""{
        d.RefreshTokenOpen, d.AccessTokenOpen = base.AliOpenRefreshToken, base.AliOpenAccessToken
        return nil
    }
    url :=  "https://openapi.alipan.com/oauth/access_token"
    var resp base.TokenResp
	var e ErrorResp
    d.OauthTokenURL = conf.Conf.Opentoken_auth_url
	
    if d.OauthTokenURL != "" && d.ClientID == "" {
        url = d.OauthTokenURL
	}

	_, err := base.RestyClient.R().
        ForceContentType("application/json").
        SetBody(base.Json{
            "client_id":     d.ClientID,
            "client_secret": d.ClientSecret,
            "grant_type":    "refresh_token",
            "refresh_token": d.RefreshTokenOpen,
        }).
        SetResult(&resp).SetError(&e).Post(url)

    if err != nil {
        fmt.Errorf("failed to refresh token: %s", e.Message)
        return err
    }
    if e.Code != "" {
        return fmt.Errorf("failed to refresh token: %s", e.Message)
    }

    if resp.RefreshToken == "" {
        return fmt.Errorf("failed to refresh token: refresh token is empty")
    }
    base.AliOpenRefreshToken, base.AliOpenAccessToken = resp.RefreshToken, resp.AccessToken
    d.RefreshTokenOpen, d.AccessTokenOpen = resp.RefreshToken, resp.AccessToken
    return nil
}

func (d *AliyundriveShare2Open) RequestOpen(uri, method string, callback base.ReqCallback, retry ...bool) ([]byte, error) {
	req := base.RestyClient.R()
	req.SetHeader("Authorization", "Bearer "+d.AccessTokenOpen)
	if method == http.MethodPost {
		req.SetHeader("Content-Type", "application/json")
	}
	if callback != nil {
		callback(req)
	}
	var e ErrorResp
	req.SetError(&e)
	res, err := req.Execute(method, d.base+uri)
	if err != nil {
		return nil, err
	}
	isRetry := len(retry) > 0 && retry[0]
	if e.Code != "" {
		if !isRetry && utils.SliceContains([]string{"AccessTokenInvalid", "AccessTokenExpired", "I400JD"}, e.Code) {
			base.AliOpenAccessToken = ""
			err = d.DoRefreshTokenOpen()
			if err != nil {
				return nil, err
			}
			return d.RequestOpen(uri, method, callback, true)
		}
		return nil, fmt.Errorf("%s:%s", e.Code, e.Message)
	}
	return res.Body(), nil
}

func (d *AliyundriveShare2Open) requestOpenCDN(url, method string, callback base.ReqCallback, retry ...bool) ([]byte, error) {
    req := base.RestyClient.R()
    req.SetHeader("Authorization", "Bearer "+d.AccessToken)
    if method == http.MethodPost {
        req.SetHeader("Content-Type", "application/json")
    }
    if callback != nil {
        callback(req)
    }
    var e ErrorResp
    req.SetError(&e)
    res, err := req.Execute(method, url)
    if err != nil {
        return nil, err
    }
    isRetry := len(retry) > 0 && retry[0]
    if e.Code != "" {
        if !isRetry && utils.SliceContains([]string{"AccessTokenInvalid", "AccessTokenExpired", "I400JD"}, e.Code) {
            base.AliOpenAccessToken = ""
            err = d.DoRefreshTokenOpen()
            if err != nil {
                return nil, err
            }
            return d.requestOpenCDN(url, method, callback, true)
        }
        return nil, fmt.Errorf("%s:%s", e.Code, e.Message)
    }
    return res.Body(), nil
}
