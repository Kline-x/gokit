package httpserver

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/Kline-x/gokit/component/log"
)

// Chain 把中间件按传入顺序由外向内套在 h 上：
// Chain(h, a, b) 的执行顺序是 a → b → h。
//
// 顺序约束：RequestLog 必须排在 Recover 与 Timeout 之前（更靠外），否则日志会失真。
// Timeout 超时后由 http.TimeoutHandler 直接向它自己收到的 writer 写 503，
// 若它排在 RequestLog 外面，这个 503 就绕过了状态记录器，日志会把请求记成 200。
// 同理 Recover 若排在外面，panic 会在 RequestLog 记录之前就穿过去，该请求不会留下任何日志。
// 推荐写法：Chain(h, RequestLog(logger), Recover(logger), Timeout(d))
func Chain(h http.Handler, mw ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mw) - 1; i >= 0; i-- {
		h = mw[i](h)
	}
	return h
}

// Recover 捕获处理链中的 panic，记录堆栈并返回 500，避免整个进程崩溃。
func Recover(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				logger.ErrorContext(r.Context(), "http 处理过程中发生 panic",
					slog.Any("panic", rec),
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.String("stack", string(debug.Stack())),
				)
				w.WriteHeader(http.StatusInternalServerError)
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// statusRecorder 记录真实写出的状态码，供 RequestLog 使用。
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += int64(n)
	return n, err
}

// Flush 透传底层 writer 的 Flush，保证 SSE 这类流式响应仍然可用。
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		if r.status == 0 {
			r.status = http.StatusOK
		}
		f.Flush()
	}
}

// Hijack 透传底层 writer 的 Hijack，保证 WebSocket 升级这类接管连接的场景仍然可用。
func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("httpserver: 底层 ResponseWriter 不支持 Hijack")
	}
	return h.Hijack()
}

// ReadFrom 透传底层 writer 的 ReadFrom，保留 io.Copy 的零拷贝快路径。
func (r *statusRecorder) ReadFrom(src io.Reader) (int64, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	rf, ok := r.ResponseWriter.(io.ReaderFrom)
	if !ok {
		return io.Copy(r.ResponseWriter, src)
	}
	n, err := rf.ReadFrom(src)
	r.bytes += n
	return n, err
}

// RequestLog 记录每个请求的方法、路径、状态码与耗时，
// 同时把 logger 放进请求 ctx，供业务代码用 log.FromContext 取用。
//
// 它依赖包装 ResponseWriter 来获取状态码，因此必须是 Recover 与 Timeout
// 之外的那一层，详见 Chain 的说明。
func RequestLog(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			begin := time.Now()
			ctx := log.NewContext(r.Context(), logger)
			rec := &statusRecorder{ResponseWriter: w}

			next.ServeHTTP(rec, r.WithContext(ctx))

			if rec.status == 0 {
				rec.status = http.StatusOK
			}
			logger.InfoContext(ctx, "http 请求",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rec.status),
				slog.Int64("bytes", rec.bytes),
				slog.Duration("latency", time.Since(begin)),
			)
		})
	}
}

// Timeout 给处理链加上整体超时，超时返回 503。
//
// 超时响应由 http.TimeoutHandler 直接写出，不经过外层包装，
// 因此 Timeout 必须排在 RequestLog 之内，否则日志记录的状态码会失真。
func Timeout(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.TimeoutHandler(next, d, "请求处理超时")
	}
}
