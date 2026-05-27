package official_115

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/alist-org/alist/v3/internal/model"
	"github.com/alist-org/alist/v3/pkg/http_range"
	"github.com/alist-org/alist/v3/pkg/utils"
	"github.com/pkg/errors"
)

// hashReader reads from a reader and computes hash
func hashReader(algo *utils.HashType, r io.Reader) (string, error) {
	var h interface{ Write([]byte) (int, error); Sum(b []byte) []byte }
	if algo == utils.SHA1 {
		h = sha1.New()
	} else if algo == utils.MD5 {
		h = md5.New()
	} else {
		return "", errors.New("unsupported hash algorithm")
	}
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type File interface {
    io.Reader
    io.ReaderAt
    io.Seeker
    io.Closer
}

type FileStreamer interface {
    io.Reader
    io.Closer
    GetID() string
    GetName() string
    SetPath(path string)
    GetPath() string
    GetSize() int64
    GetHash() utils.HashInfo
    GetMimetype() string
    ModTime() time.Time
    CreateTime() time.Time
    IsDir() bool
    NeedStore() bool
    IsForceStreamUpload() bool
    GetExist() model.Obj
    SetExist(model.Obj)
    Add(io.Closer)
    AddIfCloser(any)
    RangeRead(ra http_range.Range) (io.Reader, error)
    GetFile() File
}

// VirtualFile 按需发 HTTP Range 请求，不落盘
type VirtualFile struct {
	url        string
	client     *http.Client
	size       int64
	currOffset int64
	ctx        context.Context
}

func (v *VirtualFile) Read(p []byte) (n int, err error) {
	n, err = v.ReadAt(p, v.currOffset)
	if n > 0 {
		v.currOffset += int64(n) // 必须推进偏移量
	}
	return n, err
}

func (v *VirtualFile) ReadAt(p []byte, off int64) (n int, err error) {
	if off >= v.size && v.size > 0 {
		return 0, io.EOF
	}
	endPos := off + int64(len(p)) - 1
	if v.size > 0 && endPos >= v.size {
		endPos = v.size - 1
	}
	req, err := http.NewRequestWithContext(v.ctx, http.MethodGet, v.url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", off, endPos))
	resp, err := v.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("http error: %d", resp.StatusCode)
	}

	n, err = io.ReadFull(resp.Body, p)
	if err == io.ErrUnexpectedEOF || err == io.EOF {
		// Server returned fewer bytes than requested (e.g., CDN range limit)
		// n contains actual bytes read
		err = nil
	}
	return n, err
}

func (v *VirtualFile) Seek(offset int64, whence int) (int64, error) {
	var newOffset int64
	switch whence {
	case io.SeekStart:
		newOffset = offset
	case io.SeekCurrent:
		newOffset = v.currOffset + offset
	case io.SeekEnd:
		newOffset = v.size + offset
	default:
		return 0, errors.New("invalid whence")
	}
	if newOffset < 0 {
		return 0, errors.New("seek position out of range")
	}
	v.currOffset = newOffset
	return v.currOffset, nil
}

func (v *VirtualFile) Close() error { return nil }

type urlFileStreamer struct {
	name     string
	path     string
	size     int64
	sha1Str  string
	url      string
	rapidUpload bool
	reader      io.Reader
	readerClose func() error
	file      File   // 缓存虚拟文件，避免重复创建
}

func (f *urlFileStreamer) GetID() string            { return "" }
func (f *urlFileStreamer) GetName() string          { return f.name }
func (f *urlFileStreamer) SetPath(path string)       { f.path = path }
func (f *urlFileStreamer) GetSize() int64            { return f.size }
func (f *urlFileStreamer) ModTime() time.Time        { return time.Time{} }
func (f *urlFileStreamer) CreateTime() time.Time     { return time.Time{} }
func (f *urlFileStreamer) IsDir() bool               { return false }
func (f *urlFileStreamer) GetHash() utils.HashInfo { return utils.NewHashInfo(utils.SHA1, f.sha1Str) }
func (f *urlFileStreamer) GetPath() string           { return f.path }
func (f *urlFileStreamer) GetMimetype() string       { return "application/octet-stream" }
func (f *urlFileStreamer) NeedStore() bool           { return true }
func (f *urlFileStreamer) IsForceStreamUpload() bool { return false }
func (f *urlFileStreamer) GetExist() model.Obj        { return nil }
func (f *urlFileStreamer) SetExist(model.Obj)        {}
func (f *urlFileStreamer) Add(io.Closer)             {}
func (f *urlFileStreamer) AddIfCloser(any)           {}

func NewUrlFileStreamer(name string, size int64, sha1Str, url string) *urlFileStreamer {
	return &urlFileStreamer{name: name, size: size, sha1Str: sha1Str, url: url}
}

func (f *urlFileStreamer) Read(p []byte) (n int, err error) {
	if f.reader == nil {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, f.url, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0, err
		}
		f.reader = resp.Body
		f.readerClose = resp.Body.Close
	}
	return f.reader.Read(p)
}

func (f *urlFileStreamer) Close() error {
	if f.readerClose != nil {
		f.readerClose()
		f.readerClose = nil
	}
	return nil
}

func (f *urlFileStreamer) RangeRead(ra http_range.Range) (io.Reader, error) {
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, f.url, nil)
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", ra.Start, ra.Start+ra.Length-1))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func (f *urlFileStreamer) GetFile() File {
	if f.file != nil {
		f.file.Seek(0, io.SeekStart)
	}
	return f.file
}

