package alist_v3

import (
	"context"
	"io"
	"path"
	"strconv"
	"strings"
	"time"
	"fmt"

	"github.com/alist-org/alist/v3/drivers/base"
    "github.com/alist-org/alist/v3/internal/conf"
	"github.com/alist-org/alist/v3/internal/driver"
	"github.com/alist-org/alist/v3/internal/model"
	"github.com/alist-org/alist/v3/server/common"
	"github.com/alist-org/alist/v3/pkg/cron"
)

type AListV3 struct {
	model.Storage
	Addition
	cron2	    *cron.Cron
	DownloadUrl_dict map[string]string	
}

func (d *AListV3) Config() driver.Config {
	return config
}

func (d *AListV3) GetAddition() driver.Additional {
	return &d.Addition
}

func (d *AListV3) Init(ctx context.Context) error {
	d.Addition.Address = strings.TrimSuffix(d.Addition.Address, "/")

	var downloadurlmap map[string]string
	downloadurlmap = make(map[string]string)
	d.DownloadUrl_dict = downloadurlmap
	d.cron2 = cron.NewCron(time.Hour * 3)
    d.cron2.Do(func() {
		if len(d.DownloadUrl_dict) > 0{
			fmt.Println(time.Now().Format("01-02-2006 15:04:05")," Alist_V3清空缓存下载链接")
			downloadurlmap = make(map[string]string)
			d.DownloadUrl_dict = downloadurlmap
		}
    })
	// TODO login / refresh token
	//op.MustSaveDriverStorage(d)
	return nil
}

func (d *AListV3) Drop(ctx context.Context) error {
	if d.cron2 != nil { d.cron2.Stop() }
	return nil
}

func (d *AListV3) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {
    count := 0
    for {
        url := d.Address + "/api/fs/list"
		var resp common.Resp[FsListResp]
		_, err := base.RestyClient.R().
			SetResult(&resp).
			SetHeader("Authorization", d.AccessToken).
			SetBody(ListReq{
				PageReq: model.PageReq{
					Page:    1,
					PerPage: 0,
				},
				Path:     dir.GetPath(),
				Password: d.Password,
				Refresh:  false,
			}).Post(url)

        if err != nil {
            count += 1
            if count > conf.Conf.Retry_count {
                    return nil, err
            }
        }

        if err == nil {
            var files []model.Obj
			for _, f := range resp.Data.Content {
				file := model.ObjThumb{
					Object: model.Object{
						Name:     f.Name,
						Modified: f.Modified,
						Size:     f.Size,
						IsFolder: f.IsDir,
					},
					Thumbnail: model.Thumbnail{Thumbnail: f.Thumb},
				}		
				files = append(files, &file)
			}
			return files, nil
        }
    }
}

func (d *AListV3) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
	url := d.Address + "/api/fs/get"
	var resp common.Resp[FsGetResp]
	var oriUa = args.Header.Get("X-Alist-OriUA")
	userAgent := oriUa
	// if the original UA is empty, then use the UA in the header
	// 1st: X-Alist-OriUA
	// 2nd: user-agent
	// 3rd: default UA
	if oriUa == "" {
		userAgent = args.Header.Get("user-agent")
		if userAgent == "" {
			userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/87.0.4280.88 Safari/537.36"
		}
	}
	
	file_id := file.GetPath()
	file_name := file.GetPath()
	
	existed_download_url, ok := d.DownloadUrl_dict[file_id]
	if ok {
		fmt.Println(time.Now().Format("01-02-2006 15:04:05"),"Alist_V3下载链接已存在: ",file_name)
		return &model.Link{URL: existed_download_url,}, nil
	}

	count := 1
    for {
		_, err := base.RestyClient.R().
		SetResult(&resp).
		SetHeader("Authorization", d.AccessToken).
		SetBody(FsGetReq{
			Path:     file.GetPath(),
			Password: d.Password,
		}).SetHeader("user-agent", userAgent).SetHeader("X-Alist-OriUA", userAgent).Post(url)
		
        if err != nil || resp.Code != 200 {
            if count > 2 {
                  return &model.Link{URL: "http://img.xiaoya.pro/abnormal.png",}, err
            }
			fmt.Println(time.Now().Format("01-02-2006 15:04:05"),"Alist_V3获取下载链接失败第",count,"次 ",file_name)
            count += 1
            time.Sleep(1 * 1000 * time.Millisecond)
        }

        if err == nil {
            d.DownloadUrl_dict[file_id] = resp.Data.RawURL
			fmt.Println("文件: ",file_name,"  新增下载直链: ", d.DownloadUrl_dict[file_id])
			fmt.Println(time.Now().Format("01-02-2006 15:04:05"),"Alist_V2 已成功缓存了",len(d.DownloadUrl_dict),"个文件")
			return &model.Link{URL: resp.Data.RawURL,}, nil
		}	
    }		
}

