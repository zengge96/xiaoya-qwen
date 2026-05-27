package official_115

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/alist-org/alist/v3/internal/driver"
	"github.com/alist-org/alist/v3/internal/model"
	"github.com/alist-org/alist/v3/pkg/http_range"
	"github.com/alist-org/alist/v3/pkg/utils"
	driver115 "github.com/xiaoyaliu00/115driver/pkg/driver"
	"github.com/pkg/errors"
	"golang.org/x/time/rate"
)

type Pan115 struct {
	model.Storage
	Addition
	client     *driver115.Pan115Client
	limiter    *rate.Limiter
	appVerOnce sync.Once
}

func (d *Pan115) Config() driver.Config {
	return config
}

func (d *Pan115) GetAddition() driver.Additional {
	return &d.Addition
}

func (d *Pan115) Init(ctx context.Context) error {
	d.appVerOnce.Do(d.initAppVer)
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
		return nil, err
	}
	return utils.SliceConvert(files, func(src FileObj) (model.Obj, error) {
		return &src, nil
	})
}

func (d *Pan115) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
	if err := d.WaitLimit(ctx); err != nil {
		return nil, err
	}
	userAgent := args.Header.Get("User-Agent")
	downloadInfo, err := d.client.DownloadWithUA(file.(*FileObj).PickCode, userAgent)
	if err != nil {
		return nil, err
	}
	link := &model.Link{
		URL:    downloadInfo.Url.Url,
		Header: downloadInfo.Header,
	}
	return link, nil
}

func (d *Pan115) MakeDir(ctx context.Context, parentDir model.Obj, dirName string) (model.Obj, error) {
	if err := d.WaitLimit(ctx); err != nil {
		return nil, err
	}

	result := driver115.MkdirResp{}
	form := map[string]string{
		"pid":   parentDir.GetID(),
		"cname": dirName,
	}
	req := d.client.NewRequest().
		SetFormData(form).
		SetResult(&result).
		ForceContentType("application/json;charset=UTF-8")

	resp, err := req.Post(driver115.ApiDirAdd)

	err = driver115.CheckErr(err, &result, resp)
	if err != nil {
		return nil, err
	}
	f, err := d.getNewFile(result.FileID)
	if err != nil {
		return nil, nil
	}
	return f, nil
}

func (d *Pan115) Move(ctx context.Context, srcObj, dstDir model.Obj) (model.Obj, error) {
	if err := d.WaitLimit(ctx); err != nil {
		return nil, err
	}
	if err := d.client.Move(dstDir.GetID(), srcObj.GetID()); err != nil {
		return nil, err
	}
	f, err := d.getNewFile(srcObj.GetID())
	if err != nil {
		return nil, nil
	}
	return f, nil
}

func (d *Pan115) Rename(ctx context.Context, srcObj model.Obj, newName string) (model.Obj, error) {
	if err := d.WaitLimit(ctx); err != nil {
		return nil, err
	}
	if err := d.client.Rename(srcObj.GetID(), newName); err != nil {
		return nil, err
	}
	f, err := d.getNewFile((srcObj.GetID()))
	if err != nil {
		return nil, nil
	}
	return f, nil
}

func (d *Pan115) Copy(ctx context.Context, srcObj, dstDir model.Obj) error {
	if err := d.WaitLimit(ctx); err != nil {
		return err
	}
	return d.client.Copy(dstDir.GetID(), srcObj.GetID())
}

func (d *Pan115) Remove(ctx context.Context, obj model.Obj) error {
	if err := d.WaitLimit(ctx); err != nil {
		return err
	}
	return d.client.Delete(obj.GetID())
}

func (d *Pan115) Put(ctx context.Context, dstDir model.Obj, stream FileStreamer, up driver.UpdateProgress) (model.Obj, error) {
	if err := d.WaitLimit(ctx); err != nil {
		return nil, err
	}

	var (
		fastInfo *driver115.UploadInitResp
		dirID    = dstDir.GetID()
		fileName = stream.GetName()
	)

	if ok, err := d.client.UploadAvailable(); err != nil || !ok {
		fmt.Printf("[ali2115] 文件 %s 上传检查失败: ok=%v, err=%v\n", fileName, ok, err)
		return nil, err
	}

	if stream.GetSize() > d.client.UploadMetaInfo.SizeLimit {
		fmt.Printf("[ali2115] 文件 %s 大小 %d 超过限制 %d\n", fileName, stream.GetSize(), d.client.UploadMetaInfo.SizeLimit)
		return nil, driver115.ErrUploadTooLarge
	}

	const PreHashSize int64 = 128 * 1024
	hashSize := PreHashSize
	if stream.GetSize() < PreHashSize {
		hashSize = stream.GetSize()
	}

	reader, err := stream.RangeRead(http_range.Range{Start: 0, Length: hashSize})
	if err != nil {
		fmt.Printf("[ali2115] 文件 %s 从阿里直链读取数据失败: %v\n", fileName, err)
		return nil, err
	}
	preHash, err := hashReader(utils.SHA1, reader)
	if err != nil {
		fmt.Printf("[ali2115] 文件 %s 从阿里直链计算预哈希失败: %v\n", fileName, err)
		return nil, err
	}
	preHash = strings.ToUpper(preHash)

	hashInfo := stream.GetHash()
	var fullHash string
	fullHash = strings.ToUpper(hashInfo.GetHash(utils.SHA1))
	if fullHash == "" {
		fmt.Printf("[ali2115] 文件 %s 不支持的哈希类型\n", fileName)
		return nil, fmt.Errorf("[ali2115] 文件 %s 不支持的哈希类型", fileName)
	}

	if fastInfo, err = d.rapidUpload(stream.GetSize(), fileName, dirID, preHash, fullHash, stream); err != nil {
		fmt.Printf("[ali2115] 文件 %s rapidUpload 失败: %v\n", fileName, err)
		return nil, err
	}

	if matched, err := fastInfo.Ok(); err != nil {
		fmt.Printf("[ali2115] 文件 %s 检查匹配状态失败: %v\n", fileName, err)
		return nil, err
	} else if matched {
		f, err := d.getNewFileByPickCode(fastInfo.PickCode)
		if err != nil {
			fmt.Printf("[ali2115] 文件 %s 获取秒传后的文件失败: %v\n", fileName, err)
			return nil, err
		}
		return f, nil
	}

	fmt.Printf("[ali2115] 文件 %s 秒传未匹配或处理结束\n", fileName)
	return nil, fmt.Errorf("[ali2115] 文件 %s 秒传未匹配或处理结束", fileName)
}

func (d *Pan115) OfflineList(ctx context.Context) ([]*driver115.OfflineTask, error) {
	resp, err := d.client.ListOfflineTask(0)
	if err != nil {
		return nil, err
	}
	return resp.Tasks, nil
}

func (d *Pan115) OfflineDownload(ctx context.Context, uris []string, dstDir model.Obj) ([]string, error) {
	return d.client.AddOfflineTaskURIs(uris, dstDir.GetID(), driver115.WithAppVer(appVer))
}

func (d *Pan115) DeleteOfflineTasks(ctx context.Context, hashes []string, deleteFiles bool) error {
	return d.client.DeleteOfflineTasks(hashes, deleteFiles)
}

// GetDriverClient returns the underlying driver115 client.
func (d *Pan115) GetDriverClient() *driver115.Pan115Client {
	return d.client
}

// SetClient injects an external Pan115Client (used by alishare_115).
func (d *Pan115) SetClient(c *driver115.Pan115Client) {
	d.client = c
}

var _ driver.Driver = (*Pan115)(nil)
