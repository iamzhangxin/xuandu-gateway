package router

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"

	"github.com/cloudwego/kitex/pkg/generic/descriptor"

	"github.com/iamzhangxin/xuandu-gateway/internal/idl"
)

type Target struct {
	AppName string
	Route   idl.Route
}
type Table interface {
	Match(string, string) (Target, bool)
	Routes() []idl.Route
}
type table struct {
	router  descriptor.Router
	routes  []idl.Route
	targets map[*descriptor.FunctionDescriptor]Target
}
type ConflictError struct{ Message string }

func (e *ConflictError) Error() string { return e.Message }
func Build(apps map[string][]idl.Route) (out Table, err error) {
	defer func() {
		if recover() != nil {
			out = nil
			err = &ConflictError{"conflicting or invalid HTTP route pattern"}
		}
	}()
	t := &table{router: descriptor.NewRouter(), targets: map[*descriptor.FunctionDescriptor]Target{}}
	names := make([]string, 0, len(apps))
	for n := range apps {
		names = append(names, n)
	}
	sort.Strings(names)
	seen := map[string]string{}
	for _, name := range names {
		for _, r := range apps[name] {
			key := r.HTTPMethod + " " + r.Path
			if owner, ok := seen[key]; ok {
				return nil, &ConflictError{fmt.Sprintf("%s already owned by %s", key, owner)}
			}
			seen[key] = name
			fn := &descriptor.FunctionDescriptor{Name: name}
			t.targets[fn] = Target{AppName: name, Route: r}
			var route descriptor.Route
			switch r.HTTPMethod {
			case "GET":
				route = descriptor.NewAPIGet(r.Path, fn)
			case "POST":
				route = descriptor.NewAPIPost(r.Path, fn)
			case "PUT":
				route = descriptor.NewAPIPut(r.Path, fn)
			case "DELETE":
				route = descriptor.NewAPIDelete(r.Path, fn)
			default:
				return nil, fmt.Errorf("unsupported HTTP method")
			}
			t.router.Handle(route)
			t.routes = append(t.routes, r)
		}
	}
	return t, nil
}
func (t *table) Match(method, path string) (Target, bool) {
	req := &descriptor.HTTPRequest{Request: &http.Request{Method: method, URL: &url.URL{Path: path}}}
	fn, e := t.router.Lookup(req)
	if req.Params != nil {
		req.Params.Recycle()
	}
	if e != nil {
		return Target{}, false
	}
	target, ok := t.targets[fn]
	return target, ok
}
func (t *table) Routes() []idl.Route { return append([]idl.Route(nil), t.routes...) }
