package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	stdhttp "net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"shop-mall/backend/internal/platform/uid"

	"shop-mall/backend/internal/admin"
	"shop-mall/backend/internal/application/access"
	appauth "shop-mall/backend/internal/application/auth"
	apporder "shop-mall/backend/internal/application/order"
	apppoints "shop-mall/backend/internal/application/points"
	apprefund "shop-mall/backend/internal/application/refund"
	"shop-mall/backend/internal/cart"
	"shop-mall/backend/internal/config"
	"shop-mall/backend/internal/order"
	"shop-mall/backend/internal/payment"
	"shop-mall/backend/internal/platform/database"
	platformhttp "shop-mall/backend/internal/platform/http"
	"shop-mall/backend/internal/platform/logging"
	"shop-mall/backend/internal/platform/metrics"
	"shop-mall/backend/internal/platform/tokens"
	"shop-mall/backend/internal/platform/wechat"
	"shop-mall/backend/internal/product"
	"shop-mall/backend/internal/storage"
	"shop-mall/backend/internal/user"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func main() {
	logger := logging.New(os.Stderr, slog.LevelInfo)
	cfg, err := config.Load()
	if err != nil {
		logger.Error("invalid startup configuration", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	// signal.NotifyContext's stop() must run before the process terminates so
	// SIGINT/SIGTERM handlers are released; each os.Exit branch below calls
	// it explicitly (gocritic exitAfterDefer: defer would be skipped).
	db, err := database.Open(ctx, cfg.Database)
	if err != nil {
		stop()
		logger.ErrorContext(ctx, "open database", "error", err)
		os.Exit(1)
	}
	defer func() { _ = database.Close(db) }()

	// The observability bundle is shared by the router (request metrics), the
	// admin service (login failures), the image handler (upload failures) and
	// the database pool. It must be constructed before any component that
	// consumes it.
	appMetrics := metrics.New()
	if sqlDB, dbErr := db.DB(); dbErr == nil && sqlDB != nil {
		appMetrics.RegisterDBPool(metrics.NewDBPoolCollector(sqlDB))
	}

	if err := database.RunMigrations(ctx, db, filepath.Clean("migrations")); err != nil {
		_ = database.Close(db)
		stop()
		logger.ErrorContext(ctx, "run forward migrations", "error", err)
		os.Exit(1) //nolint:gocritic // paired with explicit stop() and database.Close() above
	}

	engine, components, err := buildRouter(cfg, db, logger, appMetrics)
	if err != nil {
		_ = database.Close(db)
		stop()
		logger.ErrorContext(ctx, "compose HTTP router", "error", err)
		os.Exit(1) //nolint:gocritic // paired with explicit stop() and database.Close() above
	}

	// The image-grace-period cleanup must be reachable in the running system:
	// replaced and unreferenced files would otherwise accumulate until the
	// volume fills and every upload returns 507. The loop runs one sweep at
	// startup and then every IMAGE_CLEANUP_INTERVAL, and stops together with
	// the HTTP server when the process context is canceled. Each sweep
	// refreshes the image-volume usage gauge so the disk-pressure alert sees
	// the state after deletions too.
	refreshStorageMetrics := func(ctx context.Context) {
		if used, err := components.imageStorage.UsedBytes(ctx); err == nil {
			appMetrics.SetImageStorageUsage(used, cfg.ImageStorageCapacity)
		}
	}
	go runImageCleanupLoop(ctx, cfg.ImageCleanupInterval, cfg.ImageGracePeriod, components.imageStorage, components.imageStorage, components.productService, refreshStorageMetrics, logger)

	server := &stdhttp.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           engine,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.ErrorContext(shutdownCtx, "shutdown HTTP server", "error", err)
		}
	}()

	logger.Info("HTTP server started", "addr", cfg.HTTPAddr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, stdhttp.ErrServerClosed) {
		stop()
		logger.Error("HTTP server stopped unexpectedly", "error", err)
		os.Exit(1)
	}
}

// serverComponents carries the runtime objects main still needs after the
// router is composed: the image storage and product service that drive the
// grace-period cleanup loop.
type serverComponents struct {
	imageStorage   *storage.LocalVolume
	productService *product.Service
}

