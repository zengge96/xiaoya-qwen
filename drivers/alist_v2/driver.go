package alist_v2

import (
	"context"
	"time"
	"fmt"
	"github.com/alist-org/alist/v3/drivers/base"
    "github.com/alist-org/alist/v3/internal/conf"
	"github.com/alist-org/alist/v3/internal/driver"
	"github.com/alist-org/alist/v3/internal/errs"
	"github.com/alist-org/alist/v3/internal/model"
	"github.com/alist-org/alist/v3/server/common"
	"github.com/alist-org/alist/v3/pkg/cron"
)

type AListV2 struct {
	model.Storage
	Addition
	cron2	    *cron.Cron
	DownloadUrl_dict map[string]string
}

func (d *AListV2) Config() driver.Config {
	return config
}

func (d *AListV2) GetAddition() driver.Additional {
	return &d.Addition
}

func (d *AListV2) Init(ctx context.Context) error {
	if len(d.Addition.Address) > 0 && string(d.Addition.Address[len(d.Addition.Address)-1]) == "/" {
		d.Addition.Address = d.Addition.Address[0 : len(d.Addition.Address)-1]
	}
	
	var downloadurlmap map[string]string
	downloadurlmap = make(map[string]string)
	d.DownloadUrl_dict = downloadurlmap
	d.cron2 = cron.NewCron(time.Hour * 3)
    d.cron2.Do(func() {
		if len(d.DownloadUrl_dict) > 0{
			fmt.Println(time.Now().Format("01-02-2006 15:04:05")," Alist_V2清空缓存下载链接")
			downloadurlmap = make(map[string]string)
			d.DownloadUrl_dict = downloadurlmap
		}
    })
	// TODO login / refresh token
	//op.MustSaveDriverStorage(d)
	return nil
}

func (d *AListV2) Drop(ctx context.Context) error {
	if d.cron2 != nil { d.cron2.Stop() }
	return nil
}

func (d *AListV2) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {
    count := 0
    for {
        url := d.Address + "/api/public/path"
		var resp common.Resp[PathResp]
		_, err := base.RestyClient.R().
			SetResult(&resp).
			SetHeader("Authorization", d.AccessToken).
			SetBody(PathReq{
                PageNum:  0,
                PageSize: 0,
                Path:     dir.GetPath(),
                Password: d.Password,
        }).Post(url)

        if err != nil {
            count += 1
            if count > conf.Conf.Retry_count {
                    return nil, err
            }
        }

        if err == nil {
            var files []model.Obj
			for _, f := range resp.Data.Files {
				file := model.ObjThumb{
					Object: model.Object{
						Name:     f.Name,
						Modified: *f.UpdatedAt,
						Size:     f.Size,
						IsFolder: f.Type == 1,
					},
					Thumbnail: model.Thumbnail{Thumbnail: f.Thumbnail},
				}
				files = append(files, &file)
			}
			return files, nil
        }
    }
}

func (d *AListV2) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
	url := d.Address + "/api/public/path"
	var resp common.Resp[PathResp]
	
	file_id := file.GetPath()
	file_name := file.GetPath()
	
	existed_download_url, ok := d.DownloadUrl_dict[file_id]
	if ok {
		fmt.Println(time.Now().Format("01-02-2006 15:04:05"),"Alist_V2下载链接已存在: ",file_name)
		return &model.Link{URL: existed_download_url,}, nil
	}

	count := 1
    for {
		_, err := base.RestyClient.R().
		SetResult(&resp).
		SetHeader("Authorization", d.AccessToken).
		SetBody(PathReq{
			PageNum:  0,
			PageSize: 0,
			Path:     file.GetPath(),
			Password: d.Password,
		}).Post(url)
		
        if err != nil {
            if count > 2 {
                  return &model.Link{URL: "http://img.xiaoya.pro/abnormal.png",}, err
            }
			fmt.Println("Alist_V2获取下载链接失败第",count,"次 ",file_name)
            count += 1
            time.Sleep(1 * 1000 * time.Millisecond)
        }

        if err == nil {
            d.DownloadUrl_dict[file_id] = resp.Data.Files[0].Url
			fmt.Println(time.Now().Format("01-02-2006 15:04:05"),"文件: ",file_name,"  新增下载直链: ", d.DownloadUrl_dict[file_id])
			fmt.Println(time.Now().Format("01-02-2006 15:04:05"),"Alist_V2 已成功缓存了",len(d.DownloadUrl_dict),"个文件")
			return &model.Link{URL: resp.Data.Files[0].Url,}, nil
		}	
    }		
}

func (d *AListV2) MakeDir(ctx context.Context, parentDir model.Obj, dirName string) error {
	return errs.NotImplement
}

func (d *AListV2) Move(ctx context.Context, srcObj, dstDir model.Obj) error {
	return errs.NotImplement
}

func (d *AListV2) Rename(ctx context.Context, srcObj model.Obj, newName string) error {
	return errs.NotImplement
}

func (d *AListV2) Copy(ctx context.Context, srcObj, dstDir model.Obj) error {
	return errs.NotImplement
}

func (d *AListV2) Remove(ctx context.Context, obj model.Obj) error {
	return errs.NotImplement
}

func (d *AListV2) Put(ctx context.Context, dstDir model.Obj, stream model.FileStreamer, up driver.UpdateProgress) error {
	return errs.NotImplement
}

//func (d *AList) Other(ctx context.Context, args model.OtherArgs) (interface{}, error) {
//	return nil, errs.NotSupport
//}

var _ driver.Driver = (*AListV2)(nil)
