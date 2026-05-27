package aliyundrive_open

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
)

// do others that not defined in Driver interface

func (d *AliyundriveOpen) refreshToken() error {
	url := d.base + "/oauth/access_token"
	d.OauthTokenURL = conf.Conf.Opentoken_auth_url
	if d.OauthTokenURL != "" && d.ClientID == "" {
		url = d.OauthTokenURL
	}
	var resp base.TokenResp
	var e ErrResp
	_, err := base.RestyClient.R().
		ForceContentType("application/json").
		SetBody(base.Json{
			"client_id":     d.ClientID,
			"client_secret": d.ClientSecret,
			"grant_type":    "refresh_token",
			"refresh_token": d.RefreshToken,
		}).
		SetResult(&resp).
		SetError(&e).
		Post(url)
	if err != nil {
		return err
	}
	if e.Code != "" {
		return fmt.Errorf("failed to refresh token: %s", e.Message)
	}
	if resp.RefreshToken == "" {
		return errors.New("failed to refresh token: refresh token is empty")
	}
	d.RefreshToken, d.AccessToken = resp.RefreshToken, resp.AccessToken
	op.MustSaveDriverStorage(d)
	return nil
}

func (d *AliyundriveOpen) request(uri, method string, callback base.ReqCallback, retry ...bool) ([]byte, error) {
	req := base.RestyClient.R()
	// TODO check whether access_token is expired
	req.SetHeader("Authorization", "Bearer "+d.AccessToken)
	if method == http.MethodPost {
		req.SetHeader("Content-Type", "application/json")
	}
	if callback != nil {
		callback(req)
	}
	var e ErrResp
	req.SetError(&e)
	res, err := req.Execute(method, d.base+uri)
	if err != nil {
		return nil, err
	}
	isRetry := len(retry) > 0 && retry[0]
	if e.Code != "" {
		if !isRetry && (utils.SliceContains([]string{"AccessTokenInvalid", "AccessTokenExpired", "I400JD"}, e.Code) || d.AccessToken == "") {
			err = d.refreshToken()
			if err != nil {
				return nil, err
			}
			return d.request(uri, method, callback, true)
		}
		return nil, fmt.Errorf("%s:%s", e.Code, e.Message)
	}
	return res.Body(), nil
}

func (d *AliyundriveOpen) getFiles(fileId string) ([]File, error) {
    // 初始预留 200 个位置（第一页的量）
    res := make([]File, 0, 200)
    var marker string

    for {
        var resp Files
        data := map[string]interface{}{
            "drive_id":        d.DriveId,
            "limit":           200,
            "order_by":        d.OrderBy,
            "order_direction": d.OrderDirection,
            "parent_file_id":  fileId,
        }
        if marker != "" {
            data["marker"] = marker
        }

        // 执行请求
        _, err := d.request("/adrive/v1.0/openFile/list", http.MethodPost, func(req *resty.Request) {
            req.SetBody(data).SetResult(&resp)
        })
        if err != nil {
            return nil, err
        }

        // 核心优化点：在拿到第一页数据后，判断是否还有后续
        // 如果有下一页 (NextMarker != "")，且这是第一页 (res 为空)
        // 说明是一个大文件夹，此时我们手动扩容 res 的容量到 2000
        if resp.NextMarker != "" && len(res) == 0 {
            newRes := make([]File, 0, 2000) 
            res = newRes // 此时 res 还是空的，但底层的 capacity 已经变成了 2000
        }

        // 合并数据
        if len(resp.Items) > 0 {
            res = append(res, resp.Items...)
        }

        marker = resp.NextMarker
        if marker == "" {
            break
        }
    }

    return res, nil
}

func makePartInfos(size int) []base.Json {
	partInfoList := make([]base.Json, size)
	for i := 0; i < size; i++ {
		partInfoList[i] = base.Json{"part_number": 1 + i}
	}
	return partInfoList
}

func (d *AliyundriveOpen) getUploadUrl(count int, fileId, uploadId string) ([]PartInfo, error) {
	partInfoList := makePartInfos(count)
	var resp CreateResp
	_, err := d.request("/adrive/v1.0/openFile/getUploadUrl", http.MethodPost, func(req *resty.Request) {
		req.SetBody(base.Json{
			"drive_id":       d.DriveId,
			"file_id":        fileId,
			"part_info_list": partInfoList,
			"upload_id":      uploadId,
		}).SetResult(&resp)
	})
	return resp.PartInfoList, err
}

func (d *AliyundriveOpen) uploadPart(ctx context.Context, i, count int, reader *utils.MultiReadable, resp *CreateResp, retry bool) error {
	partInfo := resp.PartInfoList[i-1]
	uploadUrl := partInfo.UploadUrl
	if d.InternalUpload {
		uploadUrl = strings.ReplaceAll(uploadUrl, "https://cn-beijing-data.aliyundrive.net/", "http://ccp-bj29-bj-1592982087.oss-cn-beijing-internal.aliyuncs.com/")
	}
	req, err := http.NewRequest("PUT", uploadUrl, reader)
	if err != nil {
		return err
	}
	req = req.WithContext(ctx)
	res, err := base.HttpClient.Do(req)
	if err != nil {
		if retry {
			reader.Reset()
			return d.uploadPart(ctx, i, count, reader, resp, false)
		}
		return err
	}
	res.Body.Close()
	if retry && res.StatusCode == http.StatusForbidden {
		resp.PartInfoList, err = d.getUploadUrl(count, resp.FileId, resp.UploadId)
		if err != nil {
			return err
		}
		reader.Reset()
		return d.uploadPart(ctx, i, count, reader, resp, false)
	}
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusConflict {
		return fmt.Errorf("upload status: %d", res.StatusCode)
	}
	return nil
}
