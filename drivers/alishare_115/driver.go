package alishare_115

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/alist-org/alist/v3/drivers/base"
	"github.com/alist-org/alist/v3/internal/driver"
	"github.com/alist-org/alist/v3/internal/model"
	"github.com/alist-org/alist/v3/pkg/cron"
	"github.com/alist-org/alist/v3/pkg/utils"
	"github.com/go-resty/resty/v2"
	driver115 "github.com/xiaoyaliu00/115driver/pkg/driver"
)

type AliShare115 struct {
	model.Storage
	Addition

	// Aliyun auth
	AccessToken     string
	ShareToken      string
	AccessTokenOpen string
	MyAliDriveId    string

	// 115 client
	client115 *driver115.Pan115Client
	appVer    string

	// Caches
	CopyFiles   map[string]string // share file_id -> own drive file_id
	FileID_Link map[string]string // own drive file_id -> final URL
	Hash_dict   map[string]string // share file_id -> SHA1

	cron1 *cron.Cron
	base  string
}

func (d *AliShare115) Config() driver.Config {
	return config
}

func (d *AliShare115) GetAddition() driver.Additional {
	return &d.Addition
}

func (d *AliShare115) Init(ctx context.Context) error {
	if d.base == "" {
		d.base = "https://openapi.alipan.com"
	}
	// Placeholder — real auth happens lazily in Link/List
	d.AccessToken = "fake_token_for_fast_init"
	d.ShareToken = "fake_share_token"
	d.AccessTokenOpen = "fake_open_token"

	d.CopyFiles = make(map[string]string)
	d.FileID_Link = make(map[string]string)
	d.Hash_dict = make(map[string]string)

	// 115 client from cookie
	if err := d.init115Client(); err != nil {
		return fmt.Errorf("115 init: %w", err)
	}

	// Periodic cache cleanup (every 3 min)
	d.cron1 = cron.NewCron(time.Minute * 3)
	d.cron1.Do(func() {
		if len(d.FileID_Link) == 0 {
			return
		}
		d.FileID_Link = make(map[string]string)
		d.Hash_dict = make(map[string]string)
		d.CopyFiles = make(map[string]string)
	})

	return nil
}

func (d *AliShare115) Drop(ctx context.Context) error {
	if d.cron1 != nil {
		d.cron1.Stop()
	}
	d.MyAliDriveId = ""
	return nil
}

// ── List ────────────────────────────────────────────────────────────────

func (d *AliShare115) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {
	if err := d.ensureValidToken(ctx); err != nil {
		return nil, err
	}

	var lastErr error
	for attempt := 0; attempt <= 3; attempt++ {
		files, err := d.getFiles(dir.GetID())
		if err == nil {
			return utils.SliceConvert(files, func(src File) (model.Obj, error) {
				return fileToObj(src), nil
			})
		}
		lastErr = err
		time.Sleep(2 * time.Second)
	}
	return nil, fmt.Errorf("list failed: %w", lastErr)
}

// ── Link ────────────────────────────────────────────────────────────────

func (d *AliShare115) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
	if err := d.ensureValidToken(ctx); err != nil {
		return nil, err
	}

	fileID := file.GetID()
	fileName := file.GetName()

	// 1. Cache hit — already on 115
	if cachedURL, ok := d.FileID_Link[fileID]; ok {
		return d.makeLink(cachedURL), nil
	}

	// 2. Copy share file → own Aliyun drive (required for Open API access)
	newFileID, err := d.copyToMyDrive(ctx, fileID, fileName)
	if err != nil {
		return d.fallbackLink()
	}

	time.Sleep(2 * time.Second)

	// 3. Get download URL + SHA1 from own drive
	info, err := d.getOpenDownloadInfo(newFileID, fileID, fileName)
	if err != nil {
		return d.fallbackLink()
	}

	// 4. Rapid upload to 115
	result, err := d.uploadTo115(fileName, info.URL, info.SHA1, info.Size)
	if err != nil {
		// Fallback: return Aliyun URL directly
		d.FileID_Link[fileID] = info.URL
		return d.makeLink(info.URL), nil
	}

	// 5. Get 115 download URL
	downloadURL, err := d.get115DownloadURL(result.PickCode)
	if err != nil {
		return d.fallbackLink()
	}

	// 6. Cache & return
	if !strings.Contains(downloadURL, "abnormal.png") {
		d.FileID_Link[fileID] = downloadURL
	}

	return d.makeLink(downloadURL), nil
}

// ── Other (video preview) ──────────────────────────────────────────────

func (d *AliShare115) Other(ctx context.Context, args model.OtherArgs) (interface{}, error) {
	var resp base.Json
	var uri string

	newFileID, _ := d.copyToMyDrive(ctx, args.Obj.GetID(), args.Obj.GetName())
	data := base.Json{
		"drive_id": d.MyAliDriveId,
		"share_id": d.ShareId,
		"file_id":  newFileID,
	}
	switch args.Method {
	case "video_preview":
		uri = "/adrive/v1.0/openFile/getVideoPreviewPlayInfo"
		data["category"] = "live_transcoding"
		data["url_expire_sec"] = 14400
	default:
		return nil, fmt.Errorf("unsupported method: %s", args.Method)
	}
	_, err := d.requestOpen(uri, http.MethodPost, func(req *resty.Request) {
		req.SetBody(data).SetResult(&resp)
	})
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// ── Hash ────────────────────────────────────────────────────────────────

func (d *AliShare115) GetHash(ctx context.Context, file model.Obj, args model.LinkArgs) string {
	if d.Hash_dict == nil {
		return ""
	}
	if hash, ok := d.Hash_dict[file.GetID()]; ok {
		return hash
	}
	// Trigger Link to populate cache
	if _, err := d.Link(ctx, file, args); err == nil {
		if hash, ok := d.Hash_dict[file.GetID()]; ok {
			return hash
		}
	}
	return ""
}

// ── Helpers ─────────────────────────────────────────────────────────────

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

// Interface check
var _ driver.Driver = (*AliShare115)(nil)
