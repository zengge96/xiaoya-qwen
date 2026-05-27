package alishare_pan115

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	aliyundrive_share2open "github.com/alist-org/alist/v3/drivers/aliyundrive_share2open"
	official_115 "github.com/alist-org/alist/v3/drivers/official_115"
	"github.com/alist-org/alist/v3/internal/driver"
	"github.com/alist-org/alist/v3/internal/model"
	"github.com/alist-org/alist/v3/internal/op"
)

var config = driver.Config{
	Name:        "AliyundriveShare2Pan115",
	LocalSort:   false,
	OnlyProxy:   false,
	NoUpload:    true,
	OnlyLocal:   false,
	NoCache:     false,
	NeedMs:      false,
	DefaultRoot: "root",
}

type Addition struct {
	aliyundrive_share2open.Addition
	Cookie string `json:"cookie" type:"text" required:"true" help:"115 cookie required"`
	DirId  string `json:"dir_id" type:"text" default:"0" help:"115 temp dir id (set to 'auto' to auto create/find '最近接收' dir)"`
	//本应定义成bool，但是小雅脚本导入数据库是字符串
	PurgePan115Temp string `json:"purge_pan115_temp" type:"select" options:"true,false" default:"false"`
}

type AliyundriveShare2Pan115 struct {
	Addition
	aliCore           aliyundrive_share2open.AliyundriveShare2Open
	pan115            *official_115.Pan115
	linkCache         map[string]*cacheEntry
	pan115Initialized bool

	// 真实保存的数据库对象，防止底层核心篡改
	storage model.Storage
}

// cacheEntry holds the link and the time it was cached
type cacheEntry struct {
	link      *model.Link
	timestamp time.Time
	ttl       time.Duration
}

func (d *AliyundriveShare2Pan115) Config() driver.Config {
	return config
}
func (d *AliyundriveShare2Pan115) GetAddition() driver.Additional {
	return &d.Addition
}

func (d *AliyundriveShare2Pan115) syncDB(ctx context.Context) {
	oldAliJSON, _ := json.Marshal(d.Addition.Addition)
	newAliJSON, _ := json.Marshal(d.aliCore.Addition)

	if string(oldAliJSON) != string(newAliJSON) {
		d.Addition.Addition = d.aliCore.Addition
		op.MustSaveDriverStorage(d)
	}
}

func (d *AliyundriveShare2Pan115) Init(ctx context.Context) error {
	d.aliCore.Addition = d.Addition.Addition

	defer d.syncDB(ctx)

	if err := d.aliCore.Init(ctx); err != nil {
		return err
	}

	// Initialize pan115 driver
	d.pan115 = &official_115.Pan115{
		Addition: official_115.Addition{
			Cookie: d.Cookie,
			RootID: driver.RootID{RootFolderID: d.DirId},
		},
	}

	// Initialize link cache
	d.linkCache = make(map[string]*cacheEntry)

	return nil
}

// ensurePan115Init initializes pan115 lazily on first use
func (d *AliyundriveShare2Pan115) ensurePan115Init(ctx context.Context) error {
	if d.pan115 == nil {
		err := fmt.Errorf("[ali2115] 115网盘实例未初始化")
		fmt.Printf("%s %v\n", time.Now().Format("01-02-2006 15:04:05"), err)
		return err
	}
	
	if !d.pan115Initialized {
		isAuto := d.DirId == "auto"
		if isAuto {
			d.pan115.RootID.RootFolderID = "0"
		}

		if err := d.pan115.Init(ctx); err != nil {
			fmt.Printf("[ali2115] %s 115网盘初始化失败: %v\n", time.Now().Format("01-02-2006 15:04:05"), err)
			return err
		}
		d.pan115Initialized = true

		if isAuto {
			entries, err := d.pan115.List(ctx, &dirObj{id: "0"}, model.ListArgs{})
			if err != nil {
				finalErr := fmt.Errorf("[ali2115] 无法自动获取DirId(读取根目录失败)，请手工配置: %v", err)
				fmt.Printf("%s %v\n", time.Now().Format("01-02-2006 15:04:05"), finalErr)
				return finalErr
			}

			var targetId string
			for _, entry := range entries {
				if entry.IsDir() && entry.GetName() == "最近接收" {
					targetId = entry.GetID()
					break
				}
			}

			if targetId == "" {
				newDir, err := d.pan115.MakeDir(ctx, &dirObj{id: "0"}, "最近接收")
				if err != nil {
					finalErr := fmt.Errorf("[ali2115] 无法自动获取DirId(创建'最近接收'目录失败)，请手工配置: %v", err)
					fmt.Printf("%s %v\n", time.Now().Format("01-02-2006 15:04:05"), finalErr)
					return finalErr
				}
				
				if newDir != nil && newDir.GetID() != "" {
					targetId = newDir.GetID()
				} else {
					entries, _ := d.pan115.List(ctx, &dirObj{id: "0"}, model.ListArgs{})
					for _, entry := range entries {
						if entry.IsDir() && entry.GetName() == "最近接收" {
							targetId = entry.GetID()
							break
						}
					}
				}
			}

			if targetId == "" {
				finalErr := fmt.Errorf("[ali2115] 无法自动获取DirId(最终未找到目标目录)，请手工配置")
				fmt.Printf("%s %v\n", time.Now().Format("01-02-2006 15:04:05"), finalErr)
				return finalErr
			}

			// 更新当前对象属性
			d.DirId = targetId
			d.pan115.RootID.RootFolderID = targetId

			// 保存配置至数据库，以后无需再次执行 auto 流程
			op.MustSaveDriverStorage(d)
			fmt.Printf("[ali2115] %s 自动获取/创建目录'最近接收'成功，DirId已被替换为: %s\n", time.Now().Format("01-02-2006 15:04:05"), targetId)
		}
	}
	return nil
}

