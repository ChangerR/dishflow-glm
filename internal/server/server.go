// Package server 连接 HTTP 路由、中间件和健康检查，并装配各业务域 handler。
package server

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/dishflow/zshop/internal/admin"
	"github.com/dishflow/zshop/internal/audit"
	"github.com/dishflow/zshop/internal/authn"
	"github.com/dishflow/zshop/internal/callbacks"
	exportpkg "github.com/dishflow/zshop/internal/export"
	"github.com/dishflow/zshop/internal/config"
	"github.com/dishflow/zshop/internal/customer"
	"github.com/dishflow/zshop/internal/customerauth"
	"github.com/dishflow/zshop/internal/httpx"
	"github.com/dishflow/zshop/internal/inventory"
	"github.com/dishflow/zshop/internal/marketing"
	"github.com/dishflow/zshop/internal/materials"
	"github.com/dishflow/zshop/internal/members"
	"github.com/dishflow/zshop/internal/menu"
	"github.com/dishflow/zshop/internal/orders"
	"github.com/dishflow/zshop/internal/payments"
	"github.com/dishflow/zshop/internal/platform"
	"github.com/dishflow/zshop/internal/printing"
	"github.com/dishflow/zshop/internal/pricing"
	"github.com/dishflow/zshop/internal/reliability"
	"github.com/dishflow/zshop/internal/refunds"
	"github.com/dishflow/zshop/internal/security"
	"github.com/dishflow/zshop/internal/storage"
	"github.com/dishflow/zshop/internal/analytics"
	"github.com/dishflow/zshop/internal/storefront"
	"github.com/dishflow/zshop/internal/tables"
	"github.com/go-chi/chi/v5"
	"github.com/redis/go-redis/v9"
)

// Server holds the dependencies needed to build the router.
type Server struct {
	cfg config.Config
	log *slog.Logger
	db  *sql.DB
	rdb *redis.Client
}

// New builds a Server with connected dependencies.
func New(cfg config.Config, log *slog.Logger, db *sql.DB, rdb *redis.Client) *Server {
	return &Server{cfg: cfg, log: log, db: db, rdb: rdb}
}

// Router builds and returns the HTTP router.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()

	// 全局中间件顺序：request-id → recover → security headers → logging。
	r.Use(httpx.RequestIDMiddleware)
	r.Use(httpx.RecoverMiddleware(s.log))
	r.Use(httpx.SecurityHeadersMiddleware)
	r.Use(httpx.LoggingMiddleware(s.log))
	// 幂等键头格式校验（业务复用在 P4+ 接入）。
	r.Use(reliability.Middleware)

	// 健康检查（PRD §19）。
	r.Get("/health/live", s.handleHealthLive)
	r.Get("/health/ready", s.handleHealthReady)

	// 微信支付/退款回调（PRD §14.2/§14.3；路径含门店 ID）。
	payStore := payments.NewStore(s.db, payments.MockProvider{})
	refStore := refunds.NewStore(s.db)
	cb := callbacks.New(s.db, payStore, refStore, s.cfg.DevMode)
	r.Post("/callbacks/wechat-pay/transactions/{store_id}", cb.WechatPayTransaction)

	// 业务 API 前缀 /api/v1（PRD §16）。
	r.Route("/api/v1", func(r chi.Router) {
		s.mountCustomer(r)
		s.mountAdmin(r)
	})

	// 托管管理后台静态文件（生产模式，PRD §2.1：API 可托管管理后台静态文件）。
	staticDir := os.Getenv("SHOP_ADMIN_STATIC_DIR")
	if staticDir == "" {
		staticDir = "apps/admin/dist"
	}
	if _, err := os.Stat(staticDir); err == nil {
		fs := http.StripPrefix("/", http.FileServer(http.Dir(staticDir)))
		r.Get("/*", func(w http.ResponseWriter, r *http.Request) {
			// SPA fallback：非 API/health/callbacks 路径返回 index.html。
			p := staticDir + r.URL.Path
			if _, err := os.Stat(p); err != nil {
				http.ServeFile(w, r, staticDir+"/index.html")
				return
			}
			fs.ServeHTTP(w, r)
		})
	}

	return r
}

