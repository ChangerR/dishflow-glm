package admin

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/dishflow/zshop/internal/authn"
	"github.com/dishflow/zshop/internal/httpx"
	"github.com/dishflow/zshop/internal/platform"
	"github.com/dishflow/zshop/internal/web"
)

// PlatformHandlers 平台超管接口（PRD §10.5/§3.5）。
type PlatformHandlers struct {
	plat *platform.Store
}

// NewPlatformHandlers 构造平台 handler。
func NewPlatformHandlers(plat *platform.Store) *PlatformHandlers {
	return &PlatformHandlers{plat: plat}
}

// ── 平台门店管理 ──────────────────────────────────────────────────────

type createStoreReq struct {
	Name     string `json:"name"`
	Timezone string `json:"timezone"`
	Phone    string `json:"phone"`
	Address  string `json:"address"`
}

// CreateStore POST /api/v1/admin/platform/stores
func (h *PlatformHandlers) CreateStore(w http.ResponseWriter, r *http.Request) {
	var req createStoreReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	id, err := h.plat.CreateStore(r.Context(), platform.CreateStoreInput{
		Name: web.TrimTitle(req.Name, 100), Timezone: req.Timezone, Phone: req.Phone, Address: req.Address,
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	st, _ := h.plat.GetShop(r.Context(), id)
	httpx.WriteJSON(w, http.StatusCreated, shopToDTO(st))
}

// ListStores GET /api/v1/admin/platform/stores
func (h *PlatformHandlers) ListStores(w http.ResponseWriter, r *http.Request) {
	p := web.ParsePage(r)
	cur, _ := web.DecodeCursor(p.Cursor)
	curID, _ := strconv.ParseInt(cur, 10, 64)
	stores, last, err := h.plat.ListShops(r.Context(), p.Limit, curID)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	items := make([]map[string]any, 0, len(stores))
	for _, s := range stores {
		items = append(items, shopToDTO(s))
	}
	next := ""
	if len(items) == p.Limit {
		next = strconv.FormatInt(last, 10)
	}
	web.WriteList(w, items, next)
}

// PatchStore PATCH /api/v1/admin/platform/stores/{id}
func (h *PlatformHandlers) PatchStore(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteError(w, r, httpx.New(httpx.CodeBadRequest, http.StatusBadRequest, "invalid id"))
		return
	}
	var req struct {
		Name     *string `json:"name"`
		Enabled  *bool   `json:"enabled"`
		Timezone *string `json:"timezone"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if req.Name != nil {
		req.Name = ptrStr(web.TrimTitle(*req.Name, 100))
	}
	if err := h.plat.UpdateStore(r.Context(), id, platform.UpdateStoreInput{
		Name: req.Name, Enabled: req.Enabled, Timezone: req.Timezone,
	}); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteError(w, r, httpx.New(httpx.CodeNotFound, http.StatusNotFound, "store not found"))
			return
		}
		httpx.WriteError(w, r, err)
		return
	}
	st, _ := h.plat.GetShop(r.Context(), id)
	httpx.WriteJSON(w, http.StatusOK, shopToDTO(st))
}

// AssignStoreOwner POST /api/v1/admin/platform/users/{id}/assign-store-owner
// body: {store_id, ...}（PRD §10.5）。
func (h *PlatformHandlers) AssignStoreOwner(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || userID <= 0 {
		httpx.WriteError(w, r, httpx.New(httpx.CodeBadRequest, http.StatusBadRequest, "invalid user id"))
		return
	}
	var req struct {
		StoreID int64 `json:"store_id"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if req.StoreID <= 0 {
		httpx.WriteError(w, r, httpx.New(httpx.CodeBadRequest, http.StatusBadRequest, "store_id required"))
		return
	}
	if err := h.plat.AssignStoreOwner(r.Context(), req.StoreID, userID); err != nil {
		httpx.WriteError(w, r, httpx.New(httpx.CodeConflict, http.StatusConflict, err.Error()))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "store_id": req.StoreID, "admin_user_id": userID})
}

// ── 平台后台账号 ──────────────────────────────────────────────────────

type createAccountReq struct {
	Login           string `json:"login"`
	DisplayName     string `json:"display_name"`
	Password        string `json:"password"`
	IsPlatformAdmin bool   `json:"is_platform_admin"`
}

// CreateAccount POST /api/v1/admin/platform/users
func (h *PlatformHandlers) CreateAccount(w http.ResponseWriter, r *http.Request) {
	var req createAccountReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	id, err := h.plat.CreateAdminAccount(r.Context(), platform.CreateAdminAccountInput{
		Login: req.Login, DisplayName: web.TrimTitle(req.DisplayName, 100),
		Password: req.Password, IsPlatformAdmin: req.IsPlatformAdmin,
	})
	if err != nil {
		httpx.WriteError(w, r, httpx.New(httpx.CodeConflict, http.StatusConflict, err.Error()))
		return
	}
	a, _ := h.plat.GetAdminAccount(r.Context(), id)
	httpx.WriteJSON(w, http.StatusCreated, accountToDTO(a))
}

// ListAccounts GET /api/v1/admin/platform/users
func (h *PlatformHandlers) ListAccounts(w http.ResponseWriter, r *http.Request) {
	p := web.ParsePage(r)
	cur, _ := web.DecodeCursor(p.Cursor)
	curID, _ := strconv.ParseInt(cur, 10, 64)
	accs, last, err := h.plat.ListAdminAccounts(r.Context(), p.Limit, curID)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	items := make([]map[string]any, 0, len(accs))
	for _, a := range accs {
		items = append(items, accountToDTO(a))
	}
	next := ""
	if len(items) == p.Limit {
		next = strconv.FormatInt(last, 10)
	}
	web.WriteList(w, items, next)
}

// PatchAccount PATCH /api/v1/admin/platform/users/{id}（登录账号不可改，PRD §10.5）。
func (h *PlatformHandlers) PatchAccount(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteError(w, r, httpx.New(httpx.CodeBadRequest, http.StatusBadRequest, "invalid id"))
		return
	}
	var req struct {
		DisplayName     *string `json:"display_name"`
		Enabled         *bool   `json:"enabled"`
		IsPlatformAdmin *bool   `json:"is_platform_admin"`
		NewPassword     *string `json:"new_password"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if req.DisplayName != nil {
		req.DisplayName = ptrStr(web.TrimTitle(*req.DisplayName, 100))
	}
	if err := h.plat.UpdateAdminAccount(r.Context(), id, platform.UpdateAdminAccountInput{
		DisplayName: req.DisplayName, Enabled: req.Enabled,
		IsPlatformAdmin: req.IsPlatformAdmin, NewPassword: req.NewPassword,
	}); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteError(w, r, httpx.New(httpx.CodeNotFound, http.StatusNotFound, "account not found"))
			return
		}
		httpx.WriteError(w, r, httpx.New(httpx.CodeBadRequest, http.StatusBadRequest, err.Error()))
		return
	}
	a, _ := h.plat.GetAdminAccount(r.Context(), id)
	httpx.WriteJSON(w, http.StatusOK, accountToDTO(a))
}

// ── 平台开店申请审批 ──────────────────────────────────────────────────

// ReviewShopApplication POST /api/v1/admin/platform/shop-applications/{id}/review
// body: {decision: APPROVE|REJECT, note}
func (h *PlatformHandlers) ReviewShopApplication(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteError(w, r, httpx.New(httpx.CodeBadRequest, http.StatusBadRequest, "invalid id"))
		return
	}
	var req struct {
		Decision string `json:"decision"`
		Note     string `json:"note"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	user, _ := authn.AdminUserFrom(r.Context())
	switch req.Decision {
	case "APPROVE":
		storeID, err := h.plat.ApproveShopApplication(r.Context(), id, user.ID, req.Note)
		if err != nil {
			httpx.WriteError(w, r, httpx.New(httpx.CodeConflict, http.StatusConflict, err.Error()))
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"status": "approved", "store_id": storeID})
	case "REJECT":
		if err := h.plat.RejectShopApplication(r.Context(), id, user.ID, req.Note); err != nil {
			httpx.WriteError(w, r, httpx.New(httpx.CodeConflict, http.StatusConflict, err.Error()))
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "rejected"})
	default:
		httpx.WriteError(w, r, httpx.New(httpx.CodeBadRequest, http.StatusBadRequest, "decision must be APPROVE or REJECT"))
	}
}

