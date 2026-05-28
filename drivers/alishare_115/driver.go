package alishare_115

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/alist-org/alist/v3/drivers/aliyundrive_share2open"
	"github.com/alist-org/alist/v3/drivers/official_115"
	"github.com/alist-org/alist/v3/internal/driver"
	"github.com/alist-org/alist/v3/internal/model"
	"github.com/alist-org/alist/v3/pkg/cron"
)

type AliShare115 struct {
	model.Storage
	Addition
	ali       *aliyundrive_share2open.AliyundriveShare2Open
	p115      *official_115.Pan115
	cache115  map[string]string
	cronCache *cron.Cron
}

func (d *AliShare115) Config() driver.Config {
	return config
}

func (d *AliShare115) GetAddition() driver.Additional {
	return &d.Addition
}

func (d *AliShare115) Init(ctx context.Context) error {
	// 1. Aliyun driver
	d.ali = &aliyundrive_share2open.AliyundriveShare2Open{}
	d.ali.Addition = aliyundrive_share2open.Addition{
		RefreshToken:         d.RefreshToken,
		RefreshTokenOpen:     d.RefreshTokenOpen,
		ShareId:              d.ShareId,
		SharePwd:             d.SharePwd,
		TempTransferFolderID: d.TempTransferFolderID,
		RootID:               d.RootID,
		OrderBy:              d.OrderBy,
		OrderDirection:       d.OrderDirection,
	}
	if err := d.ali.Init(ctx); err != nil {
		return fmt.Errorf("aliyun: %w", err)
	}

	// 2. 115 driver
	official_115.InitAppVerOnce()
	d.p115 = &official_115.Pan115{
		Addition: official_115.Addition{
			Cookie: d.Cookie,
		},
	}
	if err := d.p115.Init(ctx); err != nil {
		return fmt.Errorf("115: %w", err)
	}

	// 3. Cache
	d.cache115 = make(map[string]string)
	d.cronCache = cron.NewCron(time.Minute * 3)
	d.cronCache.Do(func() {
		fmt.Printf("[alishare_115] clearing cache (%s)\n", d.MountPath)
		d.cache115 = make(map[string]string)
	})

	return nil
}

func (d *AliShare115) Drop(ctx context.Context) error {
	if d.ali != nil {
		d.ali.Drop(ctx)
	}
	if d.p115 != nil {
		d.p115.Drop(ctx)
	}
	if d.cronCache != nil {
		d.cronCache.Stop()
	}
	return nil
}

func (d *AliShare115) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {
	return d.ali.List(ctx, dir, args)
}

func (d *AliShare115) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
	fileID := file.GetID()
	fileName := file.GetName()

	if url, ok := d.cache115[fileID]; ok {
		fmt.Printf("[alishare_115] cache hit: %s\n", fileName)
		return &model.Link{URL: url}, nil
	}

	// Full Aliyun flow: auth → copy → get download URL + SHA1
	aliLink, err := d.ali.Link(ctx, file, args)
	if err != nil {
		return nil, err
	}

	sha1Hash := d.ali.Hash_dict[fileID]
	abnormal := "http://img.xiaoya.pro/abnormal.png"
	if sha1Hash == "" || aliLink.URL == "" || aliLink.URL == abnormal {
		return aliLink, nil
	}

	// SHA1 rapid upload to 115
	targetDir := d.TargetFolder115
	if targetDir == "" {
		targetDir = "0"
	}

	pickCode, err := d.sha1Upload(ctx, file.GetSize(), fileName, sha1Hash, aliLink.URL, targetDir)
	if err != nil {
		fmt.Printf("[alishare_115] upload failed for %s: %v, fallback\n", fileName, err)
		return aliLink, nil
	}

	dl, err := d.p115.GetDriverClient().DownloadWithUA(pickCode, "")
	if err != nil {
		fmt.Printf("[alishare_115] DownloadWithUA failed: %v, fallback\n", err)
		return aliLink, nil
	}

	d.cache115[fileID] = dl.Url.Url
	fmt.Printf("[alishare_115] 115 link ready: %s\n", fileName)
	return &model.Link{
		URL:    dl.Url.Url,
		Header: dl.Header,
	}, nil
}

func (d *AliShare115) Other(ctx context.Context, args model.OtherArgs) (interface{}, error) {
	return d.ali.Other(ctx, args)
}

// --- helpers ---

func (d *AliShare115) sha1Upload(ctx context.Context, fileSize int64, fileName, fullSHA1, aliyunURL, dirID string) (string, error) {
	stream := official_115.NewUrlFileStreamer(fileName, fileSize, fullSHA1, aliyunURL)

	preHash, err := computePreHash(aliyunURL, fileSize)
	if err != nil {
		return "", fmt.Errorf("preHash: %w", err)
	}

	result, err := d.p115.RapidUploadCheck(ctx, fileSize, fileName, dirID, preHash, fullSHA1, stream)
	if err != nil {
		return "", fmt.Errorf("RapidUploadCheck: %w", err)
	}

	ok, err := result.Ok()
	if err != nil {
		return "", fmt.Errorf("Ok: %w", err)
	}
	if !ok {
		return "", fmt.Errorf("not matched (status=%d)", result.Status)
	}

	return result.PickCode, nil
}

func computePreHash(url string, fileSize int64) (string, error) {
	const sz int64 = 128 * 1024
	readSize := sz
	if fileSize > 0 && fileSize < readSize {
		readSize = fileSize
	}
	if readSize <= 0 {
		return "", fmt.Errorf("zero size")
	}

	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Range", fmt.Sprintf("bytes=0-%d", readSize-1))
	req.Header.Set("Referer", "https://www.aliyundrive.com/")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, readSize))
	if err != nil {
		return "", err
	}

	h := sha1.New()
	h.Write(data)
	return strings.ToUpper(hex.EncodeToString(h.Sum(nil))), nil
}

var _ driver.Driver = (*AliShare115)(nil)
