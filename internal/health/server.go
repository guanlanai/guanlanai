package health

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/guanlanai/guanlanai/internal/version"
)

// Server 提供容器健康检查端点。
type Server struct {
	httpServer *http.Server
	db         *sql.DB
	logger     *slog.Logger
	instance   string
	metrics    http.Handler
}

// Option 调整 HTTP 服务的可选端点。
type Option func(*Server)

// WithMetrics 在 /metrics 暴露 Prometheus 指标。
func WithMetrics(handler http.Handler) Option {
	return func(s *Server) {
		s.metrics = handler
	}
}

// New 构造健康检查 HTTP 服务。
func New(addr string, db *sql.DB, logger *slog.Logger, instance string, opts ...Option) *Server {
	s := &Server{
		db:       db,
		logger:   logger,
		instance: instance,
	}
	for _, opt := range opts {
		opt(s)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /readyz", s.handleReady)
	if s.metrics != nil {
		mux.Handle("GET /metrics", s.metrics)
	}

	s.httpServer = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}
	return s
}

// Handler 暴露给测试使用。
func (s *Server) Handler() http.Handler {
	return s.httpServer.Handler
}

// Start 在后台启动 HTTP 服务。
func (s *Server) Start() {
	go func() {
		s.logger.Info("健康检查服务监听", "addr", s.httpServer.Addr)
		if err := s.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logger.Error("健康检查服务异常", "error", err)
		}
	}()
}

// Shutdown 优雅关闭 HTTP 服务。
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":   "ok",
		"instance": s.instance,
		"version":  version.Version,
	})
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if err := s.db.PingContext(ctx); err != nil {
		s.logger.Error("数据库就绪检查失败", "error", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status": "unready",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status": "ready",
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
