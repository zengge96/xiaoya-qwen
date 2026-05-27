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

	driver115 "github.com/xiaoyaliu00/115driver/pkg/driver"
)

// ── 115 app version (package-level) ────────────────────────────────────

var appVer = "35.6.0.3"

func (d *AliShare115) get115UserAgent() string {
	return fmt.Sprintf("Mozilla/5.0 115Browser/%s", d.appVer)
}

// fetch115AppVer fetches the latest version from 115 API.
func fetch115AppVer() string {
	type versionWin struct {
		Version string `json:"version_code"`
	}
	type versionData struct {
		Win versionWin `json:"win"`
	}
	type versionResp struct {
		Error string      `json:"error,omitempty"`
		Data  versionData `json:"data"`
	}

	resp := versionResp{}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, driver115.ApiGetVersion, nil)
	if err != nil {
		return appVer
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return appVer
	}
	defer res.Body.Close()

	body, _ := io.ReadAll(res.Body)
	if err := json.Unmarshal(body, &resp); err != nil || resp.Error != "" || resp.Data.Win.Version == "" {
		return appVer
	}
	return resp.Data.Win.Version
}

// ── 115 client initialization ──────────────────────────────────────────

func (d *AliShare115) init115Client() error {
	d.appVer = fetch115AppVer()

	opts := []driver115.Option{
		driver115.UA(d.get115UserAgent()),
	}
	client := driver115.New(opts...)

	cr := &driver115.Credential{}
	if err := cr.FromCookie(d.Cookie115); err != nil {
		return fmt.Errorf("115 cookie parse failed: %w", err)
	}
	client.ImportCredential(cr)

	if err := client.LoginCheck(); err != nil {
		return fmt.Errorf("115 login failed: %w", err)
	}

	d.client115 = client
	return nil
}

// ── HTTP Range ReadSeeker ──────────────────────────────────────────────

// urlReadSeeker reads from an HTTP URL via Range requests, implementing io.ReadSeeker.
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
		return 0, fmt.Errorf("range request failed: HTTP %d", resp.StatusCode)
	}

	n, err := io.ReadFull(resp.Body, p)
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
		return 0, fmt.Errorf("negative seek position")
	}
	u.offset = newOffset
	return u.offset, nil
}

func (u *urlReadSeeker) Close() error { return nil }

// ── SHA1 helpers ───────────────────────────────────────────────────────

// computePreHash computes SHA1 of the first 128KB from a URL.
func computePreHash(downloadURL string, fileSize int64) (string, error) {
	hashSize := int64(128 * 1024) // 128KB
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

// ── Rapid upload to 115 ────────────────────────────────────────────────

type uploadResult struct {
	PickCode string
	FileID   string
}

func (d *AliShare115) uploadTo115(fileName, downloadURL, sha1Str string, fileSize int64) (*uploadResult, error) {
	if sha1Str == "" {
		return nil, fmt.Errorf("SHA1 is required for 115 rapid upload")
	}

	// Compute pre-hash (SHA1 of first 128KB)
	preHash, err := computePreHash(downloadURL, fileSize)
	if err != nil {
		return nil, fmt.Errorf("compute pre-hash failed: %w", err)
	}

	// Create ReadSeeker from URL (for sign check if needed)
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
		return nil, fmt.Errorf("115 rapid upload failed: %w", err)
	}

	ok, err := resp.Ok()
	if err != nil {
		return nil, fmt.Errorf("115 upload response error: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("115 SHA1 not found, file needs manual upload")
	}

	return &uploadResult{
		PickCode: resp.PickCode,
	}, nil
}

// ── Get 115 download URL by pickcode ───────────────────────────────────

func (d *AliShare115) get115DownloadURL(pickCode string) (string, error) {
	ua := fmt.Sprintf("Mozilla/5.0 115Browser/%s", d.appVer)
	info, err := d.client115.DownloadWithUA(pickCode, ua)
	if err != nil {
		return "", fmt.Errorf("115 download URL failed: %w", err)
	}
	return info.Url.Url, nil
}