// ListPlatformShopApplications GET /api/v1/admin/platform/shop-applications
func (h *PlatformHandlers) ListPlatformShopApplications(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	apps, err := h.plat.ListPlatformShopApplications(r.Context(), status)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	web.WriteList(w, apps, "")
}

// ── “我的”开店/加入申请（无门店账号）────────────────────────────────

func (h *PlatformHandlers) SubmitShopApplication(w http.ResponseWriter, r *http.Request) {
	user, ok := authn.AdminUserFrom(r.Context())
	if !ok {
		authn.Unauthorized(w, r)
		return
	}
	var req struct {
		StoreName string `json:"store_name"`
		Contact   string `json:"contact"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	id, err := h.plat.CreateShopApplication(r.Context(), user.ID, req.StoreName, req.Contact)
	if err != nil {
		httpx.WriteError(w, r, httpx.New(httpx.CodeBadRequest, http.StatusBadRequest, err.Error()))
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"id": id, "status": "PENDING"})
}

func (h *PlatformHandlers) MyShopApplications(w http.ResponseWriter, r *http.Request) {
	user, ok := authn.AdminUserFrom(r.Context())
	if !ok {
		authn.Unauthorized(w, r)
		return
	}
	apps, err := h.plat.ListMyShopApplications(r.Context(), user.ID)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	web.WriteList(w, apps, "")
}

func (h *PlatformHandlers) SubmitShopJoinRequest(w http.ResponseWriter, r *http.Request) {
	user, ok := authn.AdminUserFrom(r.Context())
	if !ok {
		authn.Unauthorized(w, r)
		return
	}
	var req struct {
		StoreID int64  `json:"store_id"`
		Role    string `json:"role"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	role := authn.Role(req.Role)
	if role != authn.RoleStaff && role != authn.RoleManager && role != authn.RoleOwner {
		httpx.WriteError(w, r, httpx.New(httpx.CodeBadRequest, http.StatusBadRequest, "invalid role"))
		return
	}
	id, err := h.plat.CreateShopJoinRequest(r.Context(), req.StoreID, user.ID, role)
	if err != nil {
		httpx.WriteError(w, r, httpx.New(httpx.CodeConflict, http.StatusConflict, err.Error()))
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"id": id, "status": "PENDING"})
}

