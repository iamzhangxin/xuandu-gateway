package apperr

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/cloudwego/kitex/pkg/kerrors"
	"github.com/cloudwego/kitex/pkg/remote"
	"github.com/cloudwego/kitex/pkg/remote/codec/perrors"
	common "github.com/iamzhangxin/rpcxcommon/errors"
)

func FromCommon(status int, err *common.Error) (int, string, string) {
	return status, fmt.Sprintf("%06d", err.Code()), err.Message()
}

func Map(err error) (int, string, string) {
	if biz, ok := kerrors.FromBizStatusError(err); ok {
		code := biz.BizStatusCode()
		if code < 100000 || code > 999999 {
			return FromCommon(500, common.ErrInternal)
		}
		status := int(code / 1000)
		if status < 400 || status > 599 {
			status = 400
		}
		return status, fmt.Sprintf("%06d", code), biz.BizMessage()
	}
	var protocol perrors.ProtocolError
	if errors.As(err, &protocol) {
		if strings.HasPrefix(protocol.Error(), "thrift marshal, Write failed:") {
			return FromCommon(400, common.ErrInvalidArgument)
		}
		return FromCommon(502, common.ErrDependencyUnavailable)
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, kerrors.ErrRPCTimeout):
		return FromCommon(504, common.ErrDependencyUnavailable)
	case errors.Is(err, kerrors.ErrServiceDiscovery), errors.Is(err, kerrors.ErrLoadbalance), errors.Is(err, kerrors.ErrNoMoreInstance):
		return FromCommon(503, common.ErrDependencyUnavailable)
	case errors.Is(err, kerrors.ErrPayloadValidation):
		return FromCommon(400, common.ErrInvalidArgument)
	case errors.Is(err, kerrors.ErrGetConnection), errors.Is(err, kerrors.ErrRemoteOrNetwork):
		return FromCommon(502, common.ErrDependencyUnavailable)
	}
	var te *remote.TransError
	if errors.As(err, &te) && te.TypeID() == remote.ProtocolError {
		return FromCommon(400, common.ErrInvalidArgument)
	}
	if ne, ok := errors.AsType[net.Error](err); ok {
		if ne.Timeout() {
			return FromCommon(504, common.ErrDependencyUnavailable)
		}
		return FromCommon(502, common.ErrDependencyUnavailable)
	}
	return FromCommon(500, common.ErrInternal)
}
