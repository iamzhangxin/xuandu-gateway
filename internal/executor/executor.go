package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/cloudwego/kitex/pkg/generic"
	"github.com/cloudwego/kitex/pkg/kerrors"
	"github.com/iamzhangxin/xuandu-gateway/internal/apperr"
	rt "github.com/iamzhangxin/xuandu-gateway/internal/runtime"
	common "github.com/iamzhangxin/rpcxcommon/errors"
	"github.com/iamzhangxin/rpcxcommon/rpcmeta"
	"net/http"
	"strings"
)

type Response struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data"`
}
type Result struct {
	Status                                   int
	Payload                                  json.RawMessage
	Header                                   http.Header
	Code, Message, Source, Reason, ErrorType string
}

// Execute only uses a caller-pinned runtime. It never performs HTTP loopback or discovery outside Kitex.
func Execute(ctx context.Context, r *rt.ServiceRuntime, request *http.Request, userId string) (result Result) {
	result.Header = make(http.Header)
	code, message, source, reason, rpcErrorType := "000000", "success", "gateway_validation", "invalid_http_request", ""
	defer func() {
		result.Code, result.Message, result.Source, result.Reason, result.ErrorType = code, message, source, reason, rpcErrorType
	}()
	fail := func(status int, failureCode, failureMessage string) {
		code, message = failureCode, failureMessage
		result.Status = status
		result.Payload, _ = json.Marshal(Response{Code: code, Message: message})
	}
	ctx = rpcmeta.WithUserId(ctx, userId)
	ctx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	req, e := generic.FromHTTPRequest(request.WithContext(ctx))
	if e != nil {
		fail(apperr.FromCommon(400, common.ErrInvalidArgument))
		return
	}
	source, reason = "upstream_rpc", "rpc_call_failed"
	resp, e := r.Client.GenericCall(ctx, "", req)
	if e != nil {
		rpcErrorType = fmt.Sprintf("%T", e)
		if _, ok := kerrors.FromBizStatusError(e); ok {
			source, reason = "upstream_business", "business_error"
		}
		fail(apperr.Map(e))
		return
	}
	source, reason = "upstream_response", "invalid_response_type"
	out, ok := resp.(*generic.HTTPResponse)
	if !ok || out == nil {
		fail(apperr.FromCommon(502, common.ErrDependencyUnavailable))
		return
	}
	status := int(out.StatusCode)
	if status == 0 {
		status = 200
	}
	if status < 200 || status > 599 {
		reason = "invalid_http_status"
		fail(apperr.FromCommon(502, common.ErrDependencyUnavailable))
		return
	}
	if status >= 400 {
		reason = "upstream_http_error"
		switch status {
		case 400:
			fail(apperr.FromCommon(status, common.ErrInvalidArgument))
		case 401:
			fail(apperr.FromCommon(status, common.ErrIdentityRequired))
		case 403:
			fail(apperr.FromCommon(status, common.ErrPermissionDenied))
		case 404:
			fail(apperr.FromCommon(status, common.ErrNotFound))
		case 409:
			fail(apperr.FromCommon(status, common.ErrConflict))
		default:
			fail(apperr.FromCommon(status, common.ErrInternal))
		}
		return
	}
	if status >= 300 {
		reason = "unexpected_redirect"
		fail(apperr.FromCommon(502, common.ErrDependencyUnavailable))
		return
	}
	var data any = out.Body
	if out.RawBody != nil {
		if len(bytes.TrimSpace(out.RawBody)) == 0 {
			data = nil
		} else {
			if !json.Valid(out.RawBody) {
				reason = "invalid_response_json"
				fail(apperr.FromCommon(502, common.ErrDependencyUnavailable))
				return
			}
			data = json.RawMessage(out.RawBody)
		}
	}
	reason = "response_encoding_failed"
	payload, e := json.Marshal(Response{Code: "000000", Message: "success", Data: data})
	if e != nil {
		fail(apperr.FromCommon(502, common.ErrDependencyUnavailable))
		return
	}
	// The envelope replaces the upstream representation and its framing headers.
	blocked := map[string]bool{"content-type": true, "content-length": true, "content-encoding": true, "transfer-encoding": true, "connection": true, "trailer": true, "upgrade": true, "keep-alive": true, "proxy-authenticate": true, "proxy-authorization": true, "te": true, "x-request-id": true, "etag": true, "content-md5": true, "content-range": true}
	for k, values := range out.Header {
		if strings.EqualFold(k, "Connection") {
			for _, v := range values {
				for _, name := range strings.Split(v, ",") {
					blocked[strings.ToLower(strings.TrimSpace(name))] = true
				}
			}
		}
	}
	for k, values := range out.Header {
		if blocked[strings.ToLower(k)] {
			continue
		}
		for _, v := range values {
			result.Header.Add(k, v)
		}
	}
	// 204/205 forbid a response body; use 200 to deliver the required envelope.
	if status == 204 || status == 205 {
		status = 200
	}
	source, reason = "", ""
	result.Status, result.Payload = status, payload
	return
}
