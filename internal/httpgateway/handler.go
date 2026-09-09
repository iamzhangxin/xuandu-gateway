package httpgateway

import (
	"bytes"
	"context"
	"encoding/json"
	common "github.com/iamzhangxin/rpcxcommon/errors"
	"github.com/iamzhangxin/rpcxcommon/rpcmeta"
	"github.com/iamzhangxin/xuandu-gateway/internal/metadata"
	"log/slog"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/iamzhangxin/xuandu-gateway/internal/apperr"
	"github.com/iamzhangxin/xuandu-gateway/internal/config"
	"github.com/iamzhangxin/xuandu-gateway/internal/executor"
	rt "github.com/iamzhangxin/xuandu-gateway/internal/runtime"
)

type Response = executor.Response

type Handler struct {
	Manager  *rt.Manager
	limit    int
	sequence atomic.Uint64
	logger   *slog.Logger
}

func NewHandler(c *config.Config, m *rt.Manager) *Handler {
	return &Handler{Manager: m, limit: c.Server.MaxRequestBodyBytes, logger: slog.Default()}
}
func (h *Handler) Serve(ctx context.Context, c *app.RequestContext) {
	start := time.Now()
	requestID := strconv.FormatInt(start.UnixNano(), 36) + "-" + strconv.FormatUint(h.sequence.Add(1), 36)
	c.Response.Header.Set("X-Request-ID", requestID)
	var appName, service, revision, rpcService, rpcMethod, idlFile, routePath string
	code, message, source, reason, rpcErrorType := "000000", "success", "", "", ""
	queryKeys := make([]string, 0)
	c.QueryArgs().VisitAll(func(k, v []byte) { queryKeys = append(queryKeys, string(k)) })
	sort.Strings(queryKeys)
	queryKeys = slices.Compact(queryKeys)
	defer func() {
		status := c.Response.StatusCode()
		level := slog.LevelInfo
		if status >= 500 {
			level = slog.LevelError
		} else if status >= 400 {
			level = slog.LevelWarn
		}
		attrs := []any{"request_id", requestID, "method", string(c.Method()), "host", string(c.Request.Header.Host()), "path", string(c.Path()),
			"route", routePath, "query_keys", queryKeys, "body_bytes", len(c.Request.Body()),
			"app", appName, "service", service, "rpc_service", rpcService, "rpc_method", rpcMethod, "idl_file", idlFile,
			"revision", revision, "duration_ms", float64(time.Since(start)) / float64(time.Millisecond),
			"status", status, "code", code, "message", message}
		if source != "" {
			attrs = append(attrs, "error_source", source, "error_reason", reason)
		}
		if rpcErrorType != "" {
			attrs = append(attrs, "rpc_error_type", rpcErrorType)
		}
		h.logger.Log(context.WithoutCancel(ctx), level, "request", attrs...)
	}()
	fail := func(status int, failureCode, failureMessage string) {
		code, message = failureCode, failureMessage
		c.JSON(status, Response{Code: code, Message: message, Data: nil})
	}
	source, reason = "gateway", "shutting_down"

	lease := h.Manager.Acquire()
	if lease == nil {
		fail(apperr.FromCommon(503, common.ErrDependencyUnavailable))
		return
	}
	defer lease.Release()
	domain, err := metadata.RequestDomain(string(c.Request.Header.Host()))
	if err != nil || len(lease.Snapshot.Domains[domain]) == 0 {
		source, reason = "gateway_validation", "unknown_domain"
		fail(apperr.FromCommon(404, common.ErrNotFound))
		return
	}
	codes := []string{}
	c.Request.Header.VisitAll(func(k, v []byte) {
		if strings.EqualFold(string(k), "X-App-Code") {
			codes = append(codes, string(v))
		}
	})
	if len(codes) != 1 || !lease.Snapshot.HasApp(domain, codes[0]) {
		source, reason = "gateway_validation", "app_code_mismatch"
		fail(apperr.FromCommon(403, common.ErrPermissionDenied))
		return
	}
	appName = codes[0]
	target, ok := lease.Snapshot.Match(appName, string(c.Method()), string(c.Path()))
	if !ok {
		source, reason = "gateway", "route_not_found"
		fail(apperr.FromCommon(404, common.ErrNotFound))
		return
	}
	r := lease.Snapshot.Runtimes[target.AppName]
	appName = r.AppName
	service = r.ServiceName
	revision = r.Revision
	rpcService, rpcMethod, idlFile, routePath = target.Route.RPCService, target.Route.RPCMethod, target.Route.IDLPath, target.Route.Path
	body := c.Request.Body()
	if len(body) > h.limit {
		source, reason = "gateway_validation", "body_too_large"
		fail(apperr.FromCommon(413, common.ErrInvalidArgument))
		return
	}
	if len(body) > 0 && !json.Valid(body) {
		source, reason = "gateway_validation", "invalid_json"
		fail(apperr.FromCommon(400, common.ErrInvalidArgument))
		return
	}
	source, reason = "gateway_validation", "invalid_http_request"
	// 仅将约定的五个请求头写入持久 Rpc 请求上下文。
	ctx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	request, e := http.NewRequestWithContext(ctx, string(c.Method()), string(c.Request.URI().FullURI()), bytes.NewReader(body))
	if e != nil {
		fail(apperr.FromCommon(400, common.ErrInvalidArgument))
		return
	}
	c.Request.Header.VisitAll(func(k, v []byte) { request.Header.Add(string(k), string(v)) })
	result := executor.Execute(ctx, r, target.Route, request, rpcmeta.FromHTTPHeader(request.Header))
	code, message, source, reason, rpcErrorType = result.Code, result.Message, result.Source, result.Reason, result.ErrorType
	for k, values := range result.Header {
		for _, v := range values {
			c.Response.Header.Add(k, v)
		}
	}
	c.Data(result.Status, "application/json; charset=utf-8", result.Payload)
}
