package httpgateway

import (
	"context"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"

	"github.com/iamzhangxin/xuandu-gateway/internal/admin"
	"github.com/iamzhangxin/xuandu-gateway/internal/config"
)

type DataServer struct{ *server.Hertz }

func NewServer(c *config.Config, h *Handler, s *admin.Service) *DataServer {
	v := server.New(server.WithHostPorts(c.Server.Address), server.WithReadTimeout(c.Server.ReadTimeout), server.WithWriteTimeout(c.Server.WriteTimeout), server.WithIdleTimeout(c.Server.IdleTimeout), server.WithMaxRequestBodySize(c.Server.MaxRequestBodyBytes), server.WithRedirectTrailingSlash(false), server.WithRedirectFixedPath(false), server.WithRemoveExtraSlash(false), server.WithExitWaitTime(25*time.Second))
	v.GET("/healthz", func(_ context.Context, c *app.RequestContext) { c.JSON(200, map[string]string{"status": "ok"}) })
	v.GET("/readyz", func(_ context.Context, c *app.RequestContext) {
		ok, details := s.Ready()
		status := 200
		if !ok {
			status = 503
		}
		c.JSON(status, map[string]any{"ready": ok, "degraded": details})
	})
	v.NoRoute(h.Serve)
	return &DataServer{v}
}