func (h *PlatformHandlers) MyShopJoinRequests(w http.ResponseWriter, r *http.Request) {
	user, ok := authn.AdminUserFrom(r.Context())
	if !ok {
		authn.Unauthorized(w, r)
		return
	}
	reqs, err := h.plat.ListMyShopJoinRequests(r.Context(), user.ID)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	web.WriteList(w, reqs, "")
}

// ── DTO 辅助 ──────────────────────────────────────────────────────────

func shopToDTO(s platform.Shop) map[string]any {
	dto := map[string]any{
		"id":                   s.ID,
		"name":                 s.Name,
		"enabled":              s.Enabled,
		"business_open":        s.BusinessOpen,
		"phone":                s.Phone,
		"address":              s.Address,
		"business_hours":       s.BusinessHours,
		"announcement":         s.Announcement,
		"timezone":             s.Timezone,
		"pickup_minutes":       s.PickupMinutes,
		"scheduled_pickup_enabled": s.ScheduledPickupEnabled,
		"pickup_advance_days":  s.PickupAdvanceDays,
		"pickup_slot_minutes":  s.PickupSlotMinutes,
		"pickup_slot_capacity": s.PickupSlotCapacity,
		"pickup_min_lead_minutes": s.PickupMinLeadMinutes,
		"points_per_yuan":      s.PointsPerYuan,
		"created_at":           s.CreatedAt,
		"updated_at":           s.UpdatedAt,
	}
	if s.NewMemberCouponTemplateID.Valid {
		dto["new_member_coupon_template_id"] = s.NewMemberCouponTemplateID.Int64
	}
	return dto
}

func accountToDTO(a platform.AdminAccount) map[string]any {
	dto := map[string]any{
		"id":                a.ID,
		"login":             a.Login,
		"display_name":      a.DisplayName,
		"enabled":           a.Enabled,
		"is_platform_admin": a.IsPlatformAdmin,
		"created_at":        a.CreatedAt,
	}
	if a.LastLoginAt.Valid {
		dto["last_login_at"] = a.LastLoginAt.Time.Format("2006-01-02T15:04:05Z07:00")
	}
	return dto
}

func ptrStr(s string) *string { return &s }

// 防止未用导入
var _ = json.Marshal
