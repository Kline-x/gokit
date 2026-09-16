package httpserver

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Kline-x/gokit/component/log"
	"github.com/Kline-x/gokit/transport"
)

func TestChainAppliesMiddlewareOutsideIn(t *testing.T) {
	var order []string
	mark := func(name string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}

	h := Chain(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			order = append(order, "handler")
		}),
		mark("first"), mark("second"),
	)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	want := []string{"first", "second", "handler"}
	if !slices.Equal(order, want) {
		t.Errorf("执行顺序 = %v, want %v", order, want)
	}
}

func TestRecoverTurnsPanicInto500(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	h := Recover(logger)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/boom", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(buf.String(), "boom") {
		t.Errorf("日志未记录 panic 内容，实际为 %q", buf.String())
	}
}

func TestRequestLogRecordsResultAndInjectsLogger(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	var gotInjected bool
	h := RequestLog(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotInjected = log.FromContext(r.Context()) == logger
		w.WriteHeader(http.StatusTeapot)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))

	if !gotInjected {
		t.Error("处理函数未能从请求 ctx 中取到注入的 logger")
	}

	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry); err != nil {
		t.Fatalf("日志不是合法 JSON: %v, 内容=%q", err, buf.String())
	}
	if entry["method"] != http.MethodGet {
		t.Errorf("method = %v, want GET", entry["method"])
	}
	if entry["path"] != "/x" {
		t.Errorf("path = %v, want /x", entry["path"])
	}
	if entry["status"] != float64(http.StatusTeapot) {
		t.Errorf("status = %v, want %d", entry["status"], http.StatusTeapot)
	}
}

func TestRequestLogDefaultsStatusTo200(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	h := RequestLog(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/y", nil))

	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry); err != nil {
		t.Fatalf("日志不是合法 JSON: %v", err)
	}
	if entry["status"] != float64(http.StatusOK) {
		t.Errorf("status = %v, want 200（处理函数未显式 WriteHeader 时）", entry["status"])
	}
}

// flushHijackRecorder 同时实现 Flusher 与 Hijacker，用来验证包装层的接口透传。
type flushHijackRecorder struct {
	*httptest.ResponseRecorder
	flushed  bool
	hijacked bool
}

func (f *flushHijackRecorder) Flush() { f.flushed = true }

func (f *flushHijackRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	f.hijacked = true
	return nil, nil, nil
}

func TestRequestLogPassesThroughOptionalInterfaces(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	rec := &flushHijackRecorder{ResponseRecorder: httptest.NewRecorder()}

	h := RequestLog(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("包装后的 writer 丢失了 http.Flusher")
			return
		}
		flusher.Flush()

		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Error("包装后的 writer 丢失了 http.Hijacker")
			return
		}
		if _, _, err := hijacker.Hijack(); err != nil {
			t.Errorf("Hijack() error = %v", err)
		}
	}))

	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/stream", nil))

	if !rec.flushed {
		t.Error("Flush 未透传到底层 writer")
	}
	if !rec.hijacked {
		t.Error("Hijack 未透传到底层 writer")
	}
}

func TestRequestLogRecordsTimeoutStatusWhenOrderedCorrectly(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	// 推荐顺序：RequestLog 在外，Timeout 在内。
	h := Chain(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(300 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		}),
		RequestLog(logger),
		Timeout(20*time.Millisecond),
	)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/slow", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}

	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry); err != nil {
		t.Fatalf("日志不是合法 JSON: %v, 内容=%q", err, buf.String())
	}
	if entry["status"] != float64(http.StatusServiceUnavailable) {
		t.Errorf("日志里的 status = %v, want 503（超时响应应被如实记录）", entry["status"])
	}
}

func TestTimeoutReturns503ForSlowHandler(t *testing.T) {
	h := Timeout(20 * time.Millisecond)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/slow", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

// plainReader 只暴露 Read，挡住 io.Copy 的 WriterTo 快路径，
// 好让拷贝真正落到目的地的 ReadFrom 上。
type plainReader struct{ r io.Reader }

func (p plainReader) Read(b []byte) (int, error) { return p.r.Read(b) }

// notReaderFrom 只实现 http.ResponseWriter，用来逼出 ReadFrom 的退化路径。
type notReaderFrom struct{ http.ResponseWriter }

func TestReadFromCountsBytesOnFallbackPath(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	payload := strings.Repeat("x", 4096)
	h := RequestLog(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := io.Copy(w, plainReader{r: strings.NewReader(payload)}); err != nil {
			t.Errorf("io.Copy() error = %v", err)
		}
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(&notReaderFrom{ResponseWriter: rec}, httptest.NewRequest(http.MethodGet, "/blob", nil))

	if got := rec.Body.Len(); got != len(payload) {
		t.Fatalf("写出字节数 = %d, want %d", got, len(payload))
	}

	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry); err != nil {
		t.Fatalf("日志不是合法 JSON: %v, 内容=%q", err, buf.String())
	}
	if entry["bytes"] != float64(len(payload)) {
		t.Errorf("日志里的 bytes = %v, want %d（退化路径也必须统计字节数）", entry["bytes"], len(payload))
	}
}

func TestRequestLogReportsHijackedConnectionSeparately(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	rec := &flushHijackRecorder{ResponseRecorder: httptest.NewRecorder()}

	h := RequestLog(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Error("包装后的 writer 丢失了 http.Hijacker")
			return
		}
		if _, _, err := hijacker.Hijack(); err != nil {
			t.Errorf("Hijack() error = %v", err)
		}
	}))

	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ws", nil))

	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry); err != nil {
		t.Fatalf("日志不是合法 JSON: %v, 内容=%q", err, buf.String())
	}
	if _, has := entry["status"]; has {
		t.Errorf("被接管的连接不应记录状态码，实际日志=%v", entry)
	}
	if entry["msg"] != "http 连接已被接管" {
		t.Errorf("msg = %v, want 「http 连接已被接管」", entry["msg"])
	}
}

func TestRecoverWritesEnvelope(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	h := Recover(logger)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/boom", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("panic 响应不是合法 JSON: %v, 内容=%q", err, rec.Body.String())
	}
	if body["code"] != float64(transport.CodeInternal) {
		t.Errorf("code = %v, want %d", body["code"], transport.CodeInternal)
	}
	if strings.Contains(rec.Body.String(), "boom") {
		t.Errorf("响应体泄漏了 panic 内容: %s", rec.Body.String())
	}
	if !strings.Contains(buf.String(), "boom") {
		t.Error("日志里没有 panic 内容，排障会断线")
	}
}

func TestTimeoutWritesEnvelope(t *testing.T) {
	h := Timeout(20 * time.Millisecond)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/slow", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("超时响应不是合法 JSON: %v, 内容=%q", err, rec.Body.String())
	}
	if body["reason"] != "REQUEST_TIMEOUT" {
		t.Errorf("reason = %v", body["reason"])
	}
}
