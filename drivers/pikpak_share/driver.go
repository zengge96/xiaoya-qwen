package pikpak_share

import (
	"context"
	"net/http"
	"time"
	"fmt"
	"github.com/alist-org/alist/v3/drivers/base"
	"github.com/alist-org/alist/v3/internal/driver"
	"github.com/alist-org/alist/v3/internal/model"
	"github.com/alist-org/alist/v3/pkg/utils"
	"github.com/alist-org/alist/v3/pkg/cron"
	"github.com/go-resty/resty/v2"
	"golang.org/x/oauth2"
	log "github.com/sirupsen/logrus"
)

type PikPakShare struct {
	model.Storage
	Addition
	cron2	    *cron.Cron
	cron		*cron.Cron
	DownloadUrl_dict map[string]string
	RefreshToken  string
	AccessToken   string
	PassCodeToken string
    oauth2Token oauth2.TokenSource
}

func (d *PikPakShare) Config() driver.Config {
	return config
}

func (d *PikPakShare) GetAddition() driver.Additional {
	return &d.Addition
}

func (d *PikPakShare) Init(ctx context.Context) error {
if d.ClientID == "" || d.ClientSecret == "" {
		d.ClientID = "YNxT9w7GMdWvEOKa"
		d.ClientSecret = "dbw2OtmVEeuUvIptb1Coyg"
	}

	withClient := func(ctx context.Context) context.Context {
		return context.WithValue(ctx, oauth2.HTTPClient, base.HttpClient)
	}

	oauth2Config := &oauth2.Config{
		ClientID:     d.ClientID,
		ClientSecret: d.ClientSecret,
		Endpoint: oauth2.Endpoint{
			AuthURL:   "https://user.mypikpak.com/v1/auth/signin",
			TokenURL:  "https://user.mypikpak.com/v1/auth/token",
			AuthStyle: oauth2.AuthStyleInParams,
		},
	}

	oauth2Token, err := oauth2Config.PasswordCredentialsToken(withClient(ctx), d.Username, d.Password)
	if err != nil {
		return err
	}
	d.oauth2Token = oauth2Config.TokenSource(withClient(context.Background()), oauth2Token)
	
	var downloadurlmap map[string]string
	downloadurlmap = make(map[string]string)
	d.DownloadUrl_dict = downloadurlmap
	
	d.cron = cron.NewCron(time.Minute * 30)
    d.cron.Do(func() {
		oauth2Token, err := oauth2Config.PasswordCredentialsToken(withClient(ctx), d.Username, d.Password)
        if err != nil {
              log.Errorf("%+v", err)
        }
		if err == nil {
			d.oauth2Token = oauth2Config.TokenSource(withClient(context.Background()), oauth2Token)
		}
    })

	d.cron2 = cron.NewCron(time.Minute * 240)
    d.cron2.Do(func() {
		if len(d.DownloadUrl_dict) > 0{
			//fmt.Println(time.Now().Format("01-02-2006 15:04:05")," PikPak清空缓存下载链接")
			downloadurlmap = make(map[string]string)
			d.DownloadUrl_dict = downloadurlmap
		}
    })
	
	if d.SharePwd != "" {
		err = d.getSharePassToken()
		if err != nil {
			return err
		}
	}
	return nil
}

func (d *PikPakShare) Drop(ctx context.Context) error {
	if d.cron2 != nil { d.cron2.Stop() }
	return nil
}

func (d *PikPakShare) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {
	files, err := d.getFiles(dir.GetID())
	if err != nil {
		return nil, err
	}
	return utils.SliceConvert(files, func(src File) (model.Obj, error) {
		return fileToObj(src), nil
	})
}

func (d *PikPakShare) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
	var resp ShareResp
	query := map[string]string{
		"share_id":        d.ShareId,
		"file_id":         file.GetID(),
		"pass_code_token": d.PassCodeToken,
	}
	
	file_id := file.GetID()
	file_name := file.GetName()
	
	existed_download_url, ok := d.DownloadUrl_dict[file_id]
	if ok {
		fmt.Println(time.Now().Format("01-02-2006 15:04:05"),"PikPak下载链接已存在: ",file_name)
		return &model.Link{URL: existed_download_url,}, nil
	}

    count := 1
    for {
		_, err := d.request("https://api-drive.mypikpak.com/drive/v1/share/file_info", http.MethodGet, func(req *resty.Request) {
			req.SetQueryParams(query) }, &resp)
		
        if err != nil {
            if count > 2 {
                  return &model.Link{URL: "http://img.xiaoya.pro/abnormal.png",}, err
            }
			fmt.Println("PikPak获取下载链接失败第",count,"次 ",file_name)
            count += 1
            time.Sleep(1 * 1000 * time.Millisecond)
        }

        if err == nil {
            //d.DownloadUrl_dict[file_id] = resp.FileInfo.WebContentLink
			d.DownloadUrl_dict[file_id] = resp.FileInfo.Medias[0].Link.Url
			fmt.Println(time.Now().Format("01-02-2006 15:04:05"),"文件: ",file_name,"  新增下载直链: ", d.DownloadUrl_dict[file_id])
			fmt.Println(time.Now().Format("01-02-2006 15:04:05"),d.ShareId," 已成功缓存了",len(d.DownloadUrl_dict),"个文件")
			//return &model.Link{URL: resp.FileInfo.WebContentLink,}, nil
			return &model.Link{URL: resp.FileInfo.Medias[0].Link.Url,}, nil
		}	
    }		
}

var _ driver.Driver = (*PikPakShare)(nil)
