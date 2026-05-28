package alishare_115

import (
	"context"
	"crypto/sha1"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/alist-org/alist/v3/drivers/aliyundrive_share2open"
	"github.com/alist-org/alist/v3/internal/conf"
	"github.com/alist-org/alist/v3/internal/driver"
	"github.com/alist-org/alist/v3/internal/model"
	"github.com/alist-org/alist/v3/pkg/cron"
	driver115 "github.com/xiaoyaliu00/115driver/pkg/driver"
)

type AliShare115 struct {
	model.Storage
	Addition
	ali       *aliyundrive_share2open.AliyundriveShare2Open
	client    *driver115.Pan115Client
	cache115  map[string]string // file_id → 115 download URL
	cronCache *cron.Cron
}

func (d *AliShare115) Config() driver.Config {
	return config
}

func (d *AliShare115) GetAddition() driver.Additional {
	return &d.Addition
}

func (d *AliShare115) Init(ctx context.Context) error {
	// 1. Create and init aliyundrive_share2open instance
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
		return fmt.Errorf("aliyun init failed: %w", err)
	}

	// 2. Login to 115
	if err := d.login115(); err != nil {
		return fmt.Errorf("115 login failed: %w", err)
	}

	// 3. Init 115 URL cache with periodic cleanup
	d.cache115 = make(map[string]string)
	d.cronCache = cron.NewCron(time.Minute * 3)
	d.cronCache.Do(func() {
		fmt.Printf("[alishare_115] clearing 115 URL cache (%s)\n", d.MountPath)
		d.cache115 = make(map[string]string)
	})

	return nil
}

func (d *AliShare115) Drop(ctx context.Context) error {
	if d.ali != nil {
		d.ali.Drop(ctx)
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

	// 115 cache hit
	if url, ok := d.cache115[fileID]; ok {
		fmt.Printf("[alishare_115] 115 cache hit: %s\n", fileName)
		return &model.Link{URL: url}, nil
	}

	// Get Aliyun link — this handles auth, copy, download URL, and SHA1 hash
	// All results are stored in exported fields: FileID_Link[fileID] and Hash_dict[fileID]
	aliLink, err := d.ali.Link(ctx, file, args)
	if err != nil {
		return nil, err
	}

	// Read SHA1 from exported Hash_dict (populated by Link → GetmyLink)
	sha1 := d.ali.Hash_dict[fileID]

	// No SHA1 or abnormal link → return Aliyun link as-is
	abnormal := "http://img.xiaoya.pro/abnormal.png"
	if sha1 == "" || aliLink.URL == "" || aliLink.URL == abnormal {
		return aliLink, nil
	}

	// Try SHA1 rapid upload to 115
	targetDir := d.TargetFolder115
	if targetDir == "" {
		targetDir = "0"
	}

	pickCode, err := d.sha1UploadTo115(ctx, file.GetSize(), fileName, sha1, aliLink.URL, targetDir)
	if err != nil {
		fmt.Printf("[alishare_115] SHA1 upload failed for %s: %v, fallback to Aliyun\n", fileName, err)
		return aliLink, nil
	}

	// Get 115 download URL
	dl, err := d.client.DownloadWithUA(pickCode, "")
	if err != nil {
		fmt.Printf("[alishare_115] DownloadWithUA failed for %s: %v, fallback to Aliyun\n", fileName, err)
		return aliLink, nil
	}

	// Cache and return 115 URL
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

// === 115 helpers ===

func (d *AliShare115) login115() error {
	opts := []driver115.Option{
		driver115.UA(driver115.UA115Browser),
		func(c *driver115.Pan115Client) {
			c.Client.SetTLSClientConfig(&tls.Config{InsecureSkipVerify: conf.Conf.TlsInsecureSkipVerify})
		},
	}
	d.client = driver115.New(opts...)
	cr := &driver115.Credential{}
	if err := cr.FromCookie(d.Cookie); err != nil {
		return fmt.Errorf("parse 115 cookie: %w", err)
	}
	d.client.ImportCredential(cr)
	return d.client.LoginCheck()
}

// sha1UploadTo115 performs SHA1-based rapid upload and returns the pick_code.
func (d *AliShare115) sha1UploadTo115(ctx context.Context, fileSize int64, fileName, fullSHA1, aliyunURL, dirID string) (string, error) {
	reader := &urlReadSeeker{
		url:    aliyunURL,
		client: &http.Client{Timeout: 60 * time.Second},
		size:   fileSize,
	}

	// Pre-hash: SHA1 of first 128KB
	const preHashSize int64 = 128 * 1024
	readSize := preHashSize
	if fileSize > 0 && fileSize < readSize {
		readSize = fileSize
	}
	if readSize == 0 {
		return "", fmt.Errorf("zero file size")
	}

	buf := make([]byte, int(readSize))
	n, err := io.ReadFull(reader, buf)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return "", fmt.Errorf("read pre-hash: %w", err)
	}
	reader.Seek(0, io.SeekStart)

	h := sha1.New()
	h.Write(buf[:n])
	preHash := strings.ToUpper(hex.EncodeToString(h.Sum(nil)))

	result, err := d.client.RapidUpload(fileSize, fileName, dirID, preHash, fullSHA1, reader)
	if err != nil {
		return "", fmt.Errorf("RapidUpload: %w", err)
	}

	ok, err := result.Ok()
	if err != nil {
		return "", fmt.Errorf("check result: %w", err)
	}
	if !ok {
		return "", fmt.Errorf("upload not matched (status=%d)", result.Status)
	}

	return result.PickCode, nil
}

// === urlReadSeeker: HTTP Range-backed io.ReadSeeker ===

type urlReadSeeker struct {
	url    string
	client *http.Client
	size   int64
	offset int64
}

func (u *urlReadSeeker) Read(p []byte) (n int, err error) {
	n, err = u.ReadAt(p, u.offset)
	if n > 0 {
		u.offset += int64(n)
	}
	return
}

func (u *urlReadSeeker) ReadAt(p []byte, off int64) (n int, err error) {
	if u.size > 0 && off >= u.size {
		return 0, io.EOF
	}
	end := off + int64(len(p)) - 1
	if u.size > 0 && end >= u.size {
		end = u.size - 1
	}

	req, err := http.NewRequest("GET", u.url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", off, end))
	req.Header.Set("Referer", "https://www.aliyundrive.com/")

	resp, err := u.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("http range error: %d", resp.StatusCode)
	}

	n, err = io.ReadFull(resp.Body, p)
	if err == io.ErrUnexpectedEOF || err == io.EOF {
		err = nil
	}
	return n, err
}

func (u *urlReadSeeker) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		u.offset = offset
	case io.SeekCurrent:
		u.offset += offset
	case io.SeekEnd:
		u.offset = u.size + offset
	default:
		return 0, fmt.Errorf("invalid whence: %d", whence)
	}
	if u.offset < 0 {
		return 0, fmt.Errorf("negative seek position")
	}
	return u.offset, nil
}

var _ driver.Driver = (*AliShare115)(nil)
