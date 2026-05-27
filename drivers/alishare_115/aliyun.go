package alishare_115

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/alist-org/alist/v3/drivers/base"
	"github.com/alist-org/alist/v3/internal/conf"
	"github.com/alist-org/alist/v3/internal/op"
	"github.com/alist-org/alist/v3/pkg/utils"
	"github.com/go-resty/resty/v2"
	"github.com/tidwall/gjson"
)

// ── Aliyun authentication ──────────────────────────────────────────────

func (d *AliShare115) refreshToken() error {
	if base.AccessToken != "" {
		d.RefreshToken, d.AccessToken = base.RefreshToken, base.AccessToken
		return nil
	}

	url := "https://auth.aliyundrive.com/v2/account/token"
	var resp base.TokenResp
	var e ErrorResp
	_, err := base.RestyClient.R().
		SetBody(base.Json{
			"refresh_token": d.RefreshToken,
			"grant_type":    "refresh_token",
		}).
		SetHeader("Content-Type", "application/json").
		SetHeader("Rererer", "https://www.aliyundrive.com/").
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

func (d *AliShare115) getShareToken() error {
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

func (d *AliShare115) refreshTokenOpen() error {
	if base.AliOpenAccessToken != "" {
		d.RefreshTokenOpen, d.AccessTokenOpen = base.AliOpenRefreshToken, base.AliOpenAccessToken
		return nil
	}

	url := "https://openapi.alipan.com/oauth/access_token"
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
		return err
	}
	if e.Code != "" {
		return fmt.Errorf("failed to refresh open token: %s", e.Message)
	}
	if resp.RefreshToken == "" {
		return fmt.Errorf("failed to refresh open token: refresh token is empty")
	}
	base.AliOpenRefreshToken, base.AliOpenAccessToken = resp.RefreshToken, resp.AccessToken
	d.RefreshTokenOpen, d.AccessTokenOpen = resp.RefreshToken, resp.AccessToken
	return nil
}

// ── Aliyun API requests ────────────────────────────────────────────────

// request sends to the share API (Bearer + ShareToken).
func (d *AliShare115) request(url, method string, callback base.ReqCallback) ([]byte, error) {
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
		switch {
		case e.Code == "ShareLinkTokenInvalid":
			if err = d.getShareToken(); err != nil {
				return nil, err
			}
			return d.request(url, method, callback)
		case utils.SliceContains([]string{"AccessTokenInvalid", "AccessTokenExpired", "I400JD"}, e.Code):
			base.AccessToken = ""
			if err = d.refreshToken(); err != nil {
				return nil, err
			}
			return d.request(url, method, callback)
		default:
			return nil, fmt.Errorf("%s: %s", e.Code, e.Message)
		}
	}
	return resp.Body(), nil
}

// requestOpen sends to the Open API (Bearer AccessTokenOpen).
func (d *AliShare115) requestOpen(uri, method string, callback base.ReqCallback, retry ...bool) ([]byte, error) {
	req := base.RestyClient.R().
		SetHeader("Authorization", "Bearer "+d.AccessTokenOpen)
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
			if err = d.refreshTokenOpen(); err != nil {
				return nil, err
			}
			return d.requestOpen(uri, method, callback, true)
		}
		return nil, fmt.Errorf("%s: %s", e.Code, e.Message)
	}
	return res.Body(), nil
}

// ── Ensure valid tokens ────────────────────────────────────────────────

func (d *AliShare115) ensureValidToken(ctx context.Context) error {
	if d.AccessToken == "fake_token_for_fast_init" {
		if err := d.refreshToken(); err != nil {
			d.AccessToken = "fake_token_for_fast_init"
			return err
		}
		if err := d.getShareToken(); err != nil {
			d.AccessToken = "fake_token_for_fast_init"
			return err
		}
		if err := d.refreshTokenOpen(); err != nil {
			d.AccessToken = "fake_token_for_fast_init"
			return err
		}
	}

	if d.MyAliDriveId == "" {
		res, err := d.requestOpen("/adrive/v1.0/user/getDriveInfo", http.MethodPost, func(req *resty.Request) {})
		if err != nil {
			return err
		}
		defaultID := utils.Json.Get(res, "default_drive_id").ToString()
		resourceID := utils.Json.Get(res, "resource_drive_id").ToString()

		d.MyAliDriveId = defaultID
		if resourceID != "" {
			d.MyAliDriveId = resourceID
		}
	}
	return nil
}

