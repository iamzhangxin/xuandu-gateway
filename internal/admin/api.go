package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"

	"github.com/iamzhangxin/xuandu-gateway/internal/config"
	"github.com/iamzhangxin/xuandu-gateway/internal/metadata"
	"github.com/iamzhangxin/xuandu-gateway/internal/router"
)

type Server struct {
	*server.Hertz
	Enabled bool
}

func reply(c *app.RequestContext, v any, e error) {
	if e == nil {
		c.JSON(200, v)
		return
	}
	status, code, msg := 422, "INVALID_CONTRACT", "contract validation or build failed"
	var conflict *router.ConflictError
	switch {
	case errors.Is(e, metadata.ErrInvalidDomain):
		status, code, msg = 422, "INVALID_DOMAIN", "请输入有效域名或 IP，不包含协议、端口或路径"
	case errors.Is(e, metadata.ErrNotFound):
		status, code, msg = 404, "APP_NOT_FOUND", "application not found"
	case errors.Is(e, metadata.ErrConflict):
		status, code, msg = 409, "METADATA_CONFLICT", "application exists or metadata changed; retry after synchronization"
	case errors.As(e, &conflict):
		status, code, msg = 409, "ROUTE_CONFLICT", conflict.Error()
	case errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded):
		status, code, msg = 503, "ADMIN_TIMEOUT", "administration request interrupted"
	}
	c.JSON(status, map[string]string{"code": code, "message": msg})
}
func NewServer(c *config.Config, s *Service) *Server {
	h := server.New(server.WithHostPorts(c.Admin.Address), server.WithReadTimeout(c.Server.ReadTimeout), server.WithWriteTimeout(0), server.WithExitWaitTime(25*time.Second), server.WithMaxRequestBodySize(64<<10))
	h.GET("/", serveUI)
	h.NoRoute(serveUI)
	h.POST("/admin/apps", func(ctx context.Context, c *app.RequestContext) {
		var req CreateAppRequest
		d := json.NewDecoder(bytes.NewReader(c.Request.Body()))
		d.DisallowUnknownFields()
		if e := d.Decode(&req); e != nil {
			c.JSON(400, map[string]string{"code": "INVALID_JSON"})
			return
		}
		var extra any
		if e := d.Decode(&extra); e != io.EOF {
			c.JSON(400, map[string]string{"code": "INVALID_JSON"})
			return
		}
		v, e := s.CreateApp(ctx, req)
		reply(c, v, e)
	})
	h.GET("/admin/apps", func(ctx context.Context, c *app.RequestContext) { v, e := s.List(ctx); reply(c, v, e) })
	h.GET("/admin/apps/:name", func(ctx context.Context, c *app.RequestContext) { v, e := s.Get(ctx, c.Param("name")); reply(c, v, e) })
	h.GET("/admin/apps/:name/openapi.json", func(ctx context.Context, c *app.RequestContext) {
		v, e := s.OpenAPI(ctx, c.Param("name"))
		c.Header("Cache-Control", "no-cache")
		reply(c, v, e)
	})
	h.POST("/admin/apps/:name/update", func(ctx context.Context, c *app.RequestContext) {
		var req UpdateAppRequest
		if len(c.Request.Body()) > 0 {
			d := json.NewDecoder(bytes.NewReader(c.Request.Body()))
			d.DisallowUnknownFields()
			if e := d.Decode(&req); e != nil {
				c.JSON(400, map[string]string{"code": "INVALID_JSON"})
				return
			}
			var extra any
			if e := d.Decode(&extra); e != io.EOF {
				c.JSON(400, map[string]string{"code": "INVALID_JSON"})
				return
			}
		}
		v, e := s.UpdateAppWithRequest(ctx, c.Param("name"), req)
		reply(c, v, e)
	})
	h.DELETE("/admin/apps/:name", func(ctx context.Context, c *app.RequestContext) {
		e := s.DeleteApp(ctx, c.Param("name"))
		reply(c, map[string]bool{"deleted": e == nil}, e)
	})
	return &Server{h, c.Admin.Enabled}
}
