// Package interfaces 是问候模块的接口层：只做协议转换，
// 把 HTTP 请求翻译成应用层用例调用，再把结果翻译回响应。
package interfaces

import (
	"log/slog"
	"net/http"

	"github.com/Kline-x/gokit/component/log"
	"github.com/Kline-x/gokit/example/minimal/internal/greeter/application"
	"github.com/Kline-x/gokit/transport"
)

// HTTPHandler 把问候用例暴露成 HTTP 接口。
type HTTPHandler struct {
	svc application.Service
}

// NewHTTPHandler 构造接口层处理器。注意它依赖的是 Service 接口，
// 因此本模块改成远程调用时，这一层完全不用动。
func NewHTTPHandler(svc application.Service) *HTTPHandler {
	return &HTTPHandler{svc: svc}
}

// Register 把本模块的路由挂到给定的 mux 上。
func (h *HTTPHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /greet/{name}", h.greet)
}

func (h *HTTPHandler) greet(w http.ResponseWriter, r *http.Request) {
	reply, err := h.svc.Greet(r.Context(), application.GreetRequest{
		Name: r.PathValue("name"),
	})
	if err != nil {
		if renderErr := transport.RenderError(w, err); renderErr != nil {
			log.FromContext(r.Context()).ErrorContext(r.Context(), "写出错误响应失败",
				slog.Any("error", renderErr))
		}
		return
	}

	if renderErr := transport.Render(w, map[string]string{"text": reply.Text}); renderErr != nil {
		log.FromContext(r.Context()).ErrorContext(r.Context(), "写出响应失败",
			slog.Any("error", renderErr))
	}
}
