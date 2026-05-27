package alishare_115

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"sync"

	"github.com/alist-org/alist/v3/drivers/aliyundrive_share2open"
	"github.com/alist-org/alist/v3/drivers/official_115"
	"github.com/alist-org/alist/v3/internal/driver"
	"github.com/alist-org/alist/v3/internal/model"
	driver115 "github.com/xiaoyaliu00/115driver/pkg/driver"
	log "github.com/sirupsen/logrus"
)

type AliShare115 struct {
	model.Storage
	Addition

	aliShare *aliyundrive_share2open.AliyundriveShare2Open
	pan115   *official_115.Pan115

	// 115 直链缓存: file_id → 115 download URL
	cMu   sync.RWMutex
	cache map[string]string
}

func (d *AliShare115) Config() driver.Config {
	return config
}

func (d *AliShare115) GetAddition() driver.Additional {
	return &d.Addition
}

func (d *AliShare115) Init(ctx context.Context) error {
	// Init aliShare（处理 token / 列表 / 直链）
	d.aliShare = &aliyundrive_share2open.AliyundriveShare2Open{}
	d.aliShare.Addition = d.Addition.Addition
	if err := d.aliShare.Init(ctx); err != nil {
		return fmt.Errorf("aliShare init: %w", err)
	}

	// Init internal Pan115 client
	official_115.InitAppVerOnce()
	d.pan115 = &official_115.Pan115{}
	if d.Cookie115 != "" {
		client, err := newPan115Client(d.Cookie115)
		if err != nil {
			return fmt.Errorf("115 client: %w", err)
		}
		d.pan115.SetClient(client)
		log.Infof("alishare_115: 115 client initialized")
	}

	d.cache = make(map[string]string)
	return nil
}

func (d *AliShare115) Drop(ctx context.Context) error {
	return nil
}

// List 直接委托给 aliShare
func (d *AliShare115) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {
	return d.aliShare.List(ctx, dir, args)
}

// Link 获取直链：优先走 115 秒传，失败时回落 aliShare 的直链
func (d *AliShare115) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
	// aliShare.Link 内部会填充 Hash_dict 和 DownloadUrl_dict
	aliLink, err := d.aliShare.Link(ctx, file, args)
	if err != nil {
		return nil, err
	}

	// SHA1 秒传需要
	sha1Str := d.aliShare.Hash_dict[file.GetID()]

	// 尝试通过 115 秒传获取直链
	if link115, err := d.try115Link(ctx, file, aliLink.URL, sha1Str, args); err == nil {
		return link115, nil
	}

	// 115 失败，回落阿里云盘直链
	log.Warnf("alishare_115: 115 transfer failed for %s, fallback", file.GetName())
	return aliLink, nil
}

// Other 直接委托给 aliShare
func (d *AliShare115) Other(ctx context.Context, args model.OtherArgs) (interface{}, error) {
	return d.aliShare.Other(ctx, args)
}

// newPan115Client 从 cookie 创建已登录的 Pan115Client
func newPan115Client(cookie string) (*driver115.Pan115Client, error) {
	opts := []driver115.Option{
		driver115.UA("Mozilla/5.0 115Browser/" + official_115.GetAppVer()),
		driver115.WithClient(&http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		}),
	}
	client := driver115.New(opts...)
	cr := &driver115.Credential{}
	if err := cr.FromCookie(cookie); err != nil {
		return nil, fmt.Errorf("parse 115 cookie: %w", err)
	}
	client.ImportCredential(cr)
	if err := client.LoginCheck(); err != nil {
		return nil, fmt.Errorf("115 login check: %w", err)
	}
	return client, nil
}

// try115Link 尝试从 115 获取直链
func (d *AliShare115) try115Link(ctx context.Context, file model.Obj, aliyunUrl, sha1Str string, args model.LinkArgs) (*model.Link, error) {
	fileId := file.GetID()

	if d.pan115 == nil || d.pan115.GetDriverClient() == nil {
		return nil, fmt.Errorf("115 client not available")
	}

	// 缓存命中
	d.cMu.RLock()
	if url, ok := d.cache[fileId]; ok {
		d.cMu.RUnlock()
		return &model.Link{URL: url}, nil
	}
	d.cMu.RUnlock()

	// 空 hash 无法秒传
	if sha1Str == "" {
		return nil, fmt.Errorf("SHA1 empty, cannot rapid upload")
	}

	// 复用 official_115 的 urlFileStreamer 创建虚拟文件
	stream := official_115.NewUrlFileStreamer(file.GetName(), file.GetSize(), sha1Str, aliyunUrl)

	dstDir := &model.Object{ID: d.TargetFolder115}

	// 秒传到 115
	obj, err := d.pan115.Put(ctx, dstDir, stream, nil)
	if err != nil {
		return nil, fmt.Errorf("115 Put: %w", err)
	}

	// 获取 115 直链
	link, err := d.pan115.Link(ctx, obj, args)
	if err != nil {
		return nil, fmt.Errorf("115 Link: %w", err)
	}

	// 缓存
	d.cMu.Lock()
	d.cache[fileId] = link.URL
	d.cMu.Unlock()

	log.Infof("alishare_115: cached 115 link for %s", file.GetName())
	return link, nil
}

// ============ 编译时接口检查 ============
var _ driver.Driver = (*AliShare115)(nil)