func (d *AliyundriveShare2Pan115) Drop(ctx context.Context) error {
	if d.pan115 != nil {
		d.pan115.Drop(ctx)
	}
	return d.aliCore.Drop(ctx)
}

func (d *AliyundriveShare2Pan115) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {
	defer d.syncDB(ctx)
	return d.aliCore.List(ctx, dir, args)
}

func (d *AliyundriveShare2Pan115) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
	defer d.syncDB(ctx)

	fileId := file.GetID()
	ua := args.Header.Get("User-Agent")
	cacheKey := fileId + "+" + ua
	fileName := file.GetName()

	if cachedEntry, ok := d.linkCache[cacheKey]; ok {
		ttl := cachedEntry.ttl
		if ttl == 0 {
			ttl = time.Minute * 60
		}

		if time.Since(cachedEntry.timestamp) < ttl {
			linkType := "115直链"
			if ttl == time.Minute*1 {
				linkType = "阿里直链(回退)"
			}
			fmt.Printf("[ali2115] %s %s缓存命中: %s -> %s\n",
				time.Now().Format("01-02-2006 15:04:05"), linkType, fileName, cachedEntry.link.URL)
			return cachedEntry.link, nil
		}
		delete(d.linkCache, cacheKey)
	}

	aliLink, err := d.aliCore.Link(ctx, file, args)
	if err != nil {
		return nil, err
	}

	fileHash := d.aliCore.GetHash(ctx, file, args)
	fileSize := file.GetSize()

	handleFallback := func(errMsg string, failErr error) (*model.Link, error) {
		fmt.Printf("%s: %v\n", errMsg, failErr)
		fmt.Printf("[ali2115] %s 触发回退机制，返回阿里直链: %s\n",
			time.Now().Format("01-02-2006 15:04:05"), fileName)

		d.linkCache[cacheKey] = &cacheEntry{
			link:      aliLink,
			timestamp: time.Now(),
			ttl:       time.Minute * 1,
		}
		return aliLink, nil
	}

	if err := d.ensurePan115Init(ctx); err != nil {
		return handleFallback("[ali2115] 初始化115转存驱动失败", err)
	}

	streamer := official_115.NewUrlFileStreamer(fileName, fileSize, fileHash, aliLink.URL)

	obj, putErr := d.pan115.Put(ctx, &dirObj{id: d.DirId}, streamer, nil)
	if putErr != nil {
		return handleFallback("[ali2115] 秒传到115失败", putErr)
	}

	defer func() {
		if putErr == nil && obj != nil && d.PurgePan115Temp == "true" {
			d.pan115.Remove(ctx, obj)
		}
	}()

	pan115Link, linkErr := d.pan115.Link(ctx, obj, args)
	if linkErr != nil {
		return handleFallback("[ali2115] 秒传到115成功，但是获取直链失败", linkErr)
	}

	if pan115Link != nil {
		fmt.Printf("[ali2115] %s 115直链获取成功: %s -> %s\n",
			time.Now().Format("01-02-2006 15:04:05"), fileName, pan115Link.URL)
	}

	d.linkCache[cacheKey] = &cacheEntry{
		link:      pan115Link,
		timestamp: time.Now(),
		ttl:       time.Minute * 60,
	}

	return pan115Link, nil
}

func (d *AliyundriveShare2Pan115) Other(ctx context.Context, args model.OtherArgs) (interface{}, error) {
	defer d.syncDB(ctx)
	return d.aliCore.Other(ctx, args)
}

func (d *AliyundriveShare2Pan115) GetStorage() *model.Storage {
	return &d.storage
}

func (d *AliyundriveShare2Pan115) SetStorage(s model.Storage) {
	d.storage = s
	fakeStorage := s
	fakeStorage.ID = 12349876
	d.aliCore.SetStorage(fakeStorage)
}

var _ driver.Driver = (*AliyundriveShare2Pan115)(nil)

func init() {
	op.RegisterDriver(func() driver.Driver {
		return &AliyundriveShare2Pan115{}
	})
}

// dirObj implements model.Obj for passing to Put (as dstDir)
type dirObj struct {
	id string
}

func (d *dirObj) GetID() string      { return d.id }
func (d *dirObj) GetName() string    { return "" }
func (d *dirObj) GetSize() int64     { return 0 }
func (d *dirObj) ModTime() time.Time { return time.Time{} }
func (d *dirObj) IsDir() bool        { return true }
func (d *dirObj) GetPath() string    { return "" }