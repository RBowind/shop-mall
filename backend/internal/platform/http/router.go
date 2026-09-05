package http

import (
	"context"
	"io"
	"log/slog"
	stdhttp "net/http"

	"shop-mall/backend/internal/config"
	"shop-mall/backend/internal/middleware"
	"shop-mall/backend/internal/platform/database"
	"shop-mall/backend/internal/platform/logging"
	"shop-mall/backend/internal/platform/metrics"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type Dependencies struct {
	RegisterBuyerRoutes func(*gin.RouterGroup)
	RegisterAdminRoutes func(*gin.RouterGroup)
	Ready               func(context.Context) error
	Metrics             *metrics.Metrics
	Logger              *slog.Logger
}

type Deps = Dependencies

func NewRouter(cfg config.Config, db *gorm.DB, deps Dependencies) *gin.Engine {
	logger := deps.Logger
	if logger == nil {
		logger = logging.New(io.Discard, slog.LevelInfo)
	}
	collector := deps.Metrics
	if collector == nil {
		collector = metrics.New()
	}

	r := gin.New()
	r.Use(
		middleware.Trace(),
		// The metrics middleware must sit OUTSIDE Recover so a panicking
		// handler still reaches the post-c.Next() increment code: Recover
		// converts the panic into a 500 response, and the middleware observes
		// that status on its way back out. Registered after Recover, the panic
		// unwinds the metrics frames and the request is never counted, which
		// would hide every panic-derived 500 from HTTP5xxRatioHigh.
		collector.Middleware(),
		middleware.Recover(logger),
		logging.RequestLogger(logger),
	)

	r.GET("/health/live", func(c *gin.Context) {
		OperationalJSON(c, stdhttp.StatusOK, gin.H{"status": "ok"})
	})
	r.GET("/health/ready", func(c *gin.Context) {
		if err := database.HealthCheck(c.Request.Context(), db); err != nil {
			OperationalJSON(c, stdhttp.StatusServiceUnavailable, gin.H{"status": "not_ready"})
			return
		}
		if deps.Ready != nil {
			if err := deps.Ready(c.Request.Context()); err != nil {
				OperationalJSON(c, stdhttp.StatusServiceUnavailable, gin.H{"status": "not_ready"})
				return
			}
		}
		OperationalJSON(c, stdhttp.StatusOK, gin.H{"status": "ok"})
	})
	// The metrics exposition is internal-only: nginx refuses public access and
	// the PrivateNetworkOnly gate rejects any peer that is not on a
	// private/loopback range, so a misconfigured public listener cannot leak
	// the metrics into the internet.
	r.GET(metrics.MetricsPath, metrics.PrivateNetworkOnly(), gin.WrapH(collector.Handler()))

	// Development-only image serving. Production always fronts this service
	// with nginx, which serves /static/images/ straight off the volume with
	// proper caching; local dev and the E2E loop have no nginx, so the API
	// process serves the same prefix itself to keep returned image URLs
	// loadable outside compose.
	if cfg.Environment == "development" && cfg.ImageVolumeDir != "" {
		r.Static("/static/images", cfg.ImageVolumeDir)
	}

	buyer := r.Group("/api/v1")
	if deps.RegisterBuyerRoutes != nil {
		deps.RegisterBuyerRoutes(buyer)
	}

	admin := r.Group("/api/admin/v1")
	admin.Use(
		middleware.AdminOrigin(cfg.AllowedOrigins),
		middleware.CSRF(middleware.CSRFConfig{
			CookieName: cfg.AdminCookie.CSRFName,
			LoginPath:  "/api/admin/v1/auth/login",
		}),
	)
	if deps.RegisterAdminRoutes != nil {
		deps.RegisterAdminRoutes(admin)
	}
	return r
}
