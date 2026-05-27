package _115

import (
	"context"
	"fmt"
	"time"

	//driver115 "github.com/SheltonZhu/115driver/pkg/driver"
	driver115 "github.com/xiaoyaliu00/115driver/pkg/driver"
	"github.com/alist-org/alist/v3/internal/driver"
	"github.com/alist-org/alist/v3/internal/model"
	"github.com/alist-org/alist/v3/pkg/utils"
	"github.com/pkg/errors"
	"github.com/alist-org/alist/v3/internal/errs"
	"golang.org/x/time/rate"
	"github.com/alist-org/alist/v3/pkg/cron"
)

type Pan115 struct {
	model.Storage
	Addition
	client  *driver115.Pan115Client
	limiter *rate.Limiter
	DownloadUrl_dict map[string]string
	cron        *cron.Cron
}

func (d *Pan115) Config() driver.Config {
	return config
}

func (d *Pan115) GetAddition() driver.Additional {
	return &d.Addition
}

func (d *Pan115) Init(ctx context.Context) error {
    var downloadurlmap map[string]string
    downloadurlmap = make(map[string]string)
    d.DownloadUrl_dict = downloadurlmap

    d.cron = cron.NewCron(time.Second * 10)
    d.cron.Do(func() {
                //fmt.Println(time.Now().Format("01-02-2006 15:04:05")," 清空115缓存下载链接: ", d.MountPath) //d.ShareId) //d.MyAliDriveId)
                d.DownloadUrl_dict = make(map[string]string)
    })

	if d.LimitRate > 0 {
		d.limiter = rate.NewLimiter(rate.Limit(d.LimitRate), 1)
	}
	return d.login()
}

func (d *Pan115) WaitLimit(ctx context.Context) error {
	if d.limiter != nil {
		return d.limiter.Wait(ctx)
	}
	return nil
}

func (d *Pan115) Drop(ctx context.Context) error {
	return nil
}

func (d *Pan115) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {
	if err := d.WaitLimit(ctx); err != nil {
		return nil, err
	}
	files, err := d.getFiles(dir.GetID())
	if err != nil && !errors.Is(err, driver115.ErrNotExist) {
		if errs.IsObjectNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return utils.SliceConvert(files, func(src FileObj) (model.Obj, error) {
		return &src, nil
	})
}

func (d *Pan115) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
    file_id :=  file.GetID()
    file_name := file.GetName()
    existed_download_url, ok := d.DownloadUrl_dict[file_id]
    if ok {
        fmt.Println(time.Now().Format("01-02-2006 15:04:05"),"115下载链接已存在: ",file_name)
        return &model.Link{URL: existed_download_url}, nil
    }

	if err := d.WaitLimit(ctx); err != nil {
		return nil, err
	}
	userAgent := args.Header.Get("User-Agent")
	downloadInfo, err := d.
		DownloadWithUA(file.(*FileObj).PickCode, userAgent)
	if err != nil {
		fmt.Println(time.Now().Format("01-02-2006 15:04:05"),file_name,"获取115直链发生错误: ",err)
		return nil, err
	}
	link := &model.Link{
		URL:    downloadInfo.Url.Url,
		Header: downloadInfo.Header,
	}
	
	d.DownloadUrl_dict[file_id] = downloadInfo.Url.Url
    fmt.Println(time.Now().Format("01-02-2006 15:04:05"),file_name,"获取115直链: ",downloadInfo.Url.Url)
	return link, nil
}

var _ driver.Driver = (*Pan115)(nil)
