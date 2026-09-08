package runtime

import (
	"context"
	"strings"
	"testing"

	consul "github.com/hashicorp/consul/api"

	"github.com/iamzhangxin/xuandu-gateway/internal/archiveidl"
	"github.com/iamzhangxin/xuandu-gateway/internal/idl"
	"github.com/iamzhangxin/xuandu-gateway/internal/metadata"
)

func TestBuilderAndClose(t *testing.T) {
	files := map[string]string{"idl/main.thrift": `struct Q {1:string name(api.body="name")} service S {Q Get(1:Q req)(api.post="/p")}`}
	bundles, routes, e := idl.LoadArchive(files)
	if e != nil {
		t.Fatal(e)
	}
	a := &metadata.App{Name: "p", ServiceName: "p", RPCTimeout: "1s", IDL: metadata.IDLSource{Type: "zip", URL: "https://example.com/idl.zip", ResolvedRevision: strings.Repeat("a", 64)}}
	b := NewBuilder(consul.DefaultConfig())
	r, e := b.Build(context.Background(), a, &archiveidl.Revision{Digest: a.IDL.ResolvedRevision}, bundles, routes)
	if e != nil {
		t.Fatal(e)
	}
	if r.Client == nil || r.Revision != a.IDL.ResolvedRevision {
		t.Fatal("invalid runtime")
	}
	if e = r.Close(); e != nil {
		t.Fatal(e)
	}
	if e = r.Close(); e != nil {
		t.Fatal(e)
	}
	routes = append(routes, routes[0])
	if _, e = b.Build(context.Background(), a, &archiveidl.Revision{}, bundles, routes); e == nil {
		t.Fatal("invalid route accepted")
	}
}
