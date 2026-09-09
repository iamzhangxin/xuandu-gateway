// Package openapi derives documentation from persisted contracts without
// changing runtime routing or requiring business service connectivity.
package openapi

import (
	"encoding/base64"
	"fmt"
	"path"
	"reflect"
	"sort"
	"strings"

	"github.com/cloudwego/thriftgo/parser"
	"github.com/iamzhangxin/xuandu-gateway/internal/idl"
)

type Object = map[string]any
type generator struct {
	asts    map[string]*parser.Thrift
	schemas Object
	err     error
}

func comment(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "*/" {
			lines[i] = ""
			continue
		}
		for _, prefix := range []string{"/**", "/*", "//", "#", "*"} {
			if strings.HasPrefix(line, prefix) {
				line = strings.TrimPrefix(line, prefix)
				break
			}
		}
		lines[i] = strings.TrimSpace(strings.TrimSuffix(line, "*/"))
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
func annotation(a parser.Annotations, key string) string {
	for _, v := range a {
		if v.Key == key && len(v.Values) > 0 {
			return v.Values[0]
		}
	}
	return ""
}
func fieldName(f *parser.Field) string {
	if name := annotation(f.Annotations, "api.body"); name != "" {
		return name
	}
	tag := reflect.StructTag(annotation(f.Annotations, "go.tag")).Get("json")
	if name := strings.Split(tag, ",")[0]; name != "" && name != "-" {
		return name
	}
	return f.Name
}
func (g *generator) resolve(file, name string) (string, string) {
	if prefix, rest, ok := strings.Cut(name, "."); ok {
		for _, inc := range g.asts[file].Includes {
			if strings.TrimSuffix(path.Base(inc.Path), path.Ext(inc.Path)) == prefix {
				return path.Clean(path.Join(path.Dir(file), inc.Path)), rest
			}
		}
	}
	return file, name
}
func schemaKey(file, name string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(file)) + "." + name
}
func (g *generator) schema(file string, t *parser.Type) Object {
	if t == nil {
		return Object{}
	}
	switch t.Name {
	case "bool":
		return Object{"type": "boolean"}
	case "byte", "i8", "i16", "i32":
		return Object{"type": "integer", "format": "int32"}
	case "i64":
		return Object{"type": "integer", "format": "int64"}
	case "double":
		return Object{"type": "number", "format": "double"}
	case "string":
		return Object{"type": "string"}
	case "binary":
		return Object{"type": "string", "format": "byte"}
	case "list", "set":
		s := Object{"type": "array", "items": g.schema(file, t.ValueType)}
		if t.Name == "set" {
			s["uniqueItems"] = true
		}
		return s
	case "map":
		return Object{"type": "object", "additionalProperties": g.schema(file, t.ValueType)}
	}
	file, name := g.resolve(file, t.Name)
	key := schemaKey(file, name)
	ref := Object{"$ref": "#/components/schemas/" + key}
	if _, ok := g.schemas[key]; ok {
		return ref
	}
	g.schemas[key] = Object{} // Break recursive struct references.
	ast := g.asts[file]
	if ast == nil {
		g.err = fmt.Errorf("unresolved IDL file %s", file)
		return ref
	}
	for _, a := range ast.Typedefs {
		if a.Alias == name {
			g.schemas[key] = Object{"allOf": []any{g.schema(file, a.Type)}, "description": comment(a.ReservedComments), "title": name}
			return ref
		}
	}
	for _, e := range ast.Enums {
		if e.Name == name {
			values := []int64{}
			names := []string{}
			for _, v := range e.Values {
				values = append(values, v.Value)
				names = append(names, fmt.Sprintf("%s = %d %s", v.Name, v.Value, comment(v.ReservedComments)))
			}
			g.schemas[key] = Object{"type": "integer", "enum": values, "title": name, "description": strings.TrimSpace(comment(e.ReservedComments) + "\n" + strings.Join(names, "\n"))}
			return ref
		}
	}
	structs := append(append(append([]*parser.StructLike{}, ast.Structs...), ast.Unions...), ast.Exceptions...)
	for _, s := range structs {
		if s.Name == name {
			g.schemas[key] = g.fields(file, s.Fields)
			g.schemas[key].(Object)["title"] = name
			g.schemas[key].(Object)["description"] = comment(s.ReservedComments)
			return ref
		}
	}
	g.err = fmt.Errorf("unresolved IDL type %s", t.Name)
	return ref
}
func (g *generator) field(file string, f *parser.Field) Object {
	s := g.schema(file, f.Type)
	if annotation(f.Annotations, "api.js_conv") == "true" && f.Type.Name == "i64" {
		s = Object{"type": "string", "pattern": "^-?[0-9]+$", "description": "64 位整数以字符串传输"}
	}
	if description := comment(f.ReservedComments); description != "" {
		s = Object{"allOf": []any{s}, "description": description}
	}
	return s
}
func (g *generator) fields(file string, fields []*parser.Field) Object {
	properties := Object{}
	required := []string{}
	for _, f := range fields {
		name := fieldName(f)
		properties[name] = g.field(file, f)
		if f.Requiredness == parser.FieldType_Required {
			required = append(required, name)
		}
	}
	s := Object{"type": "object", "properties": properties}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}