// ── Share file listing ─────────────────────────────────────────────────

func (d *AliShare115) getFiles(fileId string) ([]File, error) {
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

		_, err := base.RestyClient.R().
			SetHeader("x-share-token", d.ShareToken).
			SetResult(&resp).
			SetError(&e).
			SetBody(data).
			Post("https://api.aliyundrive.com/adrive/v3/file/list")
		if err != nil {
			return nil, err
		}

		if e.Code != "" {
			if e.Code == "ShareLinkTokenInvalid" || utils.SliceContains([]string{"AccessTokenInvalid", "AccessTokenExpired", "I400JD"}, e.Code) {
				if err = d.getShareToken(); err != nil {
					return nil, err
				}
				continue
			}
			return nil, errors.New(e.Message)
		}

		// Pre-allocate for large dirs
		if resp.NextMarker != "" && len(files) == 0 {
			files = make([]File, 0, 2000)
		}

		files = append(files, resp.Items...)

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

// ── Copy share file to own Aliyun drive ────────────────────────────────

func (d *AliShare115) copyToMyDrive(ctx context.Context, fileID, fileName string) (string, error) {
	if newID, ok := d.CopyFiles[fileID]; ok {
		return newID, nil
	}

	data := map[string]interface{}{
		"resource": "file",
		"requests": []interface{}{
			map[string]interface{}{
				"method": "POST",
				"url":    "/file/copy",
				"id":     "0",
				"headers": map[string]interface{}{
					"Content-Type": "application/json",
				},
				"body": map[string]interface{}{
					"file_id":           fileID,
					"share_id":          d.ShareId,
					"auto_rename":       true,
					"to_parent_file_id": d.TempTransferFolderID,
					"to_drive_id":       d.MyAliDriveId,
				},
			},
		},
	}

	r, err := d.request("https://api.aliyundrive.com/adrive/v2/batch", http.MethodPost, func(req *resty.Request) {
		req.SetBody(data)
	})
	if err != nil {
		return "", fmt.Errorf("copy request failed: %w", err)
	}

	// Try: responses[0].body.file_id
	newID := extractFileIDFromBatch(r)
	if newID != "" {
		d.CopyFiles[fileID] = newID
		return newID, nil
	}

	// Fallback: direct file_id key
	newID = utils.Json.Get(r, "file_id").ToString()
	if newID != "" {
		d.CopyFiles[fileID] = newID
		return newID, nil
	}

	// Retry once
	r, err = d.request("https://api.aliyundrive.com/adrive/v2/batch", http.MethodPost, func(req *resty.Request) {
		req.SetBody(data)
	})
	if err != nil {
		return "", fmt.Errorf("copy retry failed: %w", err)
	}
	newID = extractFileIDFromBatch(r)
	if newID != "" {
		d.CopyFiles[fileID] = newID
		return newID, nil
	}

	return "", fmt.Errorf("copy failed: no file_id in response")
}

func extractFileIDFromBatch(raw []byte) string {
	responses := gjson.GetBytes(raw, "responses").Array()
	if len(responses) == 0 {
		return ""
	}
	return responses[0].Get("body.file_id").String()
}

// ── Get download info (URL + SHA1) from own drive ──────────────────────

type downloadInfo struct {
	URL  string
	SHA1 string
	Size int64
}

func (d *AliShare115) getOpenDownloadInfo(fileID, oldFileID, fileName string) (*downloadInfo, error) {
	res, err := d.requestOpen("/adrive/v1.0/openFile/getDownloadUrl", http.MethodPost, func(req *resty.Request) {
		req.SetBody(base.Json{
			"drive_id":   d.MyAliDriveId,
			"file_id":    fileID,
			"expire_sec": 14300,
		})
	})
	if err != nil {
		return nil, fmt.Errorf("get download url failed: %w", err)
	}

	downloadURL := utils.Json.Get(res, "url").ToString()
	contentHash := strings.ToUpper(utils.Json.Get(res, "content_hash").ToString())
	size := utils.Json.Get(res, "size").ToInt64()

	// Cache SHA1
	d.Hash_dict[oldFileID] = contentHash

	// Try CDN URL from response
	cdnURL := utils.Json.Get(res, "cdn_url").ToString()
	if cdnURL != "" {
		downloadURL = cdnURL
	}

	return &downloadInfo{
		URL:  downloadURL,
		SHA1: contentHash,
		Size: size,
	}, nil
}
