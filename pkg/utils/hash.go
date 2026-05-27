package utils

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"encoding/json"
	"hash"
	"io"
	"errors"
	log "github.com/sirupsen/logrus"
)

func GetMD5EncodeStr(data string) string {
	return HashData(MD5, []byte(data))
}

func HashData(hashType *HashType, data []byte, params ...any) string {
	h := hashType.NewFunc(params...)
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

func GetSHA1Encode(data string) string {
	h := sha1.New()
	h.Write([]byte(data))
	return hex.EncodeToString(h.Sum(nil))
}

func GetSHA256Encode(data string) string {
	h := sha256.New()
	h.Write([]byte(data))
	return hex.EncodeToString(h.Sum(nil))
}

func GetMD5Encode(data string) string {
	h := md5.New()
	h.Write([]byte(data))
	return hex.EncodeToString(h.Sum(nil))
}

var DEC = map[string]string{
	"-": "+",
	"_": "/",
	".": "=",
}

func SafeAtob(data string) (string, error) {
	for k, v := range DEC {
		data = strings.ReplaceAll(data, k, v)
	}
	bytes, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return "", err
	}
	return string(bytes), err
}


var (
	// MD5 indicates MD5 support
	MD5 = RegisterHash("md5", "MD5", 32, md5.New)

	// SHA1 indicates SHA-1 support
	SHA1 = RegisterHash("sha1", "SHA-1", 40, sha1.New)

	// SHA256 indicates SHA-256 support
	SHA256 = RegisterHash("sha256", "SHA-256", 64, sha256.New)
)
var ErrUnsupported = errors.New("hash type not supported")
var (
	name2hash  = map[string]*HashType{}
	alias2hash = map[string]*HashType{}
	Supported  []*HashType
)

type HashType struct {
	Width   int
	Name    string
	Alias   string
	NewFunc func(...any) hash.Hash
}

type MultiHasher struct {
	w    io.Writer
	size int64
	h    map[*HashType]hash.Hash // Hashes
}

func RegisterHash(name, alias string, width int, newFunc func() hash.Hash) *HashType {
	return RegisterHashWithParam(name, alias, width, func(a ...any) hash.Hash { return newFunc() })
}

func RegisterHashWithParam(name, alias string, width int, newFunc func(...any) hash.Hash) *HashType {
	newType := &HashType{
		Name:    name,
		Alias:   alias,
		Width:   width,
		NewFunc: newFunc,
	}

	name2hash[name] = newType
	alias2hash[alias] = newType
	Supported = append(Supported, newType)
	return newType
}

func (m *MultiHasher) GetHashInfo() *HashInfo {
	dst := make(map[*HashType]string)
	for k, v := range m.h {
		dst[k] = hex.EncodeToString(v.Sum(nil))
	}
	return &HashInfo{h: dst}
}

// Sum returns the specified hash from the multihasher
func (m *MultiHasher) Sum(hashType *HashType) ([]byte, error) {
	h, ok := m.h[hashType]
	if !ok {
		return nil, ErrUnsupported
	}
	return h.Sum(nil), nil
}

// Size returns the number of bytes written
func (m *MultiHasher) Size() int64 {
	return m.size
}

// A HashInfo contains hash string for one or more hashType
type HashInfo struct {
	h map[*HashType]string `json:"hashInfo"`
}

func NewHashInfoByMap(h map[*HashType]string) HashInfo {
	return HashInfo{h}
}

func NewHashInfo(ht *HashType, str string) HashInfo {
	m := make(map[*HashType]string)
	if ht != nil {
		m[ht] = str
	}
	return HashInfo{h: m}
}

func (hi HashInfo) String() string {
	result, err := json.Marshal(hi.h)
	if err != nil {
		return ""
	}
	return string(result)
}
func FromString(str string) HashInfo {
	hi := NewHashInfo(nil, "")
	var tmp map[string]string
	err := json.Unmarshal([]byte(str), &tmp)
	if err != nil {
		log.Warnf("failed to unmarsh HashInfo from string=%s", str)
	} else {
		for k, v := range tmp {
			if name2hash[k] != nil && len(v) > 0 {
				hi.h[name2hash[k]] = v
			}
		}
	}

	return hi
}
func (hi HashInfo) GetHash(ht *HashType) string {
	return hi.h[ht]
}

func (hi HashInfo) Export() map[*HashType]string {
	return hi.h
}