func (g *generator) structFields(file string, t *parser.Type, depth int) (string, []*parser.Field) {
	if depth > 32 {
		return file, nil
	}
	file, name := g.resolve(file, t.Name)
	ast := g.asts[file]
	if ast == nil {
		return file, nil
	}
	for _, s := range ast.Structs {
		if s.Name == name {
			return file, s.Fields
		}
	}
	for _, a := range ast.Typedefs {
		if a.Alias == name {
			return g.structFields(file, a.Type, depth+1)
		}
	}
	return file, nil
}
func envelope(data any, success bool) Object {
	code := Object{"type": "string", "pattern": "^[0-9]{6}$"}
	msg := Object{"type": "string"}
	if success {
		code["enum"] = []string{"000000"}
		msg["example"] = "success"
	} else {
		code["example"] = "400001"
		msg["example"] = "invalid argument"
		data = Object{"type": "object", "nullable": true, "enum": []any{nil}}
	}
	return Object{"type": "object", "required": []string{"code", "message", "data"}, "properties": Object{"code": code, "message": msg, "data": data}}
}
func Generate(name, revision string, files map[string]string) (Object, error) {
	_, routes, err := idl.LoadArchive(files)
	if err != nil {
		return nil, err
	}
	g := &generator{asts: map[string]*parser.Thrift{}, schemas: Object{}}
	for file, content := range files {
		ast, err := parser.ParseString(file, content)
		if err != nil {
			return nil, err
		}
		g.asts[file] = ast
	}
	paths := Object{}
	tags := []Object{}
	seen := map[string]bool{}
	for _, route := range routes {
		ast := g.asts[route.IDLPath]
		var fn *parser.Function
		for _, s := range ast.Services {
			if s.Name == route.RPCService {
				if !seen[s.Name] {
					tags = append(tags, Object{"name": s.Name, "description": comment(s.ReservedComments)})
					seen[s.Name] = true
				}
				for _, f := range s.Functions {
					if f.Name == route.RPCMethod {
						fn = f
					}
				}
			}
		}
		if fn == nil {
			continue
		}
		segments := strings.Split(route.Path, "/")
		for i, s := range segments {
			if strings.HasPrefix(s, ":") || strings.HasPrefix(s, "*") {
				segments[i] = "{" + s[1:] + "}"
			}
		}
		httpPath := strings.Join(segments, "/")
		params := []Object{}
		bodyFields := []*parser.Field{}
		requestFile, fields := g.structFields(route.IDLPath, fn.Arguments[0].Type, 0)
		for _, f := range fields {
			location, paramName := "", ""
			for _, mapping := range []struct{ key, in string }{{"api.query", "query"}, {"api.path", "path"}, {"api.header", "header"}, {"api.cookie", "cookie"}} {
				if value := annotation(f.Annotations, mapping.key); value != "" {
					location, paramName = mapping.in, value
					break
				}
			}
			if location == "" {
				bodyFields = append(bodyFields, f)
				continue
			}
			params = append(params, Object{"name": paramName, "in": location, "required": location == "path" || f.Requiredness == parser.FieldType_Required, "description": comment(f.ReservedComments), "schema": g.field(requestFile, f)})
		}
		for _, segment := range segments {
			if strings.HasPrefix(segment, "{") {
				n := strings.TrimSuffix(strings.TrimPrefix(segment, "{"), "}")
				found := false
				for _, p := range params {
					if p["in"] == "path" && p["name"] == n {
						found = true
					}
				}
				if !found {
					params = append(params, Object{"name": n, "in": "path", "required": true, "schema": Object{"type": "string"}, "description": "路由占位符；IDL 未声明对应的 api.path 字段"})
				}
			}
		}
		hasUser := false
		for _, p := range params {
			if p["in"] == "header" && strings.EqualFold(p["name"].(string), "X-User-ID") {
				hasUser = true
				p["required"] = route.RequireLogin
			}
		}
		if !hasUser {
			params = append(params, Object{"name": "X-User-ID", "in": "header", "required": route.RequireLogin, "description": "由可信入口设置的用户 ID，透传到 rpcmeta.UserId(ctx)", "schema": Object{"type": "string"}})
		}
		for _, header := range []string{"X-Device-ID", "X-Device-Type", "X-Device-Name"} {
			exists := false
			for _, p := range params {
				if p["in"] == "header" && strings.EqualFold(p["name"].(string), header) {
					exists = true
				}
			}
			if !exists {
				params = append(params, Object{"name": header, "in": "header", "required": false, "description": "请求设备信息，持续透传到 Rpc 上下文", "schema": Object{"type": "string"}})
			}
		}
		description := comment(fn.ReservedComments)
		summary := strings.Split(description, "\n")[0]
		if summary == "" {
			summary = route.RPCService + "." + route.RPCMethod
		}
		operation := Object{"operationId": schemaKey(route.IDLPath, route.RPCService+"."+route.RPCMethod), "summary": summary, "description": description, "tags": []string{route.RPCService}, "parameters": params, "x-xuandu-auth": map[bool]string{true: "required", false: "optional"}[route.RequireLogin], "x-idl-file": route.IDLPath, "x-rpc-method": route.RPCMethod}
		if len(bodyFields) > 0 {
			bodyRequired := false
			for _, f := range bodyFields {
				if f.Requiredness == parser.FieldType_Required {
					bodyRequired = true
				}
			}
			operation["requestBody"] = Object{"required": bodyRequired, "content": Object{"application/json": Object{"schema": g.fields(requestFile, bodyFields)}}}
			if route.HTTPMethod == "GET" {
				operation["description"] = strings.TrimSpace(description + "\n注意：该 GET 接口的 IDL 包含请求体字段；查询参数需显式声明 api.query。")
			}
		}
		operation["responses"] = Object{"200": Object{"description": "成功；data 为下游返回对象", "content": Object{"application/json": Object{"schema": envelope(g.schema(route.IDLPath, fn.FunctionType), true)}}}, "default": Object{"description": "失败：code 为六位非零业务码，data 为 null；HTTP 状态保留错误语义", "content": Object{"application/json": Object{"schema": envelope(nil, false)}}}}
		if route.RequireLogin {
			operation["responses"].(Object)["401"] = Object{"description": "用户身份不能为空", "content": Object{"application/json": Object{"schema": envelope(nil, false), "example": Object{"code": "401003", "message": "用户身份不能为空", "data": nil}}}}
		}
		if paths[httpPath] == nil {
			paths[httpPath] = Object{}
		}
		paths[httpPath].(Object)[strings.ToLower(route.HTTPMethod)] = operation
	}
	sort.Slice(tags, func(i, j int) bool { return tags[i]["name"].(string) < tags[j]["name"].(string) })
	if g.err != nil {
		return nil, g.err
	}
	return Object{"openapi": "3.0.3", "info": Object{"title": name + " 接口文档", "version": revision, "description": "从已持久化的 Thrift IDL 生成。字段 required 仅反映 IDL 声明，不推测业务校验规则。"}, "paths": paths, "tags": tags, "components": Object{"schemas": g.schemas}}, nil
}