// buildRouter composes the full production HTTP router exactly as the
// deployment runs it. It is separated from main so the composition can be
// exercised by tests (backend/cmd/server/main_test.go) without a live process.
// The buyer surface is complete here: public product and wx-login routes, plus
// the authenticated profile, address, cart and order groups.
func buildRouter(cfg config.Config, db *gorm.DB, logger *slog.Logger, appMetrics *metrics.Metrics) (*gin.Engine, *serverComponents, error) {
	adminSigner, err := tokens.NewSigner(cfg.AdminJWT)
	if err != nil {
		return nil, nil, fmt.Errorf("admin JWT signer: %w", err)
	}
	// The login rate limit has two independent dimensions: a per-account
	// budget and a per-IP budget. A single account may attempt 50 times per
	// minute across all hosts, but a single host may only attempt 5 times
	// per minute against any account.
	rateLimiter := admin.NewLoginRateLimiterConfig(admin.LoginRateLimiterConfig{
		AccountLimit:  50,
		AccountWindow: time.Minute,
		IPLimit:       5,
		IPWindow:      time.Minute,
	})
	adminService, err := admin.NewService(admin.ServiceDeps{
		DB:          db,
		Logger:      logger,
		Now:         func() time.Time { return time.Now().UTC() },
		RateLimiter: rateLimiter,
		Metrics:     appMetrics,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("admin service: %w", err)
	}
	accessUsecase, err := access.NewAccessUsecase(access.AccessUsecaseDeps{
		DB:      db,
		Service: adminService,
		Logger:  logger,
		Now:     func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		return nil, nil, fmt.Errorf("access usecase: %w", err)
	}
	adminHandler, err := admin.NewHandler(admin.HandlerDeps{
		Service:         adminService,
		Signer:          adminSigner,
		RateLimiter:     rateLimiter,
		PasswordChanger: accessPasswordChanger{usecase: accessUsecase},
		Logger:          logger,
		Cookie:          cfg.AdminCookie,
		Now:             func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		return nil, nil, fmt.Errorf("admin handler: %w", err)
	}

	imageStorage, err := storage.NewLocalVolume(cfg.ImageVolumeDir, cfg.ImageStorageCapacity)
	if err != nil {
		return nil, nil, fmt.Errorf("image storage: %w", err)
	}
	productRepo, err := product.NewRepository(db)
	if err != nil {
		return nil, nil, fmt.Errorf("product repository: %w", err)
	}
	productService, err := product.NewService(product.ServiceDeps{
		DB:            db,
		Repository:    productRepo,
		Logger:        logger,
		PublicBaseURL: cfg.PublicBaseURL,
		Now:           func() time.Time { return time.Now().UTC() },
		Toucher:       imageStorage,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("product service: %w", err)
	}
	productHandler, err := product.NewHandler(product.HandlerDeps{
		Service:     productService,
		AdminViewer: adminService,
		Logger:      logger,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("product handler: %w", err)
	}

	// Buyer tokens are signed with their own keyring (BUYER_JWT_*), separate
	// from the administrator keyring, so the buyer middleware and the login
	// usecase share the same issuer, audience and key material.
	buyerSigner, err := tokens.NewSigner(cfg.BuyerJWT)
	if err != nil {
		return nil, nil, fmt.Errorf("buyer JWT signer: %w", err)
	}

	// The WeChat code2session integration is resolved from the environment: a
	// fully configured AppID/Secret/endpoint wires the real client (deploy),
	// otherwise the deterministic local fake is used (development and tests).
	// The AppID and Secret are deploy-only secrets and never appear in code.
	wechatClient, err := wechat.NewClient(cfg.WeChat.AppID, cfg.WeChat.Secret, cfg.WeChat.Endpoint, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("wechat client: %w", err)
	}
	loginUsecase := appauth.NewLoginUsecase(appauth.LoginUsecaseDeps{
		DB:          db,
		WeChat:      wechatClient,
		Signer:      buyerSigner,
		Logger:      logger,
		Now:         func() time.Time { return time.Now().UTC() },
		SignupBonus: cfg.SignupBonus,
	})
	// wx-login is a public unauthenticated endpoint, so the contract's 429 must
	// be reachable: a per-IP budget stops a single host spraying login codes.
	// The buyer account is not known before the WeChat code is exchanged, so a
	// per-account dimension is impossible at this layer.
	buyerLoginLimiter := appauth.NewLoginRateLimiter(20, time.Minute)
	loginHandler, err := appauth.NewLoginHandler(appauth.LoginHandlerDeps{Usecase: loginUsecase, Logger: logger, Limiter: buyerLoginLimiter})
	if err != nil {
		return nil, nil, fmt.Errorf("buyer login handler: %w", err)
	}
	userService, err := user.NewService(user.ServiceDeps{DB: db})
	if err != nil {
		return nil, nil, fmt.Errorf("buyer user service: %w", err)
	}
	profileHandler, err := user.NewProfileHandler(user.ProfileHandlerDeps{
		Service:       userService,
		Logger:        logger,
		Store:         imageStorage,
		Capacity:      imageStorage,
		CapacityLimit: cfg.ImageStorageCapacity,
		UploadLimit:   cfg.UploadLimit,
		MaxPixels:     cfg.ImageMaxPixels,
		PublicBaseURL: cfg.PublicBaseURL,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("buyer profile handler: %w", err)
	}

	addressService, err := user.NewAddressService(user.AddressServiceDeps{DB: db})
	if err != nil {
		return nil, nil, fmt.Errorf("address service: %w", err)
	}
	addressHandler, err := user.NewAddressHandler(user.AddressHandlerDeps{
		Service: addressService,
		Logger:  logger,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("address handler: %w", err)
	}
	cartService, err := cart.NewCartService(cart.CartServiceDeps{
		DB:       db,
		Products: productService,
		Logger:   logger,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("cart service: %w", err)
	}
	cartHandler, err := cart.NewCartHandler(cart.CartHandlerDeps{
		Service: cartService,
		Logger:  logger,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("cart handler: %w", err)
	}

	// The order usecase owns the single transaction that commits the order,
	// inventory, points, ledger and cart-cleanup side effects. The cart and
	// product repositories expose transaction-scoped methods so the usecase
	// locks and deletes rows inside its own transaction rather than opening
	// nested ones.
	cartRepo, err := cart.NewRepository(db)
	if err != nil {
		return nil, nil, fmt.Errorf("cart repository: %w", err)
	}
	paymentRepo, err := payment.NewRepository(db)
	if err != nil {
		return nil, nil, fmt.Errorf("payment repository: %w", err)
	}
	orderRepo, err := order.NewRepository(db)
	if err != nil {
		return nil, nil, fmt.Errorf("order repository: %w", err)
	}
	orderUsecase, err := apporder.NewCreateOrderUsecase(apporder.CreateOrderUsecaseDeps{
		DB:       db,
		Orders:   orderRepo,
		Address:  addressService,
		Cart:     cartRepo,
		Products: productRepo,
		Ledger:   paymentRepo,
		Now:      func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		return nil, nil, fmt.Errorf("order usecase: %w", err)
	}
	adjustPointsUsecase, err := apppoints.NewAdjustPointsUsecase(apppoints.AdjustPointsUsecaseDeps{
		DB:     db,
		Ledger: paymentRepo,
		Roles:  adminService,
		Logger: logger,
		Now:    func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		return nil, nil, fmt.Errorf("points usecase: %w", err)
	}
	refundUsecase, err := apprefund.NewRefundUsecase(apprefund.RefundUsecaseDeps{
		DB:       db,
		Orders:   orderRepo,
		Products: productRepo,
		Ledger:   paymentRepo,
		Roles:    adminService,
		Logger:   logger,
		Now:      func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		return nil, nil, fmt.Errorf("refund usecase: %w", err)
	}
	fulfillmentUsecase, err := apporder.NewFulfillmentUsecase(apporder.FulfillmentUsecaseDeps{
		DB:     db,
		Orders: orderRepo,
		Roles:  adminService,
		Logger: logger,
		Now:    func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		return nil, nil, fmt.Errorf("fulfillment usecase: %w", err)
	}
	orderService, err := order.NewOrderService(order.OrderServiceDeps{
		Repo:   orderRepo,
		Ledger: paymentRepo,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("order service: %w", err)
	}
	orderHandler, err := order.NewOrderHandler(order.OrderHandlerDeps{
		Service:       orderService,
		Create:        orderUsecase,
		Refund:        refundUsecase,
		Fulfillment:   fulfillmentUsecase,
		Points:        adjustPointsUsecase,
		PublicBaseURL: cfg.PublicBaseURL,
		Logger:        logger,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("order handler: %w", err)
	}
	imageHandler, err := admin.NewImageHandler(admin.ImageHandlerDeps{
		Store:         imageStorage,
		Capacity:      imageStorage,
		CapacityLimit: cfg.ImageStorageCapacity,
		UploadLimit:   cfg.UploadLimit,
		MaxPixels:     cfg.ImageMaxPixels,
		PublicBaseURL: cfg.PublicBaseURL,
		DB:            db,
		Service:       adminService,
		Logger:        logger,
		Now:           func() time.Time { return time.Now().UTC() },
		Metrics:       appMetrics,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("image handler: %w", err)
	}

	engine := platformhttp.NewRouter(cfg, db, platformhttp.Dependencies{
		Logger:  logger,
		Metrics: appMetrics,
		RegisterBuyerRoutes: func(group *gin.RouterGroup) {
			appauth.RegisterBuyerAuthRoutes(group, appauth.BuyerAuthRouteDeps{Handler: loginHandler})
			product.RegisterPublicRoutes(group, productHandler)
			buyerState := buyerExistsResolver{db: db}
			user.RegisterProfileRoutes(group, user.ProfileRouteDeps{
				Handler:            profileHandler,
				Signer:             buyerSigner,
				BuyerStateResolver: buyerState,
			})
			user.RegisterAddressRoutes(group, user.AddressRouteDeps{
				Handler:            addressHandler,
				Signer:             buyerSigner,
				BuyerStateResolver: buyerState,
			})
			cart.RegisterRoutes(group, cart.RouteDeps{
				Handler:            cartHandler,
				Signer:             buyerSigner,
				BuyerStateResolver: buyerState,
			})
			order.RegisterBuyerRoutes(group, order.BuyerRouteDeps{
				Handler:            orderHandler,
				Signer:             buyerSigner,
				BuyerStateResolver: buyerState,
			})
		},
		RegisterAdminRoutes: func(group *gin.RouterGroup) {
			admin.RegisterRoutes(group, adminHandler)
			product.RegisterAdminRoutes(group, product.AdminRouteDeps{
				Handler:            productHandler,
				Signer:             adminSigner,
				Cookie:             cfg.AdminCookie,
				Now:                func() time.Time { return time.Now().UTC() },
				PermissionResolver: adminService,
				AdminStateResolver: adminService,
			})
			admin.RegisterImageRoutes(group, admin.ImageRouteDeps{
				Handler:            imageHandler,
				Signer:             adminSigner,
				Cookie:             cfg.AdminCookie,
				Now:                func() time.Time { return time.Now().UTC() },
				PermissionResolver: adminService,
				AdminStateResolver: adminService,
			})
			order.RegisterAdminRoutes(group, order.AdminRouteDeps{
				Handler:            orderHandler,
				Signer:             adminSigner,
				Cookie:             cfg.AdminCookie,
				Now:                func() time.Time { return time.Now().UTC() },
				PermissionResolver: adminService,
				AdminStateResolver: adminService,
			})
		},
	})
	return engine, &serverComponents{
		imageStorage:   imageStorage,
		productService: productService,
	}, nil
}

// runImageCleanupLoop periodically reconciles the image volume: objects no
// longer referenced by any product and older than the grace period are
// deleted. The first sweep runs immediately so a volume containing leftovers
// from an earlier version is reclaimed on startup; subsequent sweeps follow
// the configured interval. The loop returns when ctx is canceled so shutdown
// does not leak the goroutine.
func runImageCleanupLoop(ctx context.Context, interval time.Duration, gracePeriod time.Duration, store storage.Storage, lister storage.ObjectLister, resolver storage.ReferenceResolver, afterSweep func(context.Context), logger *slog.Logger) {
	if store == nil || lister == nil || resolver == nil {
		logger.ErrorContext(ctx, "image cleanup loop: storage, lister and resolver are required")
		return
	}
	sweep := func() {
		deleted, err := storage.Cleanup(ctx, store, lister, resolver, gracePeriod, time.Now(), logger)
		if err != nil {
			logger.ErrorContext(ctx, "image cleanup failed", "error", err)
		} else if deleted > 0 {
			logger.InfoContext(ctx, "image cleanup finished", "deleted", deleted)
		}
		if afterSweep != nil {
			afterSweep(ctx)
		}
	}
	sweep()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweep()
		}
	}
}

// accessPasswordChanger adapts access.AccessUsecase to the admin.PasswordChanger
// boundary the admin handler consumes. The usecase owns the password-change
// transaction per the task brief; the handler never reaches the service
// directly. A self-service change targets the actor's own account.
type accessPasswordChanger struct {
	usecase *access.AccessUsecase
}

func (c accessPasswordChanger) ChangePassword(ctx context.Context, input admin.ChangePasswordInput) (admin.ChangePasswordResult, error) {
	out, err := c.usecase.ChangePassword(ctx, access.ChangePasswordInput{
		ActorAdminID: input.ActorAdminID,
		ActorRole:    input.ActorRole,
		TargetID:     input.ActorAdminID,
		OldPassword:  input.OldPassword,
		NewPassword:  input.NewPassword,
	})
	if err != nil {
		return admin.ChangePasswordResult{}, err
	}
	return admin.ChangePasswordResult{
		AdminID:      out.AdminID,
		TokenVersion: out.TokenVersion,
		Username:     out.Username,
		RoleName:     out.RoleName,
	}, nil
}

// buyerExistsResolver implements middleware.BuyerStateResolver against the
// users table: a JWT subject is valid only while the buyer row still exists.
type buyerExistsResolver struct {
	db *gorm.DB
}

func (r buyerExistsResolver) BuyerExists(ctx context.Context, buyerID uid.ID) (bool, error) {
	if r.db == nil || uid.IsZero(buyerID) {
		return false, nil
	}
	var count int64
	if err := r.db.WithContext(ctx).Model(&database.User{}).
		Where("id = ?", buyerID).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}
