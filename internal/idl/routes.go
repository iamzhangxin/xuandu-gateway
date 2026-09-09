package idl

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cloudwego/thriftgo/parser"
)

type Route struct {
	RequireLogin bool   `json:"requireLogin"`
	IDLPath      string `json:"idlPath"`
	HTTPMethod   string `json:"httpMethod"`
	Path         string `json:"path"`
	RPCService   string `json:"rpcService"`
	RPCMethod    string `json:"rpcMethod"`
}

func ExtractRoutes(b *Bundle) ([]Route, error) {
	ast, e := parser.ParseString(b.MainPath, b.Files[b.MainPath])
	if e != nil {
		return nil, fmt.Errorf("invalid thrift IDL")
	}
	if len(ast.Services) != 1 {
		return nil, fmt.Errorf("V1 requires exactly one service in main IDL")
	}
	svc := ast.Services[0]
	if svc.Extends != "" {
		return nil, fmt.Errorf("service inheritance is not supported in V1")
	}
	var routes []Route
	seen := map[string]bool{}
	for _, fn := range svc.Functions {
		count := 0
		requireLogin, authSeen := false, false
		for _, a := range fn.Annotations {
			if a.Key != "xuandu.Auth" {
				continue
			}
			if authSeen || len(a.Values) != 1 || (a.Values[0] != "required" && a.Values[0] != "optional") {
				return nil, fmt.Errorf("xuandu.Auth must be required or optional, declared once")
			}
			authSeen, requireLogin = true, a.Values[0] == "required"
		}
		for _, a := range fn.Annotations {
			method := ""
			switch a.Key {
			case "api.get", "api.post", "api.put", "api.delete":
				method = strings.ToUpper(strings.TrimPrefix(a.Key, "api."))
			}
			if method == "" {
				if a.Key == "api.patch" || a.Key == "api.head" || a.Key == "api.options" {
					return nil, fmt.Errorf("HTTP method annotation unsupported by pinned Kitex")
				}
				continue
			}

			if len(fn.Arguments) != 1 || !b.isStruct(b.MainPath, fn.Arguments[0].Type.Name, 0) || fn.Oneway {
				return nil, fmt.Errorf("HTTP RPC must have exactly one struct argument and cannot be oneway")
			}
			if fn.FunctionType == nil || !b.isStruct(b.MainPath, fn.FunctionType.Name, 0) {
				return nil, fmt.Errorf("HTTP RPC response must be a struct")
			}
			count++
			if len(a.Values) != 1 {
				return nil, fmt.Errorf("HTTP annotation needs one path")
			}
			p := a.Values[0]
			if !strings.HasPrefix(p, "/") || strings.ContainsAny(p, "?#") {
				return nil, fmt.Errorf("invalid HTTP path")
			}
			if p == "/healthz" || p == "/readyz" {
				return nil, fmt.Errorf("reserved health route")
			}
			key := method + " " + p
			if seen[key] {
				return nil, fmt.Errorf("duplicate HTTP route")
			}
			seen[key] = true
			routes = append(routes, Route{RequireLogin: requireLogin, HTTPMethod: method, Path: p, RPCService: svc.Name, RPCMethod: fn.Name, IDLPath: b.MainPath})
		}
		if count > 1 {
			return nil, fmt.Errorf("one HTTP annotation per RPC method required")
		}
	}

	return routes, nil
}

func (b *Bundle) isStruct(path, name string, depth int) bool {
	if depth > 32 {
		return false
	}
	ast, e := parser.ParseString(path, b.Files[path])
	if e != nil {
		return false
	}
	if prefix, rest, ok := strings.Cut(name, "."); ok {
		for _, inc := range ast.Includes {
			base := filepath.Base(inc.Path)
			if strings.TrimSuffix(base, filepath.Ext(base)) == prefix {
				return b.isStruct(filepath.Clean(filepath.Join(filepath.Dir(path), inc.Path)), rest, depth+1)
			}
		}
		return false
	}
	for _, s := range ast.Structs {
		if s.Name == name {
			return true
		}
	}
	for _, alias := range ast.Typedefs {
		if alias.Alias == name && alias.Type != nil {
			return b.isStruct(path, alias.Type.Name, depth+1)
		}
	}
	return false
}
