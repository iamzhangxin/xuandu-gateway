//go:build wireinject

package app

import (
	"github.com/google/wire"

	"github.com/iamzhangxin/xuandu-gateway/internal/admin"
	"github.com/iamzhangxin/xuandu-gateway/internal/archiveidl"
	"github.com/iamzhangxin/xuandu-gateway/internal/config"
	"github.com/iamzhangxin/xuandu-gateway/internal/httpgateway"
	"github.com/iamzhangxin/xuandu-gateway/internal/mcp"
	"github.com/iamzhangxin/xuandu-gateway/internal/metadata"
	rt "github.com/iamzhangxin/xuandu-gateway/internal/runtime"
)

func Initialize(c *config.Config) (*App, error) {
	wire.Build(metadata.NewConsulConfig, metadata.NewConsulStore, wire.Bind(new(metadata.Store), new(*metadata.ConsulStore)), archiveidl.NewFetcher, wire.Bind(new(archiveidl.Fetcher), new(*archiveidl.HTTPFetcher)), rt.NewBuilder, wire.Bind(new(admin.RuntimeBuilder), new(*rt.Builder)), rt.NewManager, admin.NewService, httpgateway.NewHandler, httpgateway.NewServer, admin.NewServer, mcp.NewRepository, mcp.NewService, New)
	return nil, nil
}
