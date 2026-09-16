package transport

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRenderWritesSuccessEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := Render(rec, map[string]string{"text": "你好"}); err != nil {
		t.Fatalf("Render() error = %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v, 内容=%q", err, rec.Body.String())
	}
	if body["code"] != float64(CodeOK) {
		t.Errorf("code = %v, want %d", body["code"], CodeOK)
	}
	data, ok := body["data"].(map[string]any)
	if !ok {
		t.Fatalf("data 不是对象: %v", body["data"])
	}
	if data["text"] != "你好" {
		t.Errorf("data.text = %v, want 你好", data["text"])
	}
}

func TestRenderErrorMapsCodeToStatus(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   int
	}{
		{"not found", NotFound("USER_NOT_FOUND", "用户不存在"), http.StatusNotFound, CodeNotFound},
		{"invalid", InvalidArgument("BAD_INPUT", "参数有误"), http.StatusBadRequest, CodeInvalidArgument},
		{"rate limited", RateLimited("TOO_MANY", "请求过快"), http.StatusTooManyRequests, CodeRateLimited},
		{"unknown", errors.New("随便一个错误"), http.StatusInternalServerError, CodeInternal},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			if err := RenderError(rec, tc.err); err != nil {
				t.Fatalf("RenderError() error = %v", err)
			}
			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantStatus)
			}

			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("响应不是合法 JSON: %v", err)
			}
			if body["code"] != float64(tc.wantCode) {
				t.Errorf("code = %v, want %d", body["code"], tc.wantCode)
			}
			if _, has := body["data"]; has {
				t.Errorf("出错时不应带 data 字段，实际 = %v", body)
			}
		})
	}
}

func TestRenderErrorIncludesReasonAndMetadata(t *testing.T) {
	err := InvalidArgument("BAD_INPUT", "name 不能为空").
		WithMetadata(map[string]string{"field": "name"})

	rec := httptest.NewRecorder()
	if renderErr := RenderError(rec, err); renderErr != nil {
		t.Fatalf("RenderError() error = %v", renderErr)
	}

	var body map[string]any
	if jsonErr := json.Unmarshal(rec.Body.Bytes(), &body); jsonErr != nil {
		t.Fatalf("响应不是合法 JSON: %v", jsonErr)
	}
	if body["reason"] != "BAD_INPUT" {
		t.Errorf("reason = %v, want BAD_INPUT", body["reason"])
	}
	meta, ok := body["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("metadata 不是对象: %v", body["metadata"])
	}
	if meta["field"] != "name" {
		t.Errorf("metadata.field = %v, want name", meta["field"])
	}
}

func TestRenderErrorHidesInternalCause(t *testing.T) {
	// cause 只服务于进程内的 errors.Is，不应泄漏到响应体里。
	err := Internal("BOOM", "内部错误").WithCause(errors.New("数据库密码错误"))

	rec := httptest.NewRecorder()
	if renderErr := RenderError(rec, err); renderErr != nil {
		t.Fatalf("RenderError() error = %v", renderErr)
	}
	if body := rec.Body.String(); strings.Contains(body, "数据库密码错误") {
		t.Errorf("响应体泄漏了内部原因: %s", body)
	}
}

func TestHTTPStatusFallsBackToInternal(t *testing.T) {
	if got := HTTPStatus(9999); got != http.StatusInternalServerError {
		t.Errorf("HTTPStatus(9999) = %d, want 500", got)
	}
	if got := HTTPStatus(CodeOK); got != http.StatusOK {
		t.Errorf("HTTPStatus(CodeOK) = %d, want 200", got)
	}
}
