package main

import (
	"context"
	"net/http"
	"sync"
	"time"

	coreruleset "github.com/corazawaf/coraza-coreruleset/v4"
	"github.com/corazawaf/coraza/v3"
	txhttp "github.com/corazawaf/coraza/v3/http"
)

var managementWAF = sync.OnceValues(func() (coraza.WAF, error) {
	return coraza.NewWAF(coraza.NewWAFConfig().WithRootFS(coreruleset.FS).WithDirectives(`
Include @coraza.conf-recommended
Include @crs-setup.conf.example
SecRuleEngine On
SecRequestBodyAccess On
SecRequestBodyLimit 1048576
SecRequestBodyNoFilesLimit 1048576
SecRequestBodyInMemoryLimit 1048576
SecResponseBodyAccess Off
SecAuditEngine Off
SecDebugLogLevel 0
Include @owasp_crs/*.conf
`))
})

func secureHandler(next http.Handler) (http.Handler, error) {
	gate := make(chan struct{}, 128)
	waf, err := managementWAF()
	if err != nil {
		return nil, err
	}
	guarded := txhttp.WrapHandler(waf, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if controller, ok := r.Context().Value(streamDeadlineKey{}).(*http.ResponseController); ok {
			_ = controller.SetReadDeadline(time.Time{})
		}
		next.ServeHTTP(w, r)
	}))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil {
			webError(w, http.StatusUpgradeRequired, "管理入口仅支持 HTTPS")
			return
		}
		select {
		case gate <- struct{}{}:
			defer func() { <-gate }()
		default:
			webError(w, 429, "管理服务繁忙，请稍后重试")
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		w.Header().Set("Cache-Control", "no-store")
		controller := http.NewResponseController(w)
		_ = controller.SetReadDeadline(time.Now().Add(15 * time.Second))
		_ = controller.SetWriteDeadline(time.Now().Add(15 * time.Second))
		r = r.WithContext(context.WithValue(r.Context(), streamDeadlineKey{}, controller))
		if r.Body != nil && r.Body != http.NoBody {
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		}
		guarded.ServeHTTP(w, r)
	}), nil
}

type streamDeadlineKey struct{}
type streamWriter struct {
	http.ResponseWriter
	controller *http.ResponseController
}

func (w *streamWriter) SetWriteDeadline(deadline time.Time) error {
	return w.controller.SetWriteDeadline(deadline)
}
func (w *streamWriter) FlushError() error {
	return http.NewResponseController(w.ResponseWriter).Flush()
}
func (w *streamWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func withStreamDeadline(w http.ResponseWriter, r *http.Request) http.ResponseWriter {
	if controller, ok := r.Context().Value(streamDeadlineKey{}).(*http.ResponseController); ok {
		return &streamWriter{ResponseWriter: w, controller: controller}
	}
	return w
}
