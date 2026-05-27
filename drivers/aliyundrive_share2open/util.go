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

func (d *AliyundriveShare2Open) refreshToken() error {
    if base.AccessToken != ""{
        d.RefreshToken, d.AccessToken = base.RefreshToken, base.AccessToken
        //fmt.Println("AccessToken 已存在")
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

// do others that not defined in Driver interface
func (d *AliyundriveShare2Open) getShareToken() error {
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

func (d *AliyundriveShare2Open) request(url, method string, callback base.ReqCallback) ([]byte, error) {
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
                                err = d.refreshToken()
                        } else {
                                err = d.getShareToken()
                        }
                        if err != nil {
                                return nil, err
                        }
                        return d.request(url, method, callback)
                } else {
                        return nil, errors.New(e.Code + ": " + e.Message)
                }
        }
        return resp.Body(), nil
}

func (d *AliyundriveShare2Open) getFiles(fileId string) ([]File, error) {
    // 1. 初始容量预设（第一页 200 条）
    files := make([]File, 0, 200)
    var marker string

    for {
        // 2. 构造请求体，避免使用 "first" 这种魔术字符串
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

        // 3. 执行请求（注意：此处使用了读锁保护 ShareToken）
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

        // 4. Token 失效处理与并发优化
        if e.Code != "" {
            if (utils.SliceContains([]string{"AccessTokenInvalid","AccessTokenExpired","I400JD"},e.Code) || e.Code == "ShareLinkTokenInvalid") {
                err = d.getShareToken()
                if err != nil {
                    return nil, err
                }
                return d.getFiles(fileId) 
            }
            return nil, errors.New(e.Message)
        }

        // 5. 核心内存优化：第一页拿到后发现还有后续，直接扩容容器
        if resp.NextMarker != "" && len(files) == 0 {
            // 预估大目录容量，避免后续多次扩容带来的 CPU 拷贝消耗
            newFiles := make([]File, 0, 2000)
            files = newFiles
        }

        // 合并数据
        if len(resp.Items) > 0 {
            files = append(files, resp.Items...)
        }

        // 6. 翻页控制
        marker = resp.NextMarker
        if marker == "" {
            break
        }
    }

    // 7. 辅助信息更新
    if len(files) > 0 && d.MyAliDriveId == "" {
        d.MyAliDriveId = files[0].DriveId
    }

    return files, nil
}

func (d *AliyundriveShare2Open) refreshTokenOpen() error {

    if base.AliOpenAccessToken != ""{
        d.RefreshTokenOpen, d.AccessTokenOpen = base.AliOpenRefreshToken, base.AliOpenAccessToken
        //fmt.Println("AliOpenAccessToken 已存在")
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

func (d *AliyundriveShare2Open) requestOpen(uri, method string, callback base.ReqCallback, retry ...bool) ([]byte, error) {

	// fmt.Println("AccessTokenOpen:\n",d.AccessTokenOpen,"\nAliOpenAccessToken:\n", base.AliOpenAccessToken )
	req := base.RestyClient.R()
	// TODO check whether access_token is expired
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
			err = d.refreshTokenOpen()
			if err != nil {
				return nil, err
			}
			return d.requestOpen(uri, method, callback, true)
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
            err = d.refreshTokenOpen()
            if err != nil {
                return nil, err
            }
            return d.requestOpenCDN(url, method, callback, true)
        }
        return nil, fmt.Errorf("%s:%s", e.Code, e.Message)
    }
    return res.Body(), nil
}
