// Package interfaces 是问候模块的接口层：只做协议转换，
// 把 HTTP 请求翻译成应用层用例调用，再把结果翻译回响应。
package interfaces

import (
	"encoding/json"
	"net/http"

	"github.com/Kline-x/gokit/example/minimal/internal/greeter/application"
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
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(map[string]string{"text": reply.Text}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
