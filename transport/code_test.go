package transport

import (
	"errors"
	"testing"
)

func TestHelpersProduceExpectedCodes(t *testing.T) {
	cases := []struct {
		name string
		err  *Error
		want int
	}{
		{"InvalidArgument", InvalidArgument("BAD_INPUT", "参数有误"), CodeInvalidArgument},
		{"Unauthenticated", Unauthenticated("NO_TOKEN", "缺少凭证"), CodeUnauthenticated},
		{"PermissionDenied", PermissionDenied("FORBIDDEN", "无权访问"), CodePermissionDenied},
		{"NotFound", NotFound("USER_NOT_FOUND", "用户不存在"), CodeNotFound},
		{"AlreadyExists", AlreadyExists("DUPLICATE", "已存在"), CodeAlreadyExists},
		{"FailedPrecondition", FailedPrecondition("NOT_READY", "状态不满足"), CodeFailedPrecondition},
		{"RateLimited", RateLimited("TOO_MANY", "请求过快"), CodeRateLimited},
		{"Internal", Internal("BOOM", "内部错误"), CodeInternal},
		{"Unavailable", Unavailable("DOWN", "依赖不可用"), CodeUnavailable},
		{"Timeout", Timeout("SLOW", "超时"), CodeTimeout},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.err.Code != tc.want {
				t.Errorf("Code = %d, want %d", tc.err.Code, tc.want)
			}
			if tc.err.Reason == "" {
				t.Error("Reason 不应为空")
			}
		})
	}
}

func TestCodeReadsThroughWrapping(t *testing.T) {
	err := NotFound("USER_NOT_FOUND", "用户不存在")
	if got := Code(err); got != CodeNotFound {
		t.Errorf("Code() = %d, want %d", got, CodeNotFound)
	}

	wrapped := errors.Join(errors.New("外层"), err)
	if got := Code(wrapped); got != CodeNotFound {
		t.Errorf("包装后 Code() = %d, want %d", got, CodeNotFound)
	}
}

func TestCodeOfNilIsOK(t *testing.T) {
	if got := Code(nil); got != CodeOK {
		t.Errorf("Code(nil) = %d, want %d", got, CodeOK)
	}
}

func TestCodeOfUnknownErrorIsInternal(t *testing.T) {
	if got := Code(errors.New("随便一个错误")); got != CodeInternal {
		t.Errorf("Code() = %d, want %d", got, CodeInternal)
	}
}
