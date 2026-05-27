package drivers

import (
	_ "github.com/alist-org/alist/v3/drivers/115"
	_ "github.com/alist-org/alist/v3/drivers/115_share"
	_ "github.com/alist-org/alist/v3/drivers/alishare_115"
	_ "github.com/alist-org/alist/v3/drivers/alist_v2"
	_ "github.com/alist-org/alist/v3/drivers/alist_v3"
	_ "github.com/alist-org/alist/v3/drivers/alias"
	_ "github.com/alist-org/alist/v3/drivers/aliyundrive_open"
	_ "github.com/alist-org/alist/v3/drivers/aliyundrive_share2open"
	_ "github.com/alist-org/alist/v3/drivers/onedrive"
	_ "github.com/alist-org/alist/v3/drivers/pikpak"
	_ "github.com/alist-org/alist/v3/drivers/pikpak_share"
	_ "github.com/alist-org/alist/v3/drivers/webdav"
	_ "github.com/alist-org/alist/v3/drivers/url_tree"
	_ "github.com/alist-org/alist/v3/drivers/strm_list"
)

func All() {
}
