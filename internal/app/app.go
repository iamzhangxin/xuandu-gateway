package app

import (
	"context"
	"errors"
	"time"

	"github.com/iamzhangxin/xuandu-gateway/internal/admin"
	"github.com/iamzhangxin/xuandu-gateway/internal/httpgateway"
	"github.com/iamzhangxin/xuandu-gateway/internal/mcp"
	rt "github.com/iamzhangxin/xuandu-gateway/internal/runtime"
)

type App struct {
	Service *admin.Service
	Data    *httpgateway.DataServer
	Admin   *admin.Server
	Manager *rt.Manager
	Mcp     *mcp.Service
}

func New(s *admin.Service, d *httpgateway.DataServer, a *admin.Server, m *rt.Manager, mc *mcp.Service) *App {
	mc.Register(a.Hertz)
	return &App{s, d, a, m, mc}
}
func (a *App) Run(ctx context.Context) error {
	work, cancel := context.WithCancel(context.Background())
	defer cancel()
	initial, stop := context.WithTimeout(ctx, 2*time.Minute)
	apps, e := a.Service.Store.List(initial)
	if e == nil {
		a.Service.Reconcile(initial, apps)
	}
	stop()
	if e != nil {
		a.Manager.Close(context.Background())
		return errors.New("cannot load initial Consul metadata")
	}
	a.Mcp.Start(work)
	done := make(chan error, 3)
	go func() { done <- a.Service.Watch(work) }()
	go func() { done <- a.Data.Run() }()
	if a.Admin.Enabled {
		go func() { done <- a.Admin.Run() }()
	}
	select {
	case <-ctx.Done():
	case e = <-done:
	}
	a.Service.Stop()
	deadline, release := context.WithTimeout(context.Background(), 25*time.Second)
	defer release()
	shutdown := make(chan error, 3)
	go func() { shutdown <- a.Mcp.Shutdown(deadline) }()
	if a.Admin.Enabled {
		go func() { shutdown <- a.Admin.Shutdown(deadline) }()
	} else {
		shutdown <- nil
	}
	go func() { shutdown <- a.Data.Shutdown(deadline) }()
	for i := 0; i < 3; i++ {
		select {
		case err := <-shutdown:
			e = errors.Join(e, err)
		case <-deadline.Done():
			e = errors.Join(e, deadline.Err())
		}
	}
	cancel()
	e = errors.Join(e, a.Manager.Close(deadline))
	return e
}
