package alishare_115

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/alist-org/alist/v3/drivers/aliyundrive_share2open"
	"github.com/alist-org/alist/v3/drivers/official_115"
	"github.com/alist-org/alist/v3/internal/conf"
	"github.com/alist-org/alist/v3/internal/driver"
	"github.com/alist-org/alist/v3/internal/model"
	"github.com/pkg/errors"
	"golang.org/x/time/rate"

	driver115 "github.com/xiaoyaliu00/115driver/pkg/driver"
)

type AliShare115 struct {
	model.Storage
	Addition

	aliShare *aliyundrive_share2open.AliyundriveShare2Open
	client   *driver115.Pan115Client
	limiter  *rate.Limiter

	// file_id → 115 下载直链缓存
	link115Cache map[string]string
}

func (d *AliShare115) Config() driver.Config {
	return config
}

func (d *AliShare115) GetAddition() driver.Additional {
	return &d.Addition
}

func (d *AliShare115) Init(ctx context.Context) error {
	d.link115Cache = make(map[string]string)

	// ── 初始化阿里云盘分享驱动 ──
	d.aliShare = &aliyundrive_share2open.AliyundriveShare2Open{}
	d.aliShare.Storage = d.Storage
	d.aliShare.Addition = aliyundrive_share2open.Addition{
		RefreshToken:         d.RefreshToken,
		RefreshTokenOpen:     d.RefreshTokenOpen,
		TempTransferFolderID: d.TempTransferFolderID,
		ShareId:              d.ShareId,
		SharePwd:             d.SharePwd,
		RootID:               d.RootID,
		OrderBy:              d.OrderBy,
		OrderDirection:       d.OrderDirection,
		OauthTokenURL:        d.OauthTokenURL,
		ClientID:             d.ClientID,
		ClientSecret:         d.ClientSecret,
	}
	if err := d.aliShare.Init(ctx); err != nil {
		return fmt.Errorf("init aliyun share failed: %w", err)
	}

	// ── 初始化 115 客户端 ──
	if err := d.init115Client(); err != nil {
		return fmt.Errorf("init 115 client failed: %w", err)
	}

	if d.LimitRate115 > 0 {
		d.limiter = rate.NewLimiter(rate.Limit(d.LimitRate115), 1)
	}

	fmt.Printf("[alishare_115] 初始化完成, 115 客户端已就绪\n")
	return nil
}

// init115Client 内部初始化 115 客户端（不注册存储，纯客户端模式）
func (d *AliShare115) init115Client() error {
	if d.Cookie115 == "" && d.QRCodeToken115 == "" {
		return errors.New("115 cookie 或 qrcode_token 必须提供其一")
	}

	// 先获取最新 appVer
	official_115.InitAppVerOnce()

	ua := fmt.Sprintf("Mozilla/5.0 115Browser/%s", official_115.GetAppVer())
	opts := []driver115.Option{
		driver115.UA(ua),
		func(c *driver115.Pan115Client) {
			c.Client.SetTLSClientConfig(&tls.Config{InsecureSkipVerify: conf.Conf.TlsInsecureSkipVerify})
		},
	}
	d.client = driver115.New(opts...)

	cr := &driver115.Credential{}
	var err error

	if d.QRCodeToken115 != "" {
		s := &driver115.QRCodeSession{UID: d.QRCodeToken115}
		if cr, err = d.client.QRCodeLoginWithApp(s, driver115.LoginApp(d.QRCodeSource115)); err != nil {
			return errors.Wrap(err, "115 QR code login failed")
		}
		// 保存解析出的 cookie（不写入数据库，仅本实例使用）
		d.Cookie115 = fmt.Sprintf("UID=%s;CID=%s;SEID=%s;KID=%s", cr.UID, cr.CID, cr.SEID, cr.KID)
		d.QRCodeToken115 = ""
	} else if d.Cookie115 != "" {
		if err = cr.FromCookie(d.Cookie115); err != nil {
			return errors.Wrap(err, "115 cookie parse failed")
		}
		d.client.ImportCredential(cr)
	}

	return d.client.LoginCheck()
}

func (d *AliShare115) waitLimit(ctx context.Context) error {
	if d.limiter != nil {
		return d.limiter.Wait(ctx)
	}
	return nil
}

