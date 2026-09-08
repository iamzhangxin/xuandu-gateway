package mcp

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/iamzhangxin/xuandu-gateway/internal/config"
	"github.com/iamzhangxin/xuandu-gateway/internal/executor"
	"github.com/iamzhangxin/xuandu-gateway/internal/metadata"
	"github.com/iamzhangxin/xuandu-gateway/internal/openapi"
	rt "github.com/iamzhangxin/xuandu-gateway/internal/runtime"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type compiledServer struct {
	Config   ServerConfig
	Revision string
	Bindings map[string]*Binding
	Views    map[string]*sdk.Server
	Handler  http.Handler
	Error    string
}
type snapshot struct {
	Catalog        Catalog
	Servers        map[string]*compiledServer
	Endpoints      map[string]string
	Fresh          time.Time
	RuntimeVersion uint64
}
type Service struct {
	repository     Repository
	contracts      metadata.Store
	manager        *rt.Manager
	config         config.McpConfig
	mu             sync.Mutex
	current        atomic.Pointer[snapshot]
	server         *http.Server
	listenerFailed atomic.Bool
}

func NewService(c *config.Config, repository *ConsulRepository, contracts metadata.Store, manager *rt.Manager) *Service {
	s := &Service{repository: repository, contracts: contracts, manager: manager, config: c.Mcp}
	s.server = &http.Server{Addr: c.Mcp.Address, Handler: s, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	return s
}
func (s *Service) Refresh(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, e := s.repository.Load(ctx)
	if e != nil {
		return e
	}
	if e = validate(c); e != nil {
		return e
	}
	// A successful metadata read, not a pending blocking watch, establishes security freshness.
	apps, e := s.contracts.List(ctx)
	if e != nil {
		return fmt.Errorf("应用配置暂不可用")
	}
	lease := s.manager.Acquire()
	if lease == nil {
		return fmt.Errorf("网关正在关闭")
	}
	defer lease.Release()
	next := s.build(ctx, c, apps, lease.Snapshot)
	s.current.Store(next)
	return nil
}
func (s *Service) build(ctx context.Context, c Catalog, apps []*metadata.App, runtime *rt.Snapshot) *snapshot {
	out := &snapshot{Catalog: c, Servers: map[string]*compiledServer{}, Endpoints: map[string]string{}, Fresh: time.Now(), RuntimeVersion: runtime.Version}
	old := s.current.Load()
	enabled := map[string]bool{}
	for _, a := range apps {
		enabled[a.Name] = a.Enabled
	}
	docs := map[string]document{}
	for _, cfg := range c.Servers {
		cs := &compiledServer{Config: cfg, Bindings: map[string]*Binding{}, Views: map[string]*sdk.Server{}}
		out.Servers[cfg.Id] = cs
		for _, url := range cfg.Endpoints {
			k, _ := endpointKey(url)
			out.Endpoints[k] = cfg.Id
		}
		if !cfg.Enabled {
			continue
		}
		r := runtime.Runtimes[cfg.App]
		if r == nil || !enabled[cfg.App] {
			cs.Error = "关联应用尚未就绪或已禁用"
			continue
		}
		cs.Revision = r.Revision
		if old != nil && old.Catalog.Version == c.Version && old.RuntimeVersion == runtime.Version {
			if previous := old.Servers[cfg.Id]; previous != nil && previous.Error == "" && previous.Revision == r.Revision {
				out.Servers[cfg.Id] = previous
				continue
			}
		}
		d, ok := docs[cfg.App]
		if !ok {
			contract, e := s.contracts.GetContract(ctx, r.Revision)
			if e != nil {
				cs.Error = "已保存的契约暂不可用"
				continue
			}
			v, e := openapi.Generate(cfg.App, r.Revision, contract.Files)
			if e != nil {
				cs.Error = "OpenAPI 生成失败"
				continue
			}
			d, e = documentFrom(v)
			if e != nil {
				cs.Error = "OpenAPI 解析失败"
				continue
			}
			docs[cfg.App] = d
		}
		count := 0
		definitionBytes := 0
		for _, t := range c.Tools {
			if t.ServerId != cfg.Id {
				continue
			}
			count++
			if count > 100 {
				cs.Error = "每个 MCP 服务最多 100 个工具"
				break
			}
			b, e := compile(t, d, r.Routes, cfg.Header)
			if e != nil {
				cs.Error = t.Name + ": " + e.Error()
				break
			}
			encoded, _ := json.Marshal([]any{b.Input, b.Output, b.Config.Description})
			definitionBytes += len(encoded)
			if definitionBytes > 4<<20 {
				cs.Error = "工具定义总大小超过 4 MiB"
				break
			}
			cs.Bindings[t.Id] = b
		}
		if cs.Error != "" {
			continue
		}
		for _, key := range c.Keys {
			if !key.Enabled {
				continue
			}
			for _, grant := range key.Grants {
				if grant.ServerId != cfg.Id {
					continue
				}
				view := sdk.NewServer(&sdk.Implementation{Name: cfg.Name, Version: "1.0.0"}, &sdk.ServerOptions{HasTools: true})
				for _, b := range sortedBindings(cs.Bindings) {
					if !slices.Contains(grant.ToolIds, b.Config.Id) {
						continue
					}
					binding := b
					view.AddTool(&sdk.Tool{Name: b.Config.Name, Description: b.Config.Description, InputSchema: b.Input, OutputSchema: b.Output}, func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
						return s.call(ctx, binding, req.Params.Arguments), nil
					})
				}
				cs.Views[key.Id] = view
			}
		}
		cs.Handler = sdk.NewStreamableHTTPHandler(func(req *http.Request) *sdk.Server {
			scope, _ := req.Context().Value(scopeKey{}).(*requestScope)
			if scope == nil {
				return nil
			}
			return cs.Views[scope.KeyId]
		}, &sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	}
	return out
}
func (s *Service) Start(ctx context.Context) {
	go func() {
		for {
			work, cancel := context.WithTimeout(ctx, 10*time.Second)
			e := s.Refresh(work)
			cancel()
			if e != nil && ctx.Err() == nil {
				slog.Warn("mcp metadata refresh failed")
			}
			timer := time.NewTimer(5 * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
	if s.config.Enabled {
		listener, e := net.Listen("tcp", s.config.Address)
		if e != nil {
			s.listenerFailed.Store(true)
			slog.Error("mcp listener unavailable", "address", s.config.Address)
			return
		}
		go func() {
			if e := s.server.Serve(listener); e != nil && e != http.ErrServerClosed {
				s.listenerFailed.Store(true)
				slog.Error("mcp listener stopped")
			}
		}()
	}

}
func (s *Service) Shutdown(ctx context.Context) error { return s.server.Shutdown(ctx) }

type Status struct {
	ServerId  string `json:"serverId"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
	Revision  string `json:"revision"`
	ToolCount int    `json:"toolCount"`
}
type View struct {
	Catalog
	ListenerEnabled bool     `json:"listenerEnabled"`
	ListenerError   string   `json:"listenerError,omitempty"`
	Statuses        []Status `json:"statuses"`
}

func (s *Service) View() (View, error) {
	p := s.current.Load()
	if p == nil {
		return View{}, fmt.Errorf("MCP 配置尚未加载")
	}
	v := View{Catalog: safeCatalog(p.Catalog), ListenerEnabled: s.config.Enabled, Statuses: []Status{}}
	if s.listenerFailed.Load() {
		v.ListenerError = "MCP 监听启动失败，请检查端口占用和启动日志"
	}
	for _, cfg := range p.Catalog.Servers {
		cs := p.Servers[cfg.Id]
		status := "ready"
		if s.listenerFailed.Load() {
			status = "unavailable"
		} else if !s.config.Enabled {
			status = "listener_off"
		} else if !cfg.Enabled {
			status = "disabled"
		} else if cs.Error != "" || time.Since(p.Fresh) > 60*time.Second {
			status = "unavailable"
		}
		v.Statuses = append(v.Statuses, Status{cfg.Id, status, cs.Error, cs.Revision, len(cs.Bindings)})
	}
	return v, nil
}
func (s *Service) Tools(id string) ([]any, error) {
	p := s.current.Load()
	if p == nil || p.Servers[id] == nil {
		return nil, fmt.Errorf("MCP 服务不存在")
	}
	out := []any{}
	for _, b := range sortedBindings(p.Servers[id].Bindings) {
		out = append(out, map[string]any{"id": b.Config.Id, "name": b.Config.Name, "inputSchema": b.Input, "outputSchema": b.Output, "route": b.Route})
	}
	return out, nil
}
func (s *Service) Mutate(ctx context.Context, m Mutation) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, e := s.repository.Load(ctx)
	if e != nil {
		return nil, e
	}
	if c.Version != m.Version {
		return nil, ErrConflict
	}
	secret := ""
	id := m.Id
	exists := false
	switch m.Kind {
	case "access":
		for _, server := range c.Servers {
			if server.Id == id {
				exists = true
			}
		}
		access := map[string][]string{}
		for _, grant := range m.Access {
			if _, duplicate := access[grant.KeyId]; duplicate {
				return nil, fmt.Errorf("Key 授权重复")
			}
			found := false
			for _, key := range c.Keys {
				if key.Id == grant.KeyId {
					found = true
				}
			}
			if !found {
				return nil, fmt.Errorf("Key 不存在")
			}
			access[grant.KeyId] = grant.ToolIds
		}
		for i := range c.Keys {
			c.Keys[i].Grants = slices.DeleteFunc(c.Keys[i].Grants, func(g Grant) bool { return g.ServerId == id })
			if toolIds, ok := access[c.Keys[i].Id]; ok {
				c.Keys[i].Grants = append(c.Keys[i].Grants, Grant{ServerId: id, ToolIds: toolIds})
			}
		}

	case "server":
		for i, v := range c.Servers {
			if v.Id != id {
				continue
			}
			exists = true
			if m.Delete {
				c.Servers = slices.Delete(c.Servers, i, i+1)
			} else {
				m.Server.Id = id
				c.Servers[i] = m.Server
			}
			break
		}
		if id == "" && !m.Delete {
			id = randomId()
			m.Server.Id = id
			c.Servers = append(c.Servers, m.Server)
			exists = true
		}
		if m.Delete {
			c.Tools = slices.DeleteFunc(c.Tools, func(t ToolConfig) bool { return t.ServerId == id })
			for i := range c.Keys {
				c.Keys[i].Grants = slices.DeleteFunc(c.Keys[i].Grants, func(g Grant) bool { return g.ServerId == id })
			}
		}
	case "tool":
		for i, v := range c.Tools {
			if v.Id != id {
				continue
			}
			exists = true
			if m.Delete {
				c.Tools = slices.Delete(c.Tools, i, i+1)
			} else {
				m.Tool.Id = id
				c.Tools[i] = m.Tool
			}
			break
		}
		if id == "" && !m.Delete {
			id = randomId()
			m.Tool.Id = id
			c.Tools = append(c.Tools, m.Tool)
			exists = true
		}
		if m.Delete {
			for i := range c.Keys {
				for j := range c.Keys[i].Grants {
					c.Keys[i].Grants[j].ToolIds = slices.DeleteFunc(c.Keys[i].Grants[j].ToolIds, func(v string) bool { return v == id })
				}
			}
		}
	case "key":
		m.Key.Hash = ""
		for i, v := range c.Keys {
			if v.Id != id {
				continue
			}
			exists = true
			if m.Delete {
				c.Keys = slices.Delete(c.Keys, i, i+1)
			} else {
				m.Key.Id = id
				m.Key.Hash = v.Hash
				if m.Rotate {
					secret = "xuandu_" + hexSecret()
					m.Key.Hash = keyHash(secret)
				}
				c.Keys[i] = m.Key
			}
			break
		}
		if id == "" && !m.Delete {
			id = randomId()
			secret = "xuandu_" + hexSecret()
			m.Key.Id = id
			m.Key.Hash = keyHash(secret)
			c.Keys = append(c.Keys, m.Key)
			exists = true
		}
	default:
		return nil, fmt.Errorf("未知操作")
	}
	if !exists {
		return nil, fmt.Errorf("配置不存在")
	}
	if e = validate(c); e != nil {
		return nil, e
	}
	apps, e := s.contracts.List(ctx)
	if e != nil {
		return nil, fmt.Errorf("应用配置不可用")
	}
	lease := s.manager.Acquire()
	if lease == nil {
		return nil, fmt.Errorf("网关正在关闭")
	}
	defer lease.Release()
	// Do not reuse previous SDK views while validating a pending change.
	c.Version = 0
	candidate := s.build(ctx, c, apps, lease.Snapshot)
	if !m.Delete && (m.Kind == "tool" || m.Kind == "server") {
		serverId := id
		if m.Kind == "tool" {
			serverId = m.Tool.ServerId
		}
		cs := candidate.Servers[serverId]
		if cs != nil && cs.Config.Enabled && cs.Error != "" {
			return nil, fmt.Errorf("%s", cs.Error)
		}
	}
	if e = s.repository.Save(ctx, c, m.Version); e != nil {
		return nil, e
	}
	// Publish only the CAS-winning metadata. Stop serving old privileges if reread fails.
	saved, e := s.repository.Load(ctx)
	if e != nil {
		s.current.Store(nil)
		return nil, e
	}
	if e = validate(saved); e != nil {
		s.current.Store(nil)
		return nil, e
	}
	next := s.build(ctx, saved, apps, lease.Snapshot)
	s.current.Store(next)
	return map[string]any{"id": id, "version": saved.Version, "secret": secret}, nil
}
func hexSecret() string { return fmt.Sprintf("%x", randomBytes(32)) }

type scopeKey struct{}
type requestScope struct {
	Snapshot        *rt.Snapshot
	Runtime         *rt.ServiceRuntime
	KeyId, ServerId string
	Context         context.Context
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		w.WriteHeader(http.StatusOK)
		return
	}
	p := s.current.Load()
	if p == nil || time.Since(p.Fresh) > 60*time.Second {
		http.Error(w, "MCP metadata unavailable", 503)
		return
	}
	id := p.Endpoints[r.URL.EscapedPath()]
	cs := p.Servers[id]
	if cs == nil || !cs.Config.Enabled {
		http.NotFound(w, r)
		return
	}
	lease := s.manager.Acquire()
	if lease == nil {
		http.Error(w, "Gateway unavailable", 503)
		return
	}
	defer lease.Release()
	runtime := lease.Snapshot.Runtimes[cs.Config.App]
	if runtime == nil || runtime.Revision != cs.Revision {
		http.Error(w, "MCP contract synchronizing", 503)
		return
	}
	domain, err := metadata.RequestDomain(r.Host)
	if err != nil || lease.Snapshot.Domains[domain] != cs.Config.App {
		slog.Warn("mcp request rejected", "server_id", id, "app", cs.Config.App, "error_reason", "domain_mismatch", "host", r.Host, "status", 403)
		http.Error(w, "MCP domain forbidden", http.StatusForbidden)
		return
	}
	// Browser access is deliberately same-origin in v1. Non-browser clients omit Origin.
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || strings.Contains(origin, "#") || !strings.EqualFold(u.Host, r.Host) {
			http.Error(w, "Origin forbidden", 403)
			return
		}
	}
	values := r.Header.Values(cs.Config.Header)
	if len(values) != 1 {
		http.Error(w, "MCP key required", 401)
		return
	}
	token := values[0]
	if cs.Config.Bearer {
		kind, value, ok := strings.Cut(token, " ")
		if !ok || !strings.EqualFold(kind, "Bearer") {
			http.Error(w, "Invalid MCP key", 401)
			return
		}
		token = value
	}
	if len(token) > 256 {
		http.Error(w, "Invalid MCP key", 401)
		return
	}
	hash := keyHash(token)
	keyId := ""
	for _, key := range p.Catalog.Keys {
		if subtle.ConstantTimeCompare([]byte(hash), []byte(key.Hash)) == 1 && key.Enabled {
			keyId = key.Id
		}
	}
	if keyId == "" {
		http.Error(w, "Invalid MCP key", 401)
		return
	}
	if cs.Error != "" {
		http.Error(w, "MCP service unavailable", 503)
		return
	}
	if cs.Views[keyId] == nil {
		http.Error(w, "MCP access denied", 403)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	scope := &requestScope{Runtime: runtime, KeyId: keyId, ServerId: id, Context: ctx, Snapshot: lease.Snapshot}
	ctx = context.WithValue(ctx, scopeKey{}, scope)
	r = r.WithContext(ctx)
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	cs.Handler.ServeHTTP(w, r)
}
func (s *Service) call(ctx context.Context, b *Binding, raw json.RawMessage) *sdk.CallToolResult {
	start := time.Now()
	scope, _ := ctx.Value(scopeKey{}).(*requestScope)
	businessCode := "500001"
	failure := func(code, msg string) *sdk.CallToolResult {
		businessCode = code
		v, _ := json.Marshal(executor.Response{Code: code, Message: msg})
		return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: string(v)}}}
	}
	if scope == nil || scope.ServerId != b.Config.ServerId {
		return failure("403001", "MCP access denied")
	}
	requestId := randomId()
	defer func() {
		level := slog.LevelInfo
		if businessCode != "000000" {
			level = slog.LevelWarn
			if strings.HasPrefix(businessCode, "5") {
				level = slog.LevelError
			}
		}
		slog.Log(context.WithoutCancel(scope.Context), level, "mcp request", "request_id", requestId, "server_id", scope.ServerId, "key_id", scope.KeyId, "app", scope.Runtime.AppName, "tool", b.Config.Name, "rpc_service", b.Route.RPCService, "rpc_method", b.Route.RPCMethod, "revision", scope.Runtime.Revision, "duration_ms", float64(time.Since(start))/float64(time.Millisecond), "code", businessCode)
	}()
	// The server view and runtime were both selected before SDK dispatch and held for the whole request.
	req, e := b.Request(raw)
	if e != nil {
		return failure("400001", e.Error())
	}
	target, ok := scope.Snapshot.Match(scope.Runtime.AppName, req.Method, req.URL.Path)
	if !ok || target.AppName != scope.Runtime.AppName || target.Route != b.Route {
		return failure("400001", "参数未匹配所选接口")
	}
	result := executor.Execute(scope.Context, scope.Runtime, req, "")

	if result.Code != "000000" {
		return failure(result.Code, result.Message)
	}
	if len(result.Payload) > 2<<20 {
		return failure("500001", "MCP result exceeds size limit")
	}
	value, e := jsonValue(result.Payload)
	if e != nil || b.outputValidator.Validate(validationValue(value)) != nil {
		return failure("500001", "Upstream response does not match contract")
	}
	out := &sdk.CallToolResult{StructuredContent: json.RawMessage(result.Payload), Content: []sdk.Content{&sdk.TextContent{Text: string(result.Payload)}}}
	encoded, e := json.Marshal(out)
	if e != nil || len(encoded) > 4<<20 {
		return failure("500001", "MCP result exceeds size limit")
	}
	businessCode = "000000"
	return out
}
