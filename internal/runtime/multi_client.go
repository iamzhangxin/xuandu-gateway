package runtime

import (
	"context"
	"errors"
	"fmt"
	"github.com/cloudwego/kitex/client/callopt"

	"github.com/cloudwego/kitex/pkg/generic"
	"github.com/iamzhangxin/xuandu-gateway/internal/router"
)

type multiClient struct {
	clients map[string]HTTPClient
	routes  router.Table
}

func (c *multiClient) GenericCall(ctx context.Context, method string, request any, opts ...callopt.Option) (any, error) {
	r, ok := request.(*generic.HTTPRequest)
	if !ok {
		return nil, fmt.Errorf("HTTP request required")
	}
	target, ok := c.routes.Match(r.Request.Method, r.Request.URL.Path)
	if !ok {
		return nil, fmt.Errorf("IDL route not found")
	}
	return c.clients[target.AppName].GenericCall(ctx, "", request, opts...)
}
func (c *multiClient) Close() error {
	var e error
	for _, client := range c.clients {
		e = errors.Join(e, client.Close())
	}
	return e
}
