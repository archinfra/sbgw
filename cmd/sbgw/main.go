package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/archinfra/sbgw/internal/auth"
	"github.com/archinfra/sbgw/internal/config"
	"github.com/archinfra/sbgw/internal/logger"
	"github.com/archinfra/sbgw/internal/proxy"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		panic(err)
	}

	log, err := logger.New(cfg.Log)
	if err != nil {
		panic(err)
	}
	defer func() { _ = log.Sync() }()

	if cfg.Server.Mode == "release" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(logger.GinMiddleware(log))

	tokenStore := auth.NewTokenStore(cfg.Auth)
	chatProxy, err := proxy.NewChatProxy(cfg, log, tokenStore)
	if err != nil {
		log.Fatal("init chat proxy failed", zap.Error(err))
	}

	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "service": "sbgw", "version": version, "commit": commit, "date": date})
	})

	v1 := r.Group("/v1")
	v1.Use(auth.Middleware(tokenStore, log))
	v1.GET("/models", chatProxy.HandleModels)
	v1.GET("/usage", chatProxy.HandleUsage)
	v1.Any("/chat/completions", chatProxy.HandleChatCompletions)

	srv := &http.Server{
		Addr:              cfg.Server.Addr,
		Handler:           r,
		ReadHeaderTimeout: 30 * time.Second,
	}

	go func() {
		log.Info("sbgw started", zap.String("addr", cfg.Server.Addr), zap.String("strategy", cfg.Upstream.Strategy), zap.Int("upstream_count", len(cfg.Upstream.Endpoints)), zap.String("version", version), zap.String("commit", commit))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal("server failed", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	log.Info("shutting down")
	if err := srv.Shutdown(ctx); err != nil {
		log.Error("shutdown failed", zap.Error(err))
	}
}
