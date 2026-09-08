package runtime

import (
	"context"
	"fmt"
	"github.com/cloudwego/kitex/client/callopt"
	"github.com/cloudwego/kitex/client/genericclient"
	"github.com/cloudwego/kitex/pkg/generic"
	consul "github.com/hashicorp/consul/api"
	"github.com/iamzhangxin/xuandu-gateway/internal/archiveidl"
	"github.com/iamzhangxin/xuandu-gateway/internal/idl"
	"github.com/iamzhangxin/xuandu-gateway/internal/metadata"
	"net/http"
	"strings"
	"testing"
)

type namedClient struct {
	genericclient.Client
	name   string
	closed int
}

func (c *namedClient) GenericCall(_ context.Context, m string, _ any, _ ...callopt.Option) (any, error) {
	if m != "" {
		return nil, fmt.Errorf("method must be selected by IDL")
	}
	return c.name, nil
}
func (c *namedClient) Close() error { c.closed++; return nil }
func TestMultipleIDLClients(t *testing.T) {
	files := map[string]string{"idl/common.thrift": `struct Q {1:string name}`}
	for _, n := range []string{"a", "b"} {
		files["idl/"+n+".thrift"] = `include "common.thrift" service S {common.Q Get(1:common.Q req)(api.get="/` + n + `")}`
	}
	bundles, routes, e := idl.LoadArchive(files)
	if e != nil {
		t.Fatal(e)
	}
	a := &metadata.App{Name: "p", ServiceName: "p", RPCTimeout: "1s", IDL: metadata.IDLSource{Type: "zip", URL: "https://example.com/idl.zip", ResolvedRevision: strings.Repeat("a", 64)}}
	r, e := NewBuilder(consul.DefaultConfig()).Build(context.Background(), a, &archiveidl.Revision{Digest: a.IDL.ResolvedRevision, Files: files}, bundles, routes)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	group := r.Client.(*multiClient)
	if len(group.clients) != 2 {
		t.Fatal("IDL clients lost")
	}
	var clients []*namedClient
	for path, old := range group.clients {
		old.Close()
		c := &namedClient{name: path}
		group.clients[path] = c
		clients = append(clients, c)
	}
	for _, n := range []string{"a", "b"} {
		req, _ := http.NewRequest("GET", "http://localhost/"+n, nil)
		g, _ := generic.FromHTTPRequest(req)
		out, e := group.GenericCall(context.Background(), "", g)
		if e != nil || out != "idl/"+n+".thrift" {
			t.Fatal(out, e)
		}
	}
	r.Close()
	r.Close()
	for _, c := range clients {
		if c.closed != 1 {
			t.Fatal("client lifecycle", c.closed)
		}
	}
}
