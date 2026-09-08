package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/cloudwego/kitex/client"
	"github.com/cloudwego/kitex/client/genericclient"
	"github.com/cloudwego/kitex/pkg/generic"
	"github.com/cloudwego/kitex/pkg/transmeta"
	"github.com/cloudwego/kitex/transport"
	consulapi "github.com/hashicorp/consul/api"
	consul "github.com/kitex-contrib/registry-consul"

	"github.com/iamzhangxin/xuandu-gateway/internal/archiveidl"
	"github.com/iamzhangxin/xuandu-gateway/internal/idl"
	"github.com/iamzhangxin/xuandu-gateway/internal/metadata"
	"github.com/iamzhangxin/xuandu-gateway/internal/router"
)

type Builder struct{ Consul *consulapi.Config }

func NewBuilder(c *consulapi.Config) *Builder { return &Builder{c} }
func (b *Builder) buildOne(ctx context.Context, a *metadata.App, r *archiveidl.Revision, bundle *idl.Bundle, routes []idl.Route) (out *ServiceRuntime, err error) {
	var cleanup func() error
	defer func() {
		if recover() != nil {
			if cleanup != nil {
				cleanup()
			}
			out = nil
			err = fmt.Errorf("invalid runtime descriptor")
		}
	}()
	if _, err = router.Build(map[string][]idl.Route{a.Name: routes}); err != nil {
		return nil, err
	}

	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if err = a.Validate(); err != nil {
		return nil, err
	}
	p, e := generic.NewThriftContentWithAbsIncludePathProviderWithDynamicGo(bundle.MainPath, bundle.Files)
	if e != nil {
		return nil, fmt.Errorf("cannot build thrift provider")
	}
	cleanup = p.Close
	// Decode structured responses: DynamicGo raw decoding cannot handle the empty
	// Thrift result carried with a TTHeader business error.
	g, e := generic.HTTPThriftGeneric(p, generic.UseRawBodyForHTTPResp(false))
	if e != nil {
		p.Close()
		return nil, fmt.Errorf("cannot build HTTP generic codec")
	}
	cleanup = g.Close
	resolver, e := consul.NewConsulResolverWithConfig(b.Consul)
	if e != nil {
		g.Close()
		return nil, fmt.Errorf("cannot build consul resolver")
	}
	timeout, _ := time.ParseDuration(a.RPCTimeout)
	cli, e := genericclient.NewClient(a.ServiceName, g, client.WithResolver(resolver), client.WithTransportProtocol(transport.TTHeader), client.WithMetaHandler(transmeta.ClientTTHeaderHandler), client.WithRPCTimeout(timeout))
	if e != nil {
		g.Close()
		return nil, fmt.Errorf("cannot build generic client")
	}
	cleanup = cli.Close
	if e = ctx.Err(); e != nil {
		cli.Close()
		return nil, e
	}
	return &ServiceRuntime{AppName: a.Name, ServiceName: a.ServiceName, Revision: r.Digest, Routes: append([]idl.Route(nil), routes...), Client: cli, Timeout: timeout, Config: *a}, nil
}

func (b *Builder) Build(ctx context.Context, a *metadata.App, r *archiveidl.Revision, bundles []*idl.Bundle, routes []idl.Route) (*ServiceRuntime, error) {
	if _, e := router.Build(map[string][]idl.Route{a.Name: routes}); e != nil {
		return nil, e
	}
	group := &multiClient{clients: map[string]HTTPClient{}}
	success := false
	defer func() {
		if !success {
			group.Close()
		}
	}()
	byFile := map[string][]idl.Route{}
	for _, bundle := range bundles {
		rs, e := idl.ExtractRoutes(bundle)
		if e != nil {
			return nil, e
		}
		one, e := b.buildOne(ctx, a, r, bundle, rs)
		if e != nil {
			return nil, e
		}
		group.clients[bundle.MainPath] = one.Client
		byFile[bundle.MainPath] = rs
	}
	table, e := router.Build(byFile)
	if e != nil {
		return nil, e
	}
	group.routes = table
	timeout, _ := time.ParseDuration(a.RPCTimeout)
	success = true
	return &ServiceRuntime{AppName: a.Name, ServiceName: a.ServiceName, Revision: r.Digest, Routes: append([]idl.Route(nil), routes...), Client: group, Timeout: timeout, Config: *a}, nil
}