func (d *AliShare115) Drop(ctx context.Context) error {
	if d.aliShare != nil {
		return d.aliShare.Drop(ctx)
	}
	return nil
}

// ── List 直接委托给阿里云盘分享驱动 ──

func (d *AliShare115) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {
	return d.aliShare.List(ctx, dir, args)
}

// ── Link: 核心逻辑 —— 阿里云转存 → 115秒传 → 115直链 ──

func (d *AliShare115) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
	fileId := file.GetID()
	fileName := file.GetName()

	// 1. 检查缓存
	if link, ok := d.link115Cache[fileId]; ok {
		fmt.Printf("[alishare_115] 命中115缓存: %s\n", fileName)
		return d.make115Link(link)
	}

	// 2. 获取阿里云直链（同时会填充 Hash_dict）
	aliLink, err := d.aliShare.Link(ctx, file, args)
	if err != nil {
		return nil, fmt.Errorf("获取阿里云直链失败: %w", err)
	}

	// 3. 获取 SHA1 hash
	sha1Hash := d.aliShare.GetHash(ctx, file, args)
	if sha1Hash == "" {
		fmt.Printf("[alishare_115] 获取 SHA1 失败，回退阿里云直链: %s\n", fileName)
		return aliLink, nil
	}
	sha1Hash = strings.ToUpper(sha1Hash)

	// 4. 构造 urlFileStreamer，用于115秒传
	fileSize := file.GetSize()
	stream := official_115.NewUrlFileStreamer(fileName, fileSize, sha1Hash, aliLink.URL)

	// 5. 限速等待
	if err := d.waitLimit(ctx); err != nil {
		return aliLink, nil
	}

	// 6. 秒传到115（构造临时 Pan115 以复用其 Put 逻辑）
	pan := &official_115.Pan115{}
	pan.SetClient(d.client)
	targetDir := &model.Object{ID: d.TargetFolder115, IsFolder: true}
	putResult, err := pan.Put(ctx, targetDir, stream, nil)
	if err != nil {
		fmt.Printf("[alishare_115] 115秒传失败，回退阿里云直链: %s, err: %v\n", fileName, err)
		return aliLink, nil
	}

	// 7. 从115获取下载直链
	fileObj115, ok := putResult.(*official_115.FileObj)
	if !ok {
		fmt.Printf("[alishare_115] 115返回类型异常，回退阿里云直链: %s\n", fileName)
		return aliLink, nil
	}

	dlInfo, err := d.client.DownloadWithUA(fileObj115.PickCode, args.Header.Get("User-Agent"))
	if err != nil {
		fmt.Printf("[alishare_115] 获取115直链失败，回退阿里云直链: %s, err: %v\n", fileName, err)
		return aliLink, nil
	}

	link115 := dlInfo.Url.Url
	fmt.Printf("[alishare_115] ✅ 115直链获取成功: %s\n", fileName)

	// 8. 缓存
	d.link115Cache[fileId] = link115

	return d.make115Link(link115)
}

func (d *AliShare115) make115Link(url string) (*model.Link, error) {
	return &model.Link{
		URL: url,
		Header: http.Header{
			"User-Agent": []string{driver115.UA115Browser},
		},
	}, nil
}

// ── Other: 视频预览等，委托给阿里云盘 ──

func (d *AliShare115) Other(ctx context.Context, args model.OtherArgs) (interface{}, error) {
	return d.aliShare.Other(ctx, args)
}

// ── GetHash: 返回文件的 SHA1 ──

func (d *AliShare115) GetHash(ctx context.Context, file model.Obj, args model.LinkArgs) string {
	return d.aliShare.GetHash(ctx, file, args)
}

// purge115Cache 清理115链接缓存
func (d *AliShare115) purge115Cache() {
	if len(d.link115Cache) > 0 {
		fmt.Printf("[alishare_115] %s 清空115链接缓存 (%d 条): %s\n",
			time.Now().Format("2006-01-02 15:04:05"), len(d.link115Cache), d.MountPath)
		d.link115Cache = make(map[string]string)
	}
}

var _ driver.Driver = (*AliShare115)(nil)
