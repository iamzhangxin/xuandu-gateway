package router

import (
	"testing"

	"github.com/iamzhangxin/xuandu-gateway/internal/idl"
)

func TestRouteTable(t *testing.T) {
	r, e := Build(map[string][]idl.Route{"a": {{HTTPMethod: "GET", Path: "/p/:id"}, {HTTPMethod: "POST", Path: "/p"}}, "b": {{HTTPMethod: "GET", Path: "/p/list"}}})
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct{ method, path, app string }{{"GET", "/p/123", "a"}, {"GET", "/p/list", "b"}, {"POST", "/p", "a"}, {"DELETE", "/p", ""}, {"GET", "/p/123/extra", ""}} {
		target, ok := r.Match(tc.method, tc.path)
		if ok != (tc.app != "") || target.AppName != tc.app {
			t.Fatalf("%+v: %+v %v", tc, target, ok)
		}
	}
	for _, path := range []string{"/p/:id", "/p/:other"} {
		if _, e := Build(map[string][]idl.Route{"a": {{HTTPMethod: "GET", Path: "/p/:id"}}, "b": {{HTTPMethod: "GET", Path: path}}}); e == nil {
			t.Fatal("accepted conflicting params")
		}
	}
}
func BenchmarkMatch(b *testing.B) {
	r, _ := Build(map[string][]idl.Route{"a": {{HTTPMethod: "GET", Path: "/p/:id"}}})
	b.ReportAllocs()
	for b.Loop() {
		r.Match("GET", "/p/123")
	}
}
