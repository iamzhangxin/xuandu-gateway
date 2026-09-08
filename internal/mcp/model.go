// Package mcp implements a separately authorized MCP surface over persisted gateway contracts.
package mcp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	consul "github.com/hashicorp/consul/api"
	"github.com/iamzhangxin/xuandu-gateway/internal/config"
)

type ServerConfig struct {
	Id        string   `json:"id"`
	Name      string   `json:"name"`
	App       string   `json:"app"`
	Endpoints []string `json:"endpoints"`
	Header    string   `json:"header"`
	Bearer    bool     `json:"bearer"`
	Enabled   bool     `json:"enabled"`
}
type ToolConfig struct {
	Id          string `json:"id"`
	ServerId    string `json:"serverId"`
	Name        string `json:"name"`
	Description string `json:"description"`
	OperationId string `json:"operationId"`
}
type Grant struct {
	ServerId string   `json:"serverId"`
	ToolIds  []string `json:"toolIds"`
}
type KeyConfig struct {
	Id      string  `json:"id"`
	Name    string  `json:"name"`
	Enabled bool    `json:"enabled"`
	Hash    string  `json:"hash,omitempty"`
	Grants  []Grant `json:"grants"`
}
type Catalog struct {
	Version uint64         `json:"version"`
	Servers []ServerConfig `json:"servers"`
	Tools   []ToolConfig   `json:"tools"`
	Keys    []KeyConfig    `json:"keys"`
}
type ServerAccess struct {
	KeyId   string   `json:"keyId"`
	ToolIds []string `json:"toolIds"`
}
type Mutation struct {
	Access  []ServerAccess `json:"access"`
	Version uint64         `json:"version"`
	Kind    string         `json:"kind"`
	Delete  bool           `json:"delete"`
	Rotate  bool           `json:"rotate"`
	Id      string         `json:"id"`
	Server  ServerConfig   `json:"server"`
	Tool    ToolConfig     `json:"tool"`
	Key     KeyConfig      `json:"key"`
}
type Repository interface {
	Load(context.Context) (Catalog, error)
	Save(context.Context, Catalog, uint64) error
}
type ConsulRepository struct {
	client *consul.Client
	path   string
}

func NewRepository(c *config.Config, cc *consul.Config) (*ConsulRepository, error) {
	cl, e := consul.NewClient(cc)
	if e != nil {
		return nil, e
	}
	prefix := strings.Trim(c.Consul.MetadataPrefix, "/")
	key := prefix + "-mcp/catalog"
	if strings.HasSuffix(prefix, "/apps") {
		key = strings.TrimSuffix(prefix, "/apps") + "/mcp/catalog"
	}
	return &ConsulRepository{cl, key}, nil
}
func (r *ConsulRepository) Load(ctx context.Context) (Catalog, error) {
	p, _, e := r.client.KV().Get(r.path, (&consul.QueryOptions{RequireConsistent: true}).WithContext(ctx))
	if e != nil {
		return Catalog{}, fmt.Errorf("MCP 配置读取失败")
	}
	c := Catalog{Servers: []ServerConfig{}, Tools: []ToolConfig{}, Keys: []KeyConfig{}}
	if p == nil {
		return c, nil
	}
	if len(p.Value) > 256<<10 {
		return c, fmt.Errorf("MCP 配置超限")
	}
	if e = json.Unmarshal(p.Value, &c); e != nil {
		return c, fmt.Errorf("MCP 配置损坏")
	}
	c.Version = p.ModifyIndex
	return c, nil
}
func (r *ConsulRepository) Save(ctx context.Context, c Catalog, version uint64) error {
	c.Version = 0
	b, e := json.Marshal(c)
	if e != nil {
		return e
	}
	if len(b) > 256<<10 {
		return fmt.Errorf("MCP 配置超过 256 KiB")
	}
	ok, _, e := r.client.KV().CAS(&consul.KVPair{Key: r.path, Value: b, ModifyIndex: version}, (&consul.WriteOptions{}).WithContext(ctx))
	if e != nil {
		return fmt.Errorf("MCP 配置保存失败")
	}
	if !ok {
		return ErrConflict
	}
	return nil
}

