package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/iamzhangxin/xuandu-gateway/internal/idl"
)

type schema = map[string]any
type parameter struct {
	Name    string `json:"name"`
	In      string `json:"in"`
	Schema  schema `json:"schema"`
	Explode *bool  `json:"explode"`
	Style   string `json:"style"`
}
type media struct {
	Schema schema `json:"schema"`
}
type content struct {
	Content map[string]media `json:"content"`
}
type operation struct {
	Id          string             `json:"operationId"`
	Summary     string             `json:"summary"`
	Description string             `json:"description"`
	Parameters  []parameter        `json:"parameters"`
	RequestBody *content           `json:"requestBody"`
	Responses   map[string]content `json:"responses"`
	Idl         string             `json:"x-idl-file"`
	RpcMethod   string             `json:"x-rpc-method"`
	Tags        []string           `json:"tags"`
}
type document struct {
	Paths      map[string]map[string]operation `json:"paths"`
	Components struct {
		Schemas map[string]schema `json:"schemas"`
	} `json:"components"`
}
type Binding struct {
	Config                          ToolConfig
	Route                           idl.Route
	Path                            string
	Parameters                      []parameter
	Body                            bool
	Grouped                         bool
	Input, Output                   schema
	inputValidator, outputValidator *jsonschema.Resolved
}

func object() schema {
	return schema{"type": "object", "properties": schema{}, "additionalProperties": false}
}
func properties(s schema) schema { return s["properties"].(map[string]any) }
func documentFrom(v any) (document, error) {
	var d document
	b, e := json.Marshal(v)
	if e == nil {
		e = json.Unmarshal(b, &d)
	}
	return d, e
}
func compile(t ToolConfig, d document, routes []idl.Route, authHeader string) (*Binding, error) {
	b := &Binding{Config: t}
	var op operation
	found := false
	for path, methods := range d.Paths {
		for method, v := range methods {
			if v.Id != t.OperationId {
				continue
			}
			if found {
				return nil, fmt.Errorf("operationId 重复")
			}
			found = true
			op = v
			b.Path = path
			for _, r := range routes {
				if r.IDLPath == v.Idl && r.RPCMethod == v.RpcMethod && len(v.Tags) > 0 && r.RPCService == v.Tags[0] && r.HTTPMethod == strings.ToUpper(method) && openapiPath(r.Path) == path {
					b.Route = r
				}
			}
		}
	}
	if !found || b.Route.Path == "" {
		return nil, fmt.Errorf("接口已移除或无法绑定，请重新选择 OpenAPI 接口")
	}
	seen := map[string]bool{}
	for _, p := range op.Parameters {
		if p.In == "header" && (sensitiveHeader(p.Name) || strings.EqualFold(p.Name, authHeader)) {
			continue
		}
		if p.In != "path" && p.In != "query" && p.In != "header" {
			return nil, fmt.Errorf("暂不支持 Cookie 参数")
		}
		if p.Style != "" && p.Style != "form" && p.Style != "simple" {
			return nil, fmt.Errorf("不支持该参数序列化方式")
		}
		if seen[p.Name] {
			b.Grouped = true
		}
		seen[p.Name] = true
		b.Parameters = append(b.Parameters, p)
	}
	var body schema
	if op.RequestBody != nil {
		m, ok := op.RequestBody.Content["application/json"]
		if !ok || b.Route.HTTPMethod == "GET" {
			return nil, fmt.Errorf("仅支持 JSON Body，GET 不支持 Body")
		}
		body = m.Schema
		b.Body = true
		if len(b.Parameters) > 0 {
			b.Grouped = true
		}
	}
	root := object()
	if b.Body && !b.Grouped {
		root = body
	} else {
		for _, p := range b.Parameters {
			group := root
			if b.Grouped {
				key := parameterGroup(p.In)
				if properties(root)[key] == nil {
					properties(root)[key] = object()
				}
				group = properties(root)[key].(map[string]any)
			}
			properties(group)[p.Name] = p.Schema
		}
		if b.Body {
			properties(root)["body"] = body
		}
	}
	var e error
	b.Input, e = closeSchema(root, d.Components.Schemas, true)
	if e != nil {
		return nil, e
	}
	if b.Input["type"] != "object" {
		return nil, fmt.Errorf("工具参数必须为对象")
	}
	m, ok := op.Responses["200"].Content["application/json"]
	if !ok {
		return nil, fmt.Errorf("缺少 JSON 成功响应")
	}
	b.Output, e = closeSchema(m.Schema, d.Components.Schemas, false)
	if e != nil {
		return nil, e
	}
	b.inputValidator, e = validator(b.Input)
	if e != nil {
		return nil, fmt.Errorf("输入 Schema 不支持: %w", e)
	}
	b.outputValidator, e = validator(b.Output)
	if e != nil {
		return nil, fmt.Errorf("输出 Schema 不支持: %w", e)
	}
	if b.Config.Description == "" {
		b.Config.Description = op.Description
		if b.Config.Description == "" {
			b.Config.Description = op.Summary
		}
	}
	return b, nil
}
func openapiPath(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		if strings.HasPrefix(s, ":") || strings.HasPrefix(s, "*") {
			parts[i] = "{" + s[1:] + "}"
		}
	}
	return strings.Join(parts, "/")
}
func parameterGroup(in string) string {
	if in == "header" {
		return "headers"
	}
	return in
}

