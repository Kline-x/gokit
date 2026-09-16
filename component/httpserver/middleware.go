package httpserver

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/Kline-x/gokit/component/log"
	"github.com/Kline-x/gokit/transport"
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
			tracked := &wroteTracker{ResponseWriter: w}
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
				if tracked.wrote {
					// 响应已经写出去一部分，补不回来了，只记日志。
					logger.ErrorContext(r.Context(), "panic 发生时响应已部分写出，无法再写错误信封")
					return
				}
				// 客户端只拿到一句泛化描述，panic 的内容与堆栈只进日志。
				// 走统一信封是为了让按信封解码的客户端不至于收到一个空响应体。
				if renderErr := transport.RenderError(w,
					transport.Internal("PANIC", "内部错误")); renderErr != nil {
					logger.ErrorContext(r.Context(), "写出 panic 响应失败",
						slog.Any("error", renderErr))
				}
			}()
			next.ServeHTTP(tracked, r)
		})
	}
}

// wroteTracker 只记录「有没有已经写出去过」。
//
// panic 发生时，handler 可能已经写了一部分响应。那时再写一个错误信封，
// 只会把两段内容拼在一起，交给客户端一个解析不了的 body ——
// 状态码也早就定死了，改不动。这种情况下唯一能做的就是别再写，让日志承载真相。
//
// 与 statusRecorder 保持独立：两个中间件各自只关心自己需要的那一点状态，
// 不共用一个包装类型。
type wroteTracker struct {
	http.ResponseWriter
	wrote bool
}

func (w *wroteTracker) WriteHeader(code int) {
	w.wrote = true
	w.ResponseWriter.WriteHeader(code)
}

func (w *wroteTracker) Write(b []byte) (int, error) {
	w.wrote = true
	return w.ResponseWriter.Write(b)
}

// Unwrap 让 http.NewResponseController 能穿透本包装层，取到底层 writer。
// Recover 在推荐顺序里排在 RequestLog 之内、业务 handler 之外，
// 因此一个会调用 http.NewResponseController 设置读写超时的 handler
// 必须能透过这层包装拿到真正的底层 writer。
func (w *wroteTracker) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Flush 透传底层 writer 的 Flush，保证 SSE 这类流式响应仍然可用。
//
// 嵌入的是 http.ResponseWriter 接口而非具体类型，Go 不会把底层值的
// Flush/Hijack 这类接口之外的方法提升上来——不显式转发，handler 对
// w.(http.Flusher) 的断言会失败，SSE/WebSocket 会因为套了这层 Recover
// 而失效，即便外层的 statusRecorder 本来是支持的。
//
// Flush 会隐式把响应提交给客户端（未写过时标准库按 200 处理），
// 所以这里也要记为「已写」，避免 panic 发生后再叠加一份错误信封。
func (w *wroteTracker) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		w.wrote = true
		f.Flush()
	}
}

// Hijack 透传底层 writer 的 Hijack，保证 WebSocket 升级这类接管连接的场景仍然可用。
//
// 接管成功后连接已经不归 HTTP 响应管，标记为「已写」是为了防止 panic 发生在
// 接管之后时，deferred 逻辑还去对一个已被接管的连接写错误信封。
func (w *wroteTracker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("httpserver: 底层 ResponseWriter 不支持 Hijack")
	}
	conn, buf, err := h.Hijack()
	if err == nil {
		w.wrote = true
	}
	return conn, buf, err
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

// timeoutBody 是超时响应的信封文本，进程启动时算一次。
//
// 注意两处标准库带来的限制：http.TimeoutHandler 把状态码写死成 503，
// 所以这里的 code 取 CodeUnavailable 而不是 CodeTimeout，免得状态码与信封自相矛盾；
// 它也不会替我们设 Content-Type，因此这条响应的类型由 Go 的内容嗅探决定，
// 不是 application/json。按信封解码的客户端不受影响，按 Content-Type 分支的会。
var timeoutBody = func() string {
	buf, err := json.Marshal(transport.Response{
		Code:    transport.CodeUnavailable,
		Reason:  "REQUEST_TIMEOUT",
		Message: "请求处理超时",
	})
	if err != nil {
		return `{"code":503,"reason":"REQUEST_TIMEOUT","message":"请求处理超时"}`
	}
	return string(buf)
}()

// Timeout 给处理链加上整体超时，超时返回 503。
//
// 超时响应由 http.TimeoutHandler 直接写出，不经过外层包装，
// 因此 Timeout 必须排在 RequestLog 之内，否则日志记录的状态码会失真。
//
// 另需注意：http.TimeoutHandler 会把整个响应缓冲起来，它交给下游的 writer
// 既不实现 Flusher 也不实现 Hijacker。因此只要用了 Timeout，
// 它内层的 SSE 流式输出与 WebSocket 升级都会失效。
//
// 另外两处标准库带来的限制：http.TimeoutHandler 把状态码写死成 503，
// 所以 timeoutBody 里的 code 取 CodeUnavailable 而不是 CodeTimeout，免得状态码与信封自相矛盾；
// 它也不会替我们设 Content-Type，因此这条响应的类型由 Go 的内容嗅探决定，不是 application/json。
// 按信封解码的客户端不受影响，按 Content-Type 分支的会。
func Timeout(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.TimeoutHandler(next, d, timeoutBody)
	}
}