// mountCustomer 装配顾客侧路由（PRD §4/§16.1）。
// 鉴权：Bearer token + X-Wechat-Appid（PRD §2.2）。门店定位全部经 AppID。
func (s *Server) mountCustomer(r chi.Router) {
	sfStore := storefront.NewStore(s.db)
	custAuth := customerauth.NewStore(s.db, customerauth.MockExchanger{}, 7*24*time.Hour)
	quoteStore := pricing.NewQuoteStore(s.rdb, s.cfg.QuoteSigningKeyBytes(), s.cfg.QuoteTTL)
	ordsStore := orders.NewStore(s.db)
	payStore := payments.NewStore(s.db, payments.MockProvider{}) // P6 接真实微信支付 v3
	h := customer.New(sfStore, custAuth, quoteStore)
	ordH := customer.NewOrders(custAuth, quoteStore, ordsStore, s.db)
	payH := customer.NewPayments(custAuth, ordsStore, payStore)
	mktStore := marketing.NewStore(s.db)
	enc, _ := security.NewEncryptor(s.cfg.CredentialKey32())
	memStore := members.NewStore(s.db, enc)
	meH := customer.NewMe(mktStore, memStore)
	h = h.WithMarketing(mktStore)

	// 公开（无需 Bearer）：bootstrap、store、menu、pickup-slots、tables、session、pricing/preview、coupon-offers。
	r.Get("/storefront/bootstrap", h.Bootstrap)
	r.Get("/store", h.StoreInfo)
	r.Get("/menu", h.Menu)
	r.Get("/pickup-slots", h.PickupSlots)
	r.Get("/tables/resolve", h.ResolveTable)
	r.Get("/coupon-offers", h.CouponOffers)
	r.Post("/auth/wechat/session", h.WechatSession)
	r.Post("/pricing/preview", h.PricingPreview) // 算价 + 报价签发

	// 需 Bearer：订单/支付/我的/会员/优惠券（PRD §4.7-§4.13）。
	r.Group(func(r chi.Router) {
		r.Use(customerauth.BearerMiddleware(custAuth))
		r.Post("/orders", ordH.Create)
		r.Get("/orders", ordH.MyList)
		r.Get("/orders/{id}", ordH.MyDetail)
		r.Post("/orders/{id}/cancel", ordH.CancelOrder)
		r.Post("/orders/{id}/prepay", payH.Prepay)
		r.Post("/orders/{id}/mock-payment/confirm", payH.ConfirmMockPayment)
		r.Get("/me", meH.Me)
		r.Get("/me/points", meH.PointsLedger)
		r.Get("/me/rewards", meH.Rewards)
		r.Post("/me/membership", meH.JoinMembership)
		r.Get("/coupons", meH.MyCoupons)
		r.Post("/coupon-offers/{template_id}/claim", meH.Claim)
	})
}

