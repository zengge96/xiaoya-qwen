package _115

import (
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/alist-org/alist/v3/internal/conf"
	"github.com/alist-org/alist/v3/pkg/utils"

	//driver115 "github.com/SheltonZhu/115driver/pkg/driver"
	driver115 "github.com/xiaoyaliu00/115driver/pkg/driver"
	crypto "github.com/gaoyb7/115drive-webdav/115"
	"github.com/pkg/errors"
)

var UserAgent = driver115.UA115Browser

func (d *Pan115) login() error {
	var err error
	opts := []driver115.Option{
		driver115.UA(UserAgent),
		func(c *driver115.Pan115Client) {
			c.Client.SetTLSClientConfig(&tls.Config{InsecureSkipVerify: conf.Conf.TlsInsecureSkipVerify})
		},
	}
	d.client = driver115.New(opts...)
	cr := &driver115.Credential{}
	if d.Addition.QRCodeToken != "" {
		s := &driver115.QRCodeSession{
			UID: d.Addition.QRCodeToken,
		}
		if cr, err = d.client.QRCodeLoginWithApp(s, driver115.LoginApp(d.QRCodeSource)); err != nil {
			return errors.Wrap(err, "failed to login by qrcode")
		}
		d.Addition.Cookie = fmt.Sprintf("UID=%s;CID=%s;SEID=%s;KID=%s", cr.UID, cr.CID, cr.SEID, cr.KID)
		d.Addition.QRCodeToken = ""
	} else if d.Addition.Cookie != "" {
		cookies := strings.Split(d.Addition.Cookie, ";")

    	var kid string
   		for _, cookie := range cookies {
   			cookie = strings.TrimSpace(cookie)
   			if strings.HasPrefix(cookie, "KID=") {
       			kid = strings.TrimPrefix(cookie, "KID=")
       			break
   			}
   		}

		if err = cr.FromCookie(d.Addition.Cookie); err != nil {
			return errors.Wrap(err, "failed to login by cookies")
		}

		if kid != "" {
      		d.client.Client.SetCookie(&http.Cookie{
        		Name:   "KID",
        		Value:  kid,
        		Path:   "/",
        		Domain: "115.com",
      		})
    	}

		d.client.ImportCredential(cr)
	} else {
		return errors.New("missing cookie or qrcode account")
	}
	return d.client.LoginCheck()
}

func (d *Pan115) getFiles(fileId string) ([]FileObj, error) {
	res := make([]FileObj, 0)
	if d.PageSize <= 0 {
		d.PageSize = driver115.FileListLimit
	}
	files, err := d.client.ListWithLimit(fileId, d.PageSize)
	if err != nil {
		return nil, err
	}
	for _, file := range *files {
		res = append(res, FileObj{file})
	}
	return res, nil
}

const (
	appVer = "2.0.3.6"
)

func (c *Pan115) DownloadWithUA(pickCode, ua string) (*driver115.DownloadInfo, error) {
	key := crypto.GenerateKey()
	result := driver115.DownloadResp{}
	params, err := utils.Json.Marshal(map[string]string{"pickcode": pickCode})
	if err != nil {
		return nil, err
	}

	data := crypto.Encode(params, key)

	bodyReader := strings.NewReader(url.Values{"data": []string{data}}.Encode())
	reqUrl := fmt.Sprintf("%s?t=%s", driver115.ApiDownloadGetUrl, driver115.Now().String())
	req, _ := http.NewRequest(http.MethodPost, reqUrl, bodyReader)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", c.Cookie)
	req.Header.Set("User-Agent", ua)

	resp, err := c.client.Client.GetClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if err := utils.Json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	if err = result.Err(string(body)); err != nil {
		return nil, err
	}

	bytes, err := crypto.Decode(string(result.EncodedData), key)
	if err != nil {
		return nil, err
	}

	downloadInfo := driver115.DownloadData{}
	if err := utils.Json.Unmarshal(bytes, &downloadInfo); err != nil {
		return nil, err
	}

	for _, info := range downloadInfo {
		if info.FileSize < 0 {
			return nil, driver115.ErrDownloadEmpty
		}
		info.Header = resp.Request.Header
		return info, nil
	}
	return nil, driver115.ErrUnexpected
}
