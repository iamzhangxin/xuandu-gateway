package runtime

import (
	"sync"
	"time"

	"context"
	"github.com/cloudwego/kitex/client/callopt"

	"github.com/iamzhangxin/xuandu-gateway/internal/idl"
	"github.com/iamzhangxin/xuandu-gateway/internal/metadata"
)

type HTTPClient interface {
	GenericCall(context.Context, string, any, ...callopt.Option) (any, error)
	Close() error
}

type ServiceRuntime struct {
	AppName     string
	ServiceName string
	Revision    string
	Routes      []idl.Route
	Client      HTTPClient
	Timeout     time.Duration
	Config      metadata.App
	closeOnce   sync.Once
	closeErr    error
	refs        int
	retired     bool
}

func (r *ServiceRuntime) Close() error {
	r.closeOnce.Do(func() {
		if r.Client != nil {
			r.closeErr = r.Client.Close()
		}
	})
	return r.closeErr
}
