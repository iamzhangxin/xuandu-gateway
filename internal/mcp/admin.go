package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"io"
	"time"
)

func (s *Service) Register(h *server.Hertz) {
	reply := func(c *app.RequestContext, v any, e error) {
		c.Header("Cache-Control", "no-store")
		if e == nil {
			c.JSON(200, v)
			return
		}
		status := 422
		if errors.Is(e, ErrConflict) {
			status = 409
		}
		c.JSON(status, map[string]string{"code": "MCP_CONFIG_ERROR", "message": e.Error()})
	}
	h.GET("/admin/mcp", func(ctx context.Context, c *app.RequestContext) {
		if s.current.Load() == nil {
			work, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			if e := s.Refresh(work); e != nil {
				reply(c, nil, e)
				return
			}
		}
		v, e := s.View()
		reply(c, v, e)
	})
	h.GET("/admin/mcp/servers/:id/tools", func(ctx context.Context, c *app.RequestContext) { v, e := s.Tools(c.Param("id")); reply(c, v, e) })
	h.POST("/admin/mcp", func(ctx context.Context, c *app.RequestContext) {
		var m Mutation
		d := json.NewDecoder(bytes.NewReader(c.Request.Body()))
		d.DisallowUnknownFields()
		e := d.Decode(&m)
		var extra any
		if e != nil || d.Decode(&extra) != io.EOF {
			c.JSON(400, map[string]string{"message": "无效的 JSON 请求"})
			return
		}
		work, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		v, e := s.Mutate(work, m)
		reply(c, v, e)
	})
}
