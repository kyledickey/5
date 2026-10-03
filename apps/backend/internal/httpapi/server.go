// Package httpapi owns the HTTP process boundary: the shared platform
// middleware stack installed on every router, and the listener lifecycle.
package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/config"
	"github.com/quackdiscord/bot/internal/httpapi/middleware"
	httpplatform "github.com/quackdiscord/bot/internal/httpapi/platform"
	"github.com/quackdiscord/bot/internal/httpapi/routes"
	"github.com/quackdiscord/bot/internal/moduleintegration"
	"github.com/quackdiscord/bot/internal/quack"
)

// PlatformRegistrar validates and installs the shared HTTP security and contract middleware for integration-owned routers.
type PlatformRegistrar struct {
	cfg config.Config
	// primitives is nil for routers that opt out of the endpoint rate and
	// idempotency policy entirely; that is distinct from primitives backed by
	// an unavailable Redis, which fail closed.
	primitives *httpplatform.Primitives
}

// NewPlatformRegistrar constructs the reusable QP-B platform registrar.
func NewPlatformRegistrar(cfg config.Config) (*PlatformRegistrar, error) {
	if err := middleware.ValidateSecurityConfig(cfg); err != nil {
		return nil, err
	}
	return &PlatformRegistrar{cfg: cfg}, nil
}

// Register installs trusted-proxy handling and middleware in the required order before feature routes are registered.
func (p *PlatformRegistrar) Register(r *gin.Engine) error {
	if err := r.SetTrustedProxies(p.cfg.API.TrustedProxies); err != nil {
		return err
	}
	r.Use(middleware.RequestContext)
	r.Use(middleware.ErrorEnvelope)
	r.Use(middleware.Logger)
	r.Use(middleware.Recovery)
	r.Use(middleware.SecurityHeaders)
	r.Use(middleware.CORS(p.cfg.API.CORSAllowedOrigins))
	r.Use(middleware.BodyLimit(p.cfg.API.MaxBodyBytes))
	r.Use(middleware.CSRF(p.cfg.Auth, p.cfg.API.CORSAllowedOrigins))
	if p.primitives != nil {
		r.Use(httpplatform.EndpointPolicy(*p.primitives, p.cfg))
	}
	return nil
}

// Run serves the configured HTTP API until the context is canceled, then performs a bounded graceful shutdown.
func Run(ctx context.Context, cfg config.Config, services *quack.Services, moduleRuntime *moduleintegration.Runtime, discord routes.DiscordStatusProvider) error {
	if cfg.Environment != "dev" {
		gin.SetMode(gin.ReleaseMode)
	}

	registrar, err := NewPlatformRegistrar(cfg)
	if err != nil {
		return fmt.Errorf("validate HTTP platform configuration: %w", err)
	}
	redisProvider, _ := services.Store.(httpplatform.RedisProvider)
	primitives := httpplatform.New(redisProvider)
	registrar.primitives = &primitives

	r := gin.New()
	if err := registrar.Register(r); err != nil {
		return fmt.Errorf("configure trusted HTTP proxies: %w", err)
	}

	if err := routes.SetupRoutesWithModules(r, services, moduleRuntime, discord); err != nil {
		return fmt.Errorf("register HTTP routes: %w", err)
	}

	server := newHTTPServer(cfg, r)
	return serve(ctx, server, time.Duration(cfg.API.ShutdownTimeoutSeconds)*time.Second)
}

// serve owns the listener and joins shutdown before dependencies are closed.
// A bind failure creates no shutdown goroutine; a drain timeout force-closes
// connections so handlers cannot keep using storage after Run returns.
func serve(ctx context.Context, server *http.Server, shutdownTimeout time.Duration) error {
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return fmt.Errorf("listen for HTTP: %w", err)
	}
	defer listener.Close()
	slog.InfoContext(ctx, "HTTP API listening", "address", listener.Addr().String())
	serveDone := make(chan struct{})
	shutdownResult := make(chan error, 1)
	go func() {
		select {
		case <-serveDone:
			shutdownResult <- nil
			return
		case <-ctx.Done():
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		err := server.Shutdown(shutdownCtx)
		if err != nil {
			slog.Error("Failed to gracefully shut down API", "error", err)
			err = errors.Join(err, server.Close())
		}
		shutdownResult <- err
	}()
	serveErr := server.Serve(listener)
	close(serveDone)
	shutdownErr := <-shutdownResult
	if errors.Is(serveErr, http.ErrServerClosed) {
		serveErr = nil
	}
	return errors.Join(serveErr, shutdownErr)
}

func newHTTPServer(cfg config.Config, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              fmt.Sprintf(":%s", cfg.API.Port),
		Handler:           handler,
		ReadHeaderTimeout: time.Duration(cfg.API.ReadHeaderTimeoutSeconds) * time.Second,
		ReadTimeout:       time.Duration(cfg.API.ReadTimeoutSeconds) * time.Second,
		WriteTimeout:      time.Duration(cfg.API.WriteTimeoutSeconds) * time.Second,
		IdleTimeout:       time.Duration(cfg.API.IdleTimeoutSeconds) * time.Second,
	}
}