var ErrConflict = fmt.Errorf("配置已被修改，请刷新后重试")
var toolName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)
var headerName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]{0,63}$`)

func randomId() string { return hex.EncodeToString(randomBytes(16)) }
func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return b
}
func keyHash(key string) string { sum := sha256.Sum256([]byte(key)); return hex.EncodeToString(sum[:]) }

// endpointKey accepts only absolute paths; full URLs are invalid configuration.
func endpointKey(raw string) (string, error) {
	u, e := url.Parse(raw)
	if e != nil || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") || u.Opaque != "" {
		return "", fmt.Errorf("MCP 路径不能包含查询参数或片段")
	}
	if u.IsAbs() || u.Host != "" || !strings.HasPrefix(raw, "/") {
		return "", fmt.Errorf("MCP 仅支持以 / 开头的路径，例如 /product/mcp；不支持完整 URL，请手动修改旧配置")
	}
	if u.Path == "" || u.Path == "/" || u.Path == "/healthz" || u.RawPath != "" || u.EscapedPath() != u.Path || strings.HasSuffix(u.Path, "/") || strings.ContainsAny(u.Path, "\\{}:*") || strings.Contains(u.Path, "//") {
		return "", fmt.Errorf("MCP 路径无效，不能使用根路径、/healthz 或以 / 结尾")
	}
	for _, segment := range strings.Split(u.Path, "/") {
		if segment == "." || segment == ".." {
			return "", fmt.Errorf("MCP 路径不允许 . 或 ..")
		}
	}
	return u.Path, nil
}

func sensitiveHeader(h string) bool {
	h = strings.ToLower(h)
	return h == "x-app-code" || h == "origin" || h == "accept" || h == "expect" || h == "upgrade" || h == "te" || h == "trailer" || h == "proxy-authorization" || h == "authorization" || h == "cookie" || h == "x-user-id" || h == "host" || h == "connection" || h == "content-length" || h == "content-type" || h == "transfer-encoding" || strings.HasPrefix(h, "mcp-") || strings.HasPrefix(h, "x-forwarded-") || strings.HasPrefix(h, "x-internal-") || h == "x-api-key" || h == "x-mcp-key"
}
func validate(c Catalog) error {
	if len(c.Servers) > 50 || len(c.Tools) > 500 || len(c.Keys) > 100 {
		return fmt.Errorf("MCP 配置数量超限（50 服务 / 500 工具 / 100 Key）")
	}
	servers := map[string]ServerConfig{}
	endpoints := map[string]bool{}
	ids := map[string]bool{}
	tools := map[string]ToolConfig{}
	names := map[string]bool{}
	for _, s := range c.Servers {
		if s.Id == "" || ids[s.Id] || strings.TrimSpace(s.Name) == "" || s.App == "" || len(s.Name) > 100 || len(s.Endpoints) == 0 || len(s.Endpoints) > 10 {
			return fmt.Errorf("MCP 服务名称、应用或路径无效")
		}
		ids[s.Id] = true
		servers[s.Id] = s
		if !headerName.MatchString(s.Header) || (sensitiveHeader(s.Header) && !strings.EqualFold(s.Header, "Authorization") && !strings.EqualFold(s.Header, "X-API-Key") && !strings.EqualFold(s.Header, "X-MCP-Key")) {
			return fmt.Errorf("认证请求头名称不可用")
		}
		if strings.EqualFold(s.Header, "Authorization") && !s.Bearer {
			return fmt.Errorf("Authorization 请求头须选择 Bearer 模式")
		}
		for _, raw := range s.Endpoints {
			k, e := endpointKey(raw)
			if e != nil {
				return e
			}
			if endpoints[k] {
				return fmt.Errorf("MCP 路径 %s 重复，不同服务必须使用不同路径", k)
			}
			endpoints[k] = true
		}
	}
	for _, t := range c.Tools {
		if t.Id == "" || ids[t.Id] || !toolName.MatchString(t.Name) || servers[t.ServerId].Id == "" || t.OperationId == "" || len(t.Description) > 4000 {
			return fmt.Errorf("工具名称、服务或接口无效")
		}
		ids[t.Id] = true
		key := t.ServerId + ":" + t.Name
		if names[key] {
			return fmt.Errorf("同一 MCP 服务中的工具名重复")
		}
		names[key] = true
		tools[t.Id] = t
	}
	for _, k := range c.Keys {
		if k.Id == "" || ids[k.Id] || strings.TrimSpace(k.Name) == "" || len(k.Name) > 100 || len(k.Hash) != 64 {
			return fmt.Errorf("Key 配置无效")
		}
		ids[k.Id] = true
		seen := map[string]bool{}
		for _, g := range k.Grants {
			if servers[g.ServerId].Id == "" || seen[g.ServerId] {
				return fmt.Errorf("Key 授权服务无效或重复")
			}
			seen[g.ServerId] = true
			for _, id := range g.ToolIds {
				if tools[id].Id == "" || tools[id].ServerId != g.ServerId {
					return fmt.Errorf("Key 只能授权对应 MCP 服务下的工具")
				}
			}
		}
	}
	return nil
}
func safeCatalog(c Catalog) Catalog {
	c.Keys = append([]KeyConfig{}, c.Keys...)
	for i := range c.Keys {
		c.Keys[i].Hash = ""
	}
	return c
}