// Only schema keywords are traversed. Literal example/default data is never rewritten.
func closeSchema(root schema, components map[string]schema, input bool) (schema, error) {
	defs := schema{}
	visiting := map[string]bool{}
	var walk func(schema) (schema, error)
	walk = func(s schema) (schema, error) {
		out := schema{}
		for k, v := range s {
			switch k {
			case "$id", "$anchor", "$dynamicRef", "$dynamicAnchor":
				return nil, fmt.Errorf("不支持动态或外部 Schema 引用")
			case "required", "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "minLength", "maxLength", "minItems", "maxItems", "pattern", "enum", "const":
				if !input {
					out[k] = v
				}
			case "nullable":
			case "$ref":
				ref, ok := v.(string)
				if !ok || !strings.HasPrefix(ref, "#/components/schemas/") {
					return nil, fmt.Errorf("仅支持契约内部 Schema 引用")
				}
				key := strings.TrimPrefix(ref, "#/components/schemas/")
				name := strings.ReplaceAll(strings.ReplaceAll(key, "~1", "/"), "~0", "~")
				target, ok := components[name]
				if !ok {
					return nil, fmt.Errorf("Schema 引用缺失")
				}
				out[k] = "#/$defs/" + key
				if !visiting[name] {
					visiting[name] = true
					copy, e := walk(target)
					if e != nil {
						return nil, e
					}
					defs[name] = copy
				}
			case "properties", "patternProperties":
				m, ok := v.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("Schema 属性无效")
				}
				copy := schema{}
				for name, child := range m {
					cs, ok := child.(map[string]any)
					if !ok {
						return nil, fmt.Errorf("Schema 属性无效")
					}
					cv, e := walk(cs)
					if e != nil {
						return nil, e
					}
					copy[name] = cv
				}
				out[k] = copy
			case "items", "additionalProperties", "not":
				if cs, ok := v.(map[string]any); ok {
					cv, e := walk(cs)
					if e != nil {
						return nil, e
					}
					out[k] = cv
				} else {
					out[k] = v
				}
			case "allOf", "anyOf", "oneOf":
				arr, ok := v.([]any)
				if !ok {
					return nil, fmt.Errorf("Schema 组合无效")
				}
				copy := []any{}
				for _, child := range arr {
					cs, ok := child.(map[string]any)
					if !ok {
						return nil, fmt.Errorf("Schema 组合无效")
					}
					cv, e := walk(cs)
					if e != nil {
						return nil, e
					}
					copy = append(copy, cv)
				}
				out[k] = copy
			default:
				out[k] = v
			}
		}
		if s["nullable"] == true {
			if t, ok := out["type"].(string); ok {
				out["type"] = []string{t, "null"}
			}
		}
		return out, nil
	}
	out, e := walk(root)
	if e != nil {
		return nil, e
	}
	if len(defs) > 0 {
		out["$defs"] = defs
	}
	encoded, _ := json.Marshal(out)
	if len(encoded) > 512<<10 {
		return nil, fmt.Errorf("Schema 超过 512 KiB")
	}
	return out, nil
}
func validator(s schema) (*jsonschema.Resolved, error) {
	b, e := json.Marshal(s)
	if e != nil {
		return nil, e
	}
	var js jsonschema.Schema
	if e = json.Unmarshal(b, &js); e != nil {
		return nil, e
	}
	return js.Resolve(nil)
}
func jsonValue(raw []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	if e := d.Decode(&v); e != nil {
		return nil, e
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil, fmt.Errorf("多余 JSON 数据")
	}
	return v, nil
}
func (b *Binding) Request(raw json.RawMessage) (*http.Request, error) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	value, e := jsonValue(raw)
	if e != nil {
		return nil, fmt.Errorf("参数必须是 JSON 对象")
	}
	args, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("参数必须是 JSON 对象")
	}
	if e = b.inputValidator.Validate(validationValue(value)); e != nil {
		return nil, fmt.Errorf("参数类型与接口不一致")
	}
	path := b.Path
	query := url.Values{}
	headers := http.Header{}
	for _, p := range b.Parameters {
		src := args
		if b.Grouped {
			src, _ = args[parameterGroup(p.In)].(map[string]any)
		}
		v, exists := src[p.Name]
		if !exists || v == nil {
			if p.In == "path" {
				return nil, fmt.Errorf("缺少路径参数 %s", p.Name)
			}
			continue
		}
		values, e := scalarValues(v)
		if e != nil {
			return nil, e
		}
		switch p.In {
		case "query":
			if p.Explode != nil && !*p.Explode {
				query.Set(p.Name, strings.Join(values, ","))
			} else {
				query[p.Name] = values
			}
		case "header":
			if len(values) != 1 {
				return nil, fmt.Errorf("Header 必须是标量")
			}
			if strings.ContainsAny(values[0], "\r\n") {
				return nil, fmt.Errorf("Header 无效")
			}
			headers.Set(p.Name, values[0])
		case "path":
			if len(values) != 1 || values[0] == "" || strings.ContainsAny(values[0], "/\\") || values[0] == "." || values[0] == ".." {
				return nil, fmt.Errorf("路径参数无效")
			}
			path = strings.ReplaceAll(path, "{"+p.Name+"}", url.PathEscape(values[0]))
		}
	}
	var payload []byte
	if b.Body {
		var body any = args
		if b.Grouped {
			body = args["body"]
		}
		if body != nil {
			payload, e = json.Marshal(body)
			if e != nil {
				return nil, e
			}
		}
	}
	// This request is an in-memory generic codec input; it is never sent via http.Client.
	req, e := http.NewRequest(b.Route.HTTPMethod, "http://gateway.invalid"+path+"?"+query.Encode(), bytes.NewReader(payload))
	if e != nil {
		return nil, fmt.Errorf("无法构造接口参数")
	}
	req.Header = headers
	if b.Body {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}