// mountAdmin 装配管理后台路由（PRD §5/§16.2）。
func (s *Server) mountAdmin(r chi.Router) {
	authStore := authn.NewStore(s.db)
	limiter := authn.NewRateLimiter()
	platStore := platform.NewStore(s.db)
	menuStore := menu.NewStore(s.db)
	invStore := inventory.NewStore(s.db)
	ordsStore := orders.NewStore(s.db)
	mktStore := marketing.NewStore(s.db)
	enc, _ := security.NewEncryptor(s.cfg.CredentialKey32())
	memStore := members.NewStore(s.db, enc)
	tblStore := tables.NewStore(s.db)
	anStore := analytics.NewStore(s.db)
	expStore := exportpkg.NewStore(s.db)
	matStore := materials.NewStore(s.db)
	prnStore := printing.NewStore(s.db, printing.MockPrintProvider{})
	_ = audit.NewStore(s.db) // 预留：门店运营审计在后续接入

	sessH := admin.NewSessionHandlers(authStore, limiter, s.cfg.SessionIdleTTL, s.cfg.SessionAbsoluteTTL, s.cfg.DevMode)
	platH := admin.NewPlatformHandlers(platStore)
	menuH := admin.NewMenuHandlers(menuStore)
	invH := admin.NewInventoryHandlers(invStore)
	ordH := admin.NewOrderHandlers(ordsStore, s.db)
	mktH := admin.NewMarketingHandlers(mktStore, memStore, tblStore)
	anH := admin.NewAnalyticsHandlers(anStore, expStore)
	opsH := admin.NewMaterialsHandlers(matStore, prnStore)

	// /admin —— 会话公开端点（登录/提交申请无需认证）。
	r.Route("/admin", func(r chi.Router) {
		// 会话登录/退出（公开）。
		r.Post("/session", sessH.Login)
		r.Delete("/session", sessH.Logout)

		// 受认证保护区域。
		r.Group(func(r chi.Router) {
			r.Use(authn.Middleware(authStore, s.cfg.SessionIdleTTL, s.cfg.SessionAbsoluteTTL, s.cfg.SessionRenewSlack))
			r.Get("/session", sessH.SessionInfo)

			// 我的开店/加入申请（任意已登录账号）。
			r.Post("/shop-applications", platH.SubmitShopApplication)
			r.Get("/my/shop-applications", platH.MyShopApplications)
			r.Post("/shop-join-requests", platH.SubmitShopJoinRequest)
			r.Get("/my/shop-join-requests", platH.MyShopJoinRequests)

			// 门店运营：需 X-Store-Id + 成员关系（任意角色可读；写按角色）。
			r.Group(func(r chi.Router) {
				r.Use(authn.RequireMember)
				// 分类（MANAGER+ 写）。
				r.Get("/categories", menuH.ListCategories)
				r.With(authn.RequireRole(authn.RoleManager)).Post("/categories", menuH.CreateCategory)
				r.With(authn.RequireRole(authn.RoleManager)).Patch("/categories/{id}", menuH.PatchCategory)
				r.With(authn.RequireRole(authn.RoleManager)).Delete("/categories/{id}", menuH.DeleteCategory)
				r.With(authn.RequireRole(authn.RoleManager)).Post("/categories/{id}/restore", menuH.RestoreCategory)
				// 菜品。
				r.Get("/dishes", menuH.ListDishes)
				r.Get("/dishes/{id}", menuH.GetDish)
				r.With(authn.RequireRole(authn.RoleManager)).Post("/dishes", menuH.CreateDish)
				r.With(authn.RequireRole(authn.RoleManager)).Delete("/dishes/{id}", menuH.DeleteDish)
				r.With(authn.RequireRole(authn.RoleManager)).Post("/dishes/{id}/restore", menuH.RestoreDish)
				// 库存调整（MANAGER+）。
				r.With(authn.RequireRole(authn.RoleManager)).Post("/dishes/{id}/stock-adjustments", invH.AdjustStock)
				r.Get("/dishes/{id}/stock-adjustments", invH.ListMovements)
				// 订单工作台与详情（STAFF+ 可读，PRD §6.1）。
				r.Get("/orders/board", ordH.Board)
				r.Get("/orders/{id}", ordH.Detail)
				// 状态推进（STAFF+，乐观锁）。
				r.Post("/orders/{id}/transitions", ordH.Transition)
				// 门店主动退款（MANAGER+，PRD §6.3）。
				r.With(authn.RequireRole(authn.RoleManager)).Post("/orders/{id}/refunds", ordH.StaffRefund)
				// 顾客取消退款审核（MANAGER+，PRD §6.4）。
				r.With(authn.RequireRole(authn.RoleManager)).Post("/refunds/{id}/review", ordH.ReviewCancel)
				// 退款列表 / 异常列表 + 重试 / 审计日志（MANAGER+，PRD §6.4/§18）。
				r.With(authn.RequireRole(authn.RoleManager)).Get("/refunds", ordH.ListRefunds)
				r.With(authn.RequireRole(authn.RoleManager)).Get("/exceptions", ordH.ListExceptions)
				r.With(authn.RequireRole(authn.RoleManager)).Post("/exceptions/{id}/retry", ordH.RetryException)
				r.With(authn.RequireRole(authn.RoleManager)).Get("/audit-logs", ordH.ListAuditLogs)
				// 满减（MANAGER+）。
				r.Get("/promotions", mktH.ListPromotions)
				r.With(authn.RequireRole(authn.RoleManager)).Post("/promotions", mktH.CreatePromotion)
				// 桌台（MANAGER+）。
				r.Get("/tables", mktH.ListTables)
				r.With(authn.RequireRole(authn.RoleManager)).Post("/tables", mktH.CreateTable)
				r.With(authn.RequireRole(authn.RoleManager)).Post("/tables/{id}/rotate-token", mktH.RotateTableToken)
				// 会员积分人工调整（MANAGER+，PRD §8.1）。
				r.With(authn.RequireRole(authn.RoleManager)).Post("/customer-members/{customerId}/points-adjustments", mktH.PointsAdjustment)
				// 经营分析（MANAGER+，PRD §9）。
				r.With(authn.RequireRole(authn.RoleManager)).Get("/analytics/overview", anH.Overview)
				r.With(authn.RequireRole(authn.RoleManager)).Get("/analytics/trends", anH.Trends)
				r.With(authn.RequireRole(authn.RoleManager)).Get("/analytics/breakdown", anH.Breakdown)
				// 导出（MANAGER+）；导入仅 OWNER（PRD §11）。
				r.With(authn.RequireRole(authn.RoleManager)).Get("/store/export", anH.Export)
				r.With(authn.RequireRole(authn.RoleOwner)).Post("/store/import", anH.Import)
				// 物料与采购清单（STAFF+，PRD §12）。
				r.Get("/materials", opsH.ListMaterials)
				r.With(authn.RequireRole(authn.RoleManager)).Post("/materials", opsH.CreateMaterial)
				r.With(authn.RequireRole(authn.RoleManager)).Post("/purchase-lists", opsH.CreatePurchaseList)
				r.With(authn.RequireRole(authn.RoleManager)).Post("/purchase-lists/{id}/items", opsH.AddPurchaseItem)
				r.With(authn.RequireRole(authn.RoleManager)).Post("/purchase-lists/{id}/submit", opsH.SubmitPurchaseList)
				r.With(authn.RequireRole(authn.RoleManager)).Post("/purchase-lists/{id}/complete", opsH.CompletePurchaseList)
				// 云打印配置（MANAGER+ 读，PRD §13）。
				r.With(authn.RequireRole(authn.RoleManager)).Get("/print/config", opsH.GetPrintConfig)
			})

			// 平台超管（PRD §3.5）。
			r.Group(func(r chi.Router) {
				r.Use(authn.RequirePlatformAdmin)
				// 门店。
				r.Post("/platform/stores", platH.CreateStore)
				r.Get("/platform/stores", platH.ListStores)
				r.Patch("/platform/stores/{id}", platH.PatchStore)
				// 后台账号。
				r.Post("/platform/users", platH.CreateAccount)
				r.Get("/platform/users", platH.ListAccounts)
				r.Patch("/platform/users/{id}", platH.PatchAccount)
				r.Post("/platform/users/{id}/assign-store-owner", platH.AssignStoreOwner)
				// 开店申请审批。
				r.Get("/platform/shop-applications", platH.ListPlatformShopApplications)
				r.Post("/platform/shop-applications/{id}/review", platH.ReviewShopApplication)
			})
		})
	})
}

func (s *Server) handleHealthLive(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleHealthReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	problems := []string{}

	if s.db == nil {
		problems = append(problems, "mysql not configured")
	} else if err := s.db.PingContext(ctx); err != nil {
		problems = append(problems, "mysql: "+err.Error())
	}

	if s.rdb == nil {
		problems = append(problems, "redis not configured")
	} else if err := s.rdb.Ping(ctx).Err(); err != nil {
		problems = append(problems, "redis: "+err.Error())
	}

	if len(problems) > 0 {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status":   "unavailable",
			"problems": problems,
		})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// NewLogger builds a slog JSON logger writing to stderr at the configured level.
func NewLogger(cfg config.Config) *slog.Logger {
	var level slog.Level
	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}

// 编译期保证 storage.SQLDB 签名被使用（cmd/serve 调用）。
var _ = storage.SQLDB
