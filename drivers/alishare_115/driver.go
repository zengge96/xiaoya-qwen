package alishare_115

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/alist-org/alist/v3/drivers/aliyundrive_share2open"
	"github.com/alist-org/alist/v3/internal/driver"
	"github.com/alist-org/alist/v3/internal/model"
	driver115 "github.com/xiaoyaliu00/115driver/pkg/driver"
)

type AliShare115 struct {
	model.Storage
	Addition

	// Delegated Aliyun driver
	ali *aliyundrive_share2open.AliyundriveShare2Open

	// 115
	client115 *driver115.Pan115Client
	appVer    string
}

// ── Driver interface ───────────────────────────────────────────────────

func (d *AliShare115) Config() driver.Config {
	return config
}

func (d *AliShare115) GetAddition() driver.Additional {
	return &d.Addition
}

func (d *AliShare115) Init(ctx context.Context) error {
	// Create delegated Aliyun driver
	d.ali = &aliyundrive_share2open.AliyundriveShare2Open{}
	d.ali.Storage = d.Storage
	d.ali.Addition = aliyundrive_share2open.Addition{
		RefreshToken:         d.RefreshToken,
		RefreshTokenOpen:     d.RefreshTokenOpen,
		TempTransferFolderID: d.TempTransferFolderID,
		ShareId:              d.ShareId,
		SharePwd:             d.SharePwd,
		OrderBy:              d.OrderBy,
		OrderDirection:       d.OrderDirection,
		OauthTokenURL:        d.OauthTokenURL,
		ClientID:             d.ClientID,
		ClientSecret:         d.ClientSecret,
	}
	d.ali.Addition.RootID.RootFolderID = d.Addition.RootID.RootFolderID
	if err := d.ali.Init(ctx); err != nil {
		return fmt.Errorf("aliyun init: %w", err)
	}

	// Init 115 client
	if err := d.init115Client(); err != nil {
		return fmt.Errorf("115 init: %w", err)
	}
	return nil
}

func (d *AliShare115) Drop(ctx context.Context) error {
	if d.ali != nil {
		d.ali.Drop(ctx)
	}
	return nil
}

func (d *AliShare115) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {
	return d.ali.List(ctx, dir, args)
}

func (d *AliShare115) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
	// 1. Copy from share to own drive
	newFileID, err := d.ali.Copy2Myali(ctx, d.ali.MyAliDriveId, file.GetID(), file.GetName())
	if err != nil || newFileID == "" {
		return d.fallbackLink()
	}

	time.Sleep(2 * time.Second)

	// 2. Get Aliyun download URL + SHA1
	downloadURL, err := d.ali.GetmyLink(ctx, newFileID, file.GetID(), file.GetName())
	if err != nil && downloadURL == "" {
		return d.fallbackLink()
	}

	// 3. Try 115 rapid upload
	sha1Str := d.ali.Hash_dict[file.GetID()]
	if sha1Str != "" {
		if result, err := d.uploadTo115(file.GetName(), downloadURL, sha1Str, file.GetSize()); err == nil {
			if url115, err := d.get115DownloadURL(result.PickCode); err == nil && !strings.Contains(url115, "abnormal.png") {
				return d.makeLink(url115), nil
			}
		}
	}

	// 4. Fallback: return Aliyun URL
	return d.makeLink(downloadURL), nil
}

func (d *AliShare115) Other(ctx context.Context, args model.OtherArgs) (interface{}, error) {
	return d.ali.Other(ctx, args)
}

func (d *AliShare115) GetHash(ctx context.Context, file model.Obj, args model.LinkArgs) string {
	if d.ali.Hash_dict != nil {
		if hash, ok := d.ali.Hash_dict[file.GetID()]; ok {
			return hash
		}
	}
	if _, err := d.Link(ctx, file, args); err == nil {
		if hash, ok := d.ali.Hash_dict[file.GetID()]; ok {
			return hash
		}
	}
	return ""
}

// ── Link helpers ───────────────────────────────────────────────────────

func (d *AliShare115) makeLink(url string) *model.Link {
	return &model.Link{
		Header: http.Header{
			"Referer": []string{"https://www.aliyundrive.com/"},
		},
		URL: url,
	}
}

func (d *AliShare115) fallbackLink() (*model.Link, error) {
	return d.makeLink("http://img.xiaoya.pro/abnormal.png"), nil
}

// ── 115 client ─────────────────────────────────────────────────────────

