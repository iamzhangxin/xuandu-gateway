package idl

import (
	"fmt"

	"github.com/cloudwego/thriftgo/parser"

	"path"
	"sort"
	"strings"
)

type Bundle struct {
	MainPath string
	Files    map[string]string
}

// LoadArchive parses ALL thrift files, validates include closure (even unused
// files), and returns each service-bearing IDL as a separate client bundle.
func LoadArchive(files map[string]string) ([]*Bundle, []Route, error) {
	asts := map[string]*parser.Thrift{}
	names := make([]string, 0, len(files))
	for name, body := range files {
		ast, e := parser.ParseString(name, body)
		if e != nil {
			return nil, nil, fmt.Errorf("invalid thrift IDL: %s", name)
		}
		asts[name] = ast
		names = append(names, name)
	}
	sort.Strings(names)
	visiting, done := map[string]bool{}, map[string]bool{}
	var check func(string) error
	check = func(name string) error {
		if visiting[name] {
			return fmt.Errorf("cyclic thrift include")
		}
		if done[name] {
			return nil
		}
		ast := asts[name]
		if ast == nil {
			return fmt.Errorf("missing thrift include")
		}
		visiting[name] = true
		for _, inc := range ast.Includes {
			target := path.Clean(path.Join(path.Dir(name), inc.Path))
			if path.IsAbs(inc.Path) || strings.Contains(inc.Path, "\\") || !strings.HasPrefix(target, "idl/") {
				return fmt.Errorf("include escapes idl/")
			}
			if e := check(target); e != nil {
				return e
			}
		}
		visiting[name] = false
		done[name] = true
		return nil
	}
	var bundles []*Bundle
	var routes []Route
	for _, name := range names {
		if e := check(name); e != nil {
			return nil, nil, e
		}
		if len(asts[name].Services) == 0 {
			continue
		}
		b := &Bundle{MainPath: name, Files: files}
		rs, e := ExtractRoutes(b)
		if e != nil {
			return nil, nil, e
		}
		if len(rs) == 0 {
			continue
		}
		bundles = append(bundles, b)
		routes = append(routes, rs...)
	}
	if len(routes) == 0 {
		return nil, nil, fmt.Errorf("ZIP has no HTTP routes")
	}
	return bundles, routes, nil
}
