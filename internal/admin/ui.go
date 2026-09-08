package admin

import (
	"context"
	"mime"
	"path"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/iamzhangxin/xuandu-gateway/web"
)

func serveUI(_ context.Context, c *app.RequestContext) {
	name := strings.TrimPrefix(string(c.Path()), "/")
	if name == "" {
		name = "index.html"
	}
	if !c.IsGet() || strings.HasPrefix(name, "admin/") || path.Clean(name) != name || strings.Contains(name, "\\") {
		c.Status(404)
		return
	}
	data, err := web.Assets.ReadFile("dist/" + name)
	if err != nil {
		c.Status(404)
		return
	}
	c.Header("Cache-Control", "no-cache")
	c.Header("X-Content-Type-Options", "nosniff")
	contentType := mime.TypeByExtension(path.Ext(name))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	c.Data(200, contentType, data)
}