func (d *AliShare115) init115Client() error {
	d.appVer = fetch115AppVer()
	opts := []driver115.Option{
		driver115.UA(fmt.Sprintf("Mozilla/5.0 115Browser/%s", d.appVer)),
	}
	client := driver115.New(opts...)

	cr := &driver115.Credential{}
	if err := cr.FromCookie(d.Cookie115); err != nil {
		return fmt.Errorf("115 cookie: %w", err)
	}
	client.ImportCredential(cr)

	if err := client.LoginCheck(); err != nil {
		return fmt.Errorf("115 login: %w", err)
	}

	d.client115 = client
	return nil
}

// ── 115 rapid upload ───────────────────────────────────────────────────

type uploadResult struct {
	PickCode string
}

func (d *AliShare115) uploadTo115(fileName, downloadURL, sha1Str string, fileSize int64) (*uploadResult, error) {
	if sha1Str == "" {
		return nil, fmt.Errorf("SHA1 required")
	}

	preHash, err := computePreHash(downloadURL, fileSize)
	if err != nil {
		return nil, fmt.Errorf("pre-hash: %w", err)
	}

	rs := &urlReadSeeker{
		url:  downloadURL,
		size: fileSize,
		ctx:  context.Background(),
	}

	dirID := d.TargetFolder115
	if dirID == "" {
		dirID = "0"
	}

	resp, err := d.client115.RapidUpload(fileSize, fileName, dirID, preHash, sha1Str, rs)
	if err != nil {
		return nil, fmt.Errorf("rapid upload: %w", err)
	}

	ok, err := resp.Ok()
	if err != nil {
		return nil, fmt.Errorf("upload resp: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("SHA1 not found on 115")
	}

	return &uploadResult{PickCode: resp.PickCode}, nil
}

func (d *AliShare115) get115DownloadURL(pickCode string) (string, error) {
	ua := fmt.Sprintf("Mozilla/5.0 115Browser/%s", d.appVer)
	info, err := d.client115.DownloadWithUA(pickCode, ua)
	if err != nil {
		return "", fmt.Errorf("115 download: %w", err)
	}
	return info.Url.Url, nil
}

// ── HTTP Range ReadSeeker ──────────────────────────────────────────────

type urlReadSeeker struct {
	url    string
	size   int64
	offset int64
	ctx    context.Context
}

func (u *urlReadSeeker) Read(p []byte) (int, error) {
	n, err := u.ReadAt(p, u.offset)
	if n > 0 {
		u.offset += int64(n)
	}
	return n, err
}

func (u *urlReadSeeker) ReadAt(p []byte, off int64) (int, error) {
	if off >= u.size {
		return 0, io.EOF
	}
	end := off + int64(len(p)) - 1
	if end >= u.size {
		end = u.size - 1
	}
	req, err := http.NewRequestWithContext(u.ctx, http.MethodGet, u.url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", off, end))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("Range request: HTTP %d", resp.StatusCode)
	}
	n, err := io.ReadFull(resp.Body, p[:min64(int64(len(p)), end-off+1)])
	if err == io.ErrUnexpectedEOF || err == io.EOF {
		err = nil
	}
	return n, err
}

func (u *urlReadSeeker) Seek(offset int64, whence int) (int64, error) {
	var newOffset int64
	switch whence {
	case io.SeekStart:
		newOffset = offset
	case io.SeekCurrent:
		newOffset = u.offset + offset
	case io.SeekEnd:
		newOffset = u.size + offset
	default:
		return 0, fmt.Errorf("invalid whence")
	}
	if newOffset < 0 {
		return 0, fmt.Errorf("negative seek")
	}
	u.offset = newOffset
	return u.offset, nil
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// ── SHA1 pre-hash ──────────────────────────────────────────────────────

func computePreHash(downloadURL string, fileSize int64) (string, error) {
	hashSize := int64(128 * 1024)
	if fileSize < hashSize {
		hashSize = fileSize
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, downloadURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=0-%d", hashSize-1))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	h := sha1.New()
	if _, err := io.Copy(h, resp.Body); err != nil {
		return "", err
	}
	return strings.ToUpper(hex.EncodeToString(h.Sum(nil))), nil
}

// ── 115 app version ────────────────────────────────────────────────────

var defaultAppVer = "35.6.0.3"

func fetch115AppVer() string {
	type win struct {
		Version string `json:"version_code"`
	}
	type data struct {
		Win win `json:"win"`
	}
	type resp struct {
		Error string `json:"error,omitempty"`
		Data  data   `json:"data"`
	}
	var r resp
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, driver115.ApiGetVersion, nil)
	if err != nil {
		return defaultAppVer
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return defaultAppVer
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if err := json.Unmarshal(body, &r); err != nil || r.Error != "" || r.Data.Win.Version == "" {
		return defaultAppVer
	}
	return r.Data.Win.Version
}

// Interface check
var _ driver.Driver = (*AliShare115)(nil)
