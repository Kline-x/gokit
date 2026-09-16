package httpserver

import (
	"bufio"
	"errors"
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
	status   int
	bytes    int64
	hijacked bool
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
//
// 这里把未写过的状态记为 200，是因为标准库的 Flush 在响应头尚未写出时
// 会隐式提交 200。若底层 writer 的 Flush 没有这个语义，记录值可能与实际不符。
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		if r.status == 0 {
			r.status = http.StatusOK
		}
		f.Flush()
	}
}

// Hijack 透传底层 writer 的 Hijack，保证 WebSocket 升级这类接管连接的场景仍然可用。
//
// 注意：本包装层无条件实现 http.Hijacker，所以 w.(http.Hijacker) 断言总会成功，
// 底层是否真的支持只能由返回的 error 体现。调用方必须检查这个 error，
// 不能因为断言成功就认定拿到了可用的连接。
func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("httpserver: 底层 ResponseWriter 不支持 Hijack")
	}

	conn, buf, err := h.Hijack()
	if err == nil {
		r.hijacked = true
	}
	return conn, buf, err
}

// Unwrap 让 http.NewResponseController 能穿透本包装层，
// 取到底层 writer 去设置读写截止时间。
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// ReadFrom 透传底层 writer 的 ReadFrom，保留 io.Copy 的零拷贝快路径。
func (r *statusRecorder) ReadFrom(src io.Reader) (int64, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}

	if rf, ok := r.ResponseWriter.(io.ReaderFrom); ok {
		n, err := rf.ReadFrom(src)
		r.bytes += n
		return n, err
	}

	// 底层不支持 ReadFrom 时退回逐块拷贝。这里刻意包一层只暴露 Write 的匿名类型：
	// 既让字节数走本记录器的 Write 统计，又避免 io.Copy 再次命中 ReadFrom 造成无限递归。
	return io.Copy(struct{ io.Writer }{r}, src)
}

// RequestLog 记录每个请求的方法、路径、状态码与耗时，
// 同时把 logger 放进请求 ctx，供业务代码用 log.FromContext 取用。
//
// 它依赖包装 ResponseWriter 来获取状态码，因此必须是 Recover 与 Timeout
// 之外的那一层，详见 Chain 的说明。
//
// 与 gRPC 侧不同，http.Handler 不返回 error，所以这里记不到错误详情。
// 业务层若要保留底层原因，应在写出响应之前自行记一条日志——
// transport.RenderError 只会把泛化描述发给客户端。
func RequestLog(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			begin := time.Now()
			ctx := log.NewContext(r.Context(), logger)
			rec := &statusRecorder{ResponseWriter: w}

			next.ServeHTTP(rec, r.WithContext(ctx))

			if rec.hijacked {
				// 连接已被接管，之后的收发不再经过 HTTP 响应，
				// 记录成独立事件，避免用一个假的状态码误导排障。
				logger.InfoContext(ctx, "http 连接已被接管",
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.Duration("latency", time.Since(begin)),
				)
				return
			}

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
//
// 另需注意：http.TimeoutHandler 会把整个响应缓冲起来，它交给下游的 writer
// 既不实现 Flusher 也不实现 Hijacker。因此只要用了 Timeout，
// 它内层的 SSE 流式输出与 WebSocket 升级都会失效。
func Timeout(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.TimeoutHandler(next, d, "请求处理超时")
	}
}