func scalarValues(v any) ([]string, error) {
	switch x := v.(type) {
	case string:
		return []string{x}, nil
	case bool:
		if x {
			return []string{"true"}, nil
		}
		return []string{"false"}, nil
	case json.Number:
		return []string{x.String()}, nil
	case []any:
		out := []string{}
		for _, item := range x {
			values, e := scalarValues(item)
			if e != nil || len(values) != 1 {
				return nil, fmt.Errorf("查询数组必须包含标量")
			}
			out = append(out, values[0])
		}
		return out, nil
	}
	return nil, fmt.Errorf("参数位置只支持标量或标量数组")
}
func sortedBindings(bindings map[string]*Binding) []*Binding {
	out := []*Binding{}
	for _, b := range bindings {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Config.Name < out[j].Config.Name })
	return out
}

// jsonschema-go v0.4.3 treats json.Number as a string for type checks. Convert a
// validation-only copy; binding and output retain their original number tokens.
func validationValue(v any) any {
	switch x := v.(type) {
	case json.Number:
		if n, e := strconv.ParseInt(x.String(), 10, 64); e == nil {
			return n
		}
		if n, e := strconv.ParseUint(x.String(), 10, 64); e == nil {
			return n
		}
		if n, e := x.Float64(); e == nil {
			return n
		}
		return nil
	case map[string]any:
		out := map[string]any{}
		for k, v := range x {
			out[k] = validationValue(v)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, v := range x {
			out[i] = validationValue(v)
		}
		return out
	default:
		return v
	}
}