func (d *AListV3) MakeDir(ctx context.Context, parentDir model.Obj, dirName string) error {
	url := d.Address + "/api/fs/mkdir"
	var resp common.Resp[interface{}]
	_, err := base.RestyClient.R().
		SetResult(&resp).
		SetHeader("Authorization", d.AccessToken).
		SetBody(MkdirOrLinkReq{
			Path: path.Join(parentDir.GetPath(), dirName),
		}).Post(url)
	return checkResp(resp, err)
}

func (d *AListV3) Move(ctx context.Context, srcObj, dstDir model.Obj) error {
	url := d.Address + "/api/fs/move"
	var resp common.Resp[interface{}]
	_, err := base.RestyClient.R().
		SetResult(&resp).
		SetHeader("Authorization", d.AccessToken).
		SetBody(MoveCopyReq{
			SrcDir: path.Dir(srcObj.GetPath()),
			DstDir: dstDir.GetPath(),
			Names:  []string{srcObj.GetName()},
		}).Post(url)
	return checkResp(resp, err)
}

func (d *AListV3) Rename(ctx context.Context, srcObj model.Obj, newName string) error {
	url := d.Address + "/api/fs/rename"
	var resp common.Resp[interface{}]
	_, err := base.RestyClient.R().
		SetResult(&resp).
		SetHeader("Authorization", d.AccessToken).
		SetBody(RenameReq{
			Path: srcObj.GetPath(),
			Name: newName,
		}).Post(url)
	return checkResp(resp, err)
}

func (d *AListV3) Copy(ctx context.Context, srcObj, dstDir model.Obj) error {
	url := d.Address + "/api/fs/copy"
	var resp common.Resp[interface{}]
	_, err := base.RestyClient.R().
		SetResult(&resp).
		SetHeader("Authorization", d.AccessToken).
		SetBody(MoveCopyReq{
			SrcDir: path.Dir(srcObj.GetPath()),
			DstDir: dstDir.GetPath(),
			Names:  []string{srcObj.GetName()},
		}).Post(url)
	return checkResp(resp, err)
}

func (d *AListV3) Remove(ctx context.Context, obj model.Obj) error {
	url := d.Address + "/api/fs/remove"
	var resp common.Resp[interface{}]
	_, err := base.RestyClient.R().
		SetResult(&resp).
		SetHeader("Authorization", d.AccessToken).
		SetBody(RemoveReq{
			Dir:   path.Dir(obj.GetPath()),
			Names: []string{obj.GetName()},
		}).Post(url)
	return checkResp(resp, err)
}

func (d *AListV3) Put(ctx context.Context, dstDir model.Obj, stream model.FileStreamer, up driver.UpdateProgress) error {
	url := d.Address + "/api/fs/put"
	var resp common.Resp[interface{}]
	fileBytes, err := io.ReadAll(stream.GetReadCloser())
	if err != nil {
		return nil
	}
	_, err = base.RestyClient.R().SetContext(ctx).
		SetResult(&resp).
		SetHeader("Authorization", d.AccessToken).
		SetHeader("File-Path", path.Join(dstDir.GetPath(), stream.GetName())).
		SetHeader("Password", d.Password).
		SetHeader("Content-Length", strconv.FormatInt(stream.GetSize(), 10)).
		SetBody(fileBytes).Put(url)
	return checkResp(resp, err)
}

//func (d *AList) Other(ctx context.Context, args model.OtherArgs) (interface{}, error) {
//	return nil, errs.NotSupport
//}

var _ driver.Driver = (*AListV3)(nil)
