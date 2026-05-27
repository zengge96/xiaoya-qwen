package _115

import (
	//"github.com/SheltonZhu/115driver/pkg/driver"
	"github.com/xiaoyaliu00/115driver/pkg/driver"
	"github.com/alist-org/alist/v3/internal/model"
)

var _ model.Obj = (*FileObj)(nil)

type FileObj struct {
	driver.File
}
