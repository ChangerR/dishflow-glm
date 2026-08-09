package admin

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/dishflow/zshop/internal/authn"
	"github.com/dishflow/zshop/internal/httpx"
	"github.com/dishflow/zshop/internal/menu"
)

// MenuHandlers 菜单/库存管理后台 handler（PRD §7.1/§7.2/§7.3）。
type MenuHandlers struct {
	menu *menu.Store
}

// NewMenuHandlers 构造菜单 handler。
func NewMenuHandlers(m *menu.Store) *MenuHandlers { return &MenuHandlers{menu: m} }

func currentStore(r *http.Request) (int64, bool) { return authn.CurrentStoreID(r.Context()) }

// ListCategories GET /api/v1/admin/categories
func (h *MenuHandlers) ListCategories(w http.ResponseWriter, r *http.Request) {
	storeID, ok := currentStore(r)
	if !ok {
		authn.Forbidden(w, r)
		return
	}
	includeDeleted := r.URL.Query().Has("deleted")
	cats, err := h.menu.ListCategories(r.Context(), storeID, includeDeleted)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	items := make([]map[string]any, 0, len(cats))
	for _, c := range cats {
		items = append(items, categoryToDTO(c))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

type categoryReq struct {
	Name      string `json:"name"`
	Enabled   *bool  `json:"enabled"`
	SortOrder *int   `json:"sort_order"`
}

// CreateCategory POST /api/v1/admin/categories
func (h *MenuHandlers) CreateCategory(w http.ResponseWriter, r *http.Request) {
	storeID, ok := currentStore(r)
	if !ok {
		authn.Forbidden(w, r)
		return
	}
	var req categoryReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	so := 0
	if req.SortOrder != nil {
		so = *req.SortOrder
	}
	id, err := h.menu.CreateCategory(r.Context(), menu.CreateCategoryInput{
		StoreID: storeID, Name: req.Name, Enabled: enabled, SortOrder: so,
	})
	if err != nil {
		httpx.WriteError(w, r, httpx.New(httpx.CodeConflict, http.StatusConflict, err.Error()))
		return
	}
	cats, _ := h.menu.ListCategories(r.Context(), storeID, true)
	for _, c := range cats {
		if c.ID == id {
			httpx.WriteJSON(w, http.StatusCreated, categoryToDTO(c))
			return
		}
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"id": id})
}

// PatchCategory PATCH /api/v1/admin/categories/{id}
func (h *MenuHandlers) PatchCategory(w http.ResponseWriter, r *http.Request) {
	storeID, ok := currentStore(r)
	if !ok {
		authn.Forbidden(w, r)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteError(w, r, httpx.New(httpx.CodeBadRequest, http.StatusBadRequest, "invalid id"))
		return
	}
	var req categoryReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	in := menu.UpdateCategoryInput{Name: &req.Name}
	if req.Enabled != nil {
		in.Enabled = req.Enabled
	}
	if req.SortOrder != nil {
		in.SortOrder = req.SortOrder
	}
	if err := h.menu.UpdateCategory(r.Context(), storeID, id, in); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteError(w, r, httpx.New(httpx.CodeNotFound, http.StatusNotFound, "category not found"))
			return
		}
		httpx.WriteError(w, r, httpx.New(httpx.CodeBadRequest, http.StatusBadRequest, err.Error()))
		return
	}
	cats, _ := h.menu.ListCategories(r.Context(), storeID, true)
	for _, c := range cats {
		if c.ID == id {
			httpx.WriteJSON(w, http.StatusOK, categoryToDTO(c))
			return
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"id": id})
}

// DeleteCategory DELETE /api/v1/admin/categories/{id}
func (h *MenuHandlers) DeleteCategory(w http.ResponseWriter, r *http.Request) {
	storeID, ok := currentStore(r)
	if !ok {
		authn.Forbidden(w, r)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteError(w, r, httpx.New(httpx.CodeBadRequest, http.StatusBadRequest, "invalid id"))
		return
	}
	batch, err := h.menu.DeleteCategory(r.Context(), storeID, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteError(w, r, httpx.New(httpx.CodeNotFound, http.StatusNotFound, "category not found"))
			return
		}
		httpx.WriteError(w, r, httpx.New(httpx.CodeConflict, http.StatusConflict, err.Error()))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"id": id, "deleted": true, "delete_batch_id": batch})
}

// RestoreCategory POST /api/v1/admin/categories/{id}/restore
func (h *MenuHandlers) RestoreCategory(w http.ResponseWriter, r *http.Request) {
	storeID, ok := currentStore(r)
	if !ok {
		authn.Forbidden(w, r)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteError(w, r, httpx.New(httpx.CodeBadRequest, http.StatusBadRequest, "invalid id"))
		return
	}
	if err := h.menu.RestoreCategory(r.Context(), storeID, id); err != nil {
		httpx.WriteError(w, r, httpx.New(httpx.CodeConflict, http.StatusConflict, err.Error()))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"id": id, "restored": true})
}

// ── 菜品 ──────────────────────────────────────────────────────────────

// ListDishes GET /api/v1/admin/dishes
func (h *MenuHandlers) ListDishes(w http.ResponseWriter, r *http.Request) {
	storeID, ok := currentStore(r)
	if !ok {
		authn.Forbidden(w, r)
		return
	}
	var categoryID int64
	if v := r.URL.Query().Get("category_id"); v != "" {
		categoryID, _ = strconv.ParseInt(v, 10, 64)
	}
	includeDeleted := r.URL.Query().Has("deleted")
	prods, err := h.menu.ListProducts(r.Context(), storeID, categoryID, includeDeleted)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	items := make([]map[string]any, 0, len(prods))
	for _, p := range prods {
		items = append(items, productToDTO(p))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// GetDish GET /api/v1/admin/dishes/{id}
func (h *MenuHandlers) GetDish(w http.ResponseWriter, r *http.Request) {
	storeID, ok := currentStore(r)
	if !ok {
		authn.Forbidden(w, r)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteError(w, r, httpx.New(httpx.CodeBadRequest, http.StatusBadRequest, "invalid id"))
		return
	}
	p, skus, ogs, items, err := h.menu.GetProductDetail(r.Context(), storeID, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteError(w, r, httpx.New(httpx.CodeNotFound, http.StatusNotFound, "dish not found"))
			return
		}
		httpx.WriteError(w, r, err)
		return
	}
	dto := productToDTO(p)
	skuList := make([]map[string]any, 0, len(skus))
	for _, s := range skus {
		skuList = append(skuList, skuToDTO(s))
	}
	ogList := make([]map[string]any, 0, len(ogs))
	for _, g := range ogs {
		gdto := optionGroupToDTO(g)
		oiList := make([]map[string]any, 0)
		for _, oi := range items[g.ID] {
			oiList = append(oiList, optionItemToDTO(oi))
		}
		gdto["items"] = oiList
		ogList = append(ogList, gdto)
	}
	dto["skus"] = skuList
	dto["option_groups"] = ogList
	httpx.WriteJSON(w, http.StatusOK, dto)
}

type createDishReq struct {
	CategoryID      int64                    `json:"category_id"`
	Code            string                   `json:"code"`
	Name            string                   `json:"name"`
	Description     string                   `json:"description"`
	ImageURL        string                   `json:"image_url"`
	Enabled         bool                     `json:"enabled"`
	ManuallySoldOut bool                     `json:"manually_sold_out"`
	SortOrder       int                      `json:"sort_order"`
	PackagingFee    int64                    `json:"packaging_fee_cents"`
	SKUs            []map[string]any         `json:"skus"`
	OptionGroups    []map[string]any         `json:"option_groups"`
}

// CreateDish POST /api/v1/admin/dishes
func (h *MenuHandlers) CreateDish(w http.ResponseWriter, r *http.Request) {
	storeID, ok := currentStore(r)
	if !ok {
		authn.Forbidden(w, r)
		return
	}
	var req createDishReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	in := menu.CreateProductInput{
		StoreID: storeID, CategoryID: req.CategoryID, Code: req.Code, Name: req.Name,
		Description: req.Description, ImageURL: req.ImageURL, Enabled: req.Enabled,
		ManuallySoldOut: req.ManuallySoldOut, SortOrder: req.SortOrder, PackagingFeeCents: req.PackagingFee,
	}
	for _, sk := range req.SKUs {
		in.SKUs = append(in.SKUs, menu.SKUInput{
			Name: str(sk, "name"), PriceCents: i64(sk, "price_cents"),
			InventoryMode: str(sk, "inventory_mode"), DailyStock: i(sk, "daily_stock"),
			Enabled: boolv(sk, "enabled", true), IsDefault: boolv(sk, "is_default", false),
			SortOrder: i(sk, "sort_order"),
		})
	}
	for _, g := range req.OptionGroups {
		og := menu.OptionGroupInput{
			Name: str(g, "name"), SelectionType: str(g, "selection_type"),
			IsRequired: boolv(g, "is_required", false), MinSelect: i(g, "min_select"),
			MaxSelect: i(g, "max_select"), SortOrder: i(g, "sort_order"),
		}
		if og.MaxSelect == 0 {
			og.MaxSelect = 1
		}
		if og.MinSelect == 0 && og.SelectionType != "MULTI" {
			og.MinSelect = 1
		}
		for _, oi := range mapSlice(g, "items") {
			og.Items = append(og.Items, menu.OptionItemInput{
				Name: str(oi, "name"), PriceModifierCents: i64(oi, "price_modifier_cents"),
				Enabled: boolv(oi, "enabled", true), IsDefault: boolv(oi, "is_default", false),
				SortOrder: i(oi, "sort_order"),
			})
		}
		in.OptionGroups = append(in.OptionGroups, og)
	}
	id, err := h.menu.CreateProduct(r.Context(), in)
	if err != nil {
		httpx.WriteError(w, r, httpx.New(httpx.CodeBadRequest, http.StatusBadRequest, err.Error()))
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"id": id})
}

// DeleteDish DELETE /api/v1/admin/dishes/{id}
func (h *MenuHandlers) DeleteDish(w http.ResponseWriter, r *http.Request) {
	storeID, ok := currentStore(r)
	if !ok {
		authn.Forbidden(w, r)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteError(w, r, httpx.New(httpx.CodeBadRequest, http.StatusBadRequest, "invalid id"))
		return
	}
	batch, err := h.menu.DeleteProduct(r.Context(), storeID, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteError(w, r, httpx.New(httpx.CodeNotFound, http.StatusNotFound, "dish not found"))
			return
		}
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"id": id, "deleted": true, "delete_batch_id": batch})
}

// RestoreDish POST /api/v1/admin/dishes/{id}/restore
func (h *MenuHandlers) RestoreDish(w http.ResponseWriter, r *http.Request) {
	storeID, ok := currentStore(r)
	if !ok {
		authn.Forbidden(w, r)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteError(w, r, httpx.New(httpx.CodeBadRequest, http.StatusBadRequest, "invalid id"))
		return
	}
	if err := h.menu.RestoreProduct(r.Context(), storeID, id); err != nil {
		httpx.WriteError(w, r, httpx.New(httpx.CodeConflict, http.StatusConflict, err.Error()))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"id": id, "restored": true})
}

// ── DTO 辅助 ──────────────────────────────────────────────────────────

func categoryToDTO(c menu.Category) map[string]any {
	dto := map[string]any{
		"id": c.ID, "name": c.Name, "enabled": c.Enabled, "sort_order": c.SortOrder,
		"created_at": c.CreatedAt, "updated_at": c.UpdatedAt,
	}
	if c.DeletedAt.Valid {
		dto["deleted_at"] = c.DeletedAt.Time.Format("2006-01-02T15:04:05Z07:00")
		dto["delete_batch_id"] = c.DeleteBatchID
	}
	return dto
}

func productToDTO(p menu.Product) map[string]any {
	dto := map[string]any{
		"id": p.ID, "category_id": p.CategoryID, "code": p.Code, "name": p.Name,
		"description": p.Description, "image_url": p.ImageURL, "enabled": p.Enabled,
		"manually_sold_out": p.ManuallySoldOut, "sort_order": p.SortOrder,
		"packaging_fee_cents": p.PackagingFeeCents, "created_at": p.CreatedAt, "updated_at": p.UpdatedAt,
	}
	if p.DeletedAt.Valid {
		dto["deleted_at"] = p.DeletedAt.Time.Format("2006-01-02T15:04:05Z07:00")
		dto["delete_batch_id"] = p.DeleteBatchID
	}
	return dto
}

func skuToDTO(s menu.SKU) map[string]any {
	return map[string]any{
		"id": s.ID, "name": s.Name, "price_cents": s.PriceCents, "inventory_mode": s.InventoryMode,
		"daily_stock": s.DailyStock, "enabled": s.Enabled, "is_default": s.IsDefault, "sort_order": s.SortOrder,
	}
}

func optionGroupToDTO(g menu.OptionGroup) map[string]any {
	return map[string]any{
		"id": g.ID, "name": g.Name, "selection_type": g.SelectionType, "is_required": g.IsRequired,
		"min_select": g.MinSelect, "max_select": g.MaxSelect, "sort_order": g.SortOrder,
	}
}

func optionItemToDTO(oi menu.OptionItem) map[string]any {
	return map[string]any{
		"id": oi.ID, "name": oi.Name, "price_modifier_cents": oi.PriceModifierCents,
		"enabled": oi.Enabled, "is_default": oi.IsDefault, "sort_order": oi.SortOrder,
	}
}

// ── 通用 map 取值辅助 ─────────────────────────────────────────────────

func str(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}
func i(m map[string]any, k string) int {
	switch v := m[k].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}
func i64(m map[string]any, k string) int64 {
	switch v := m[k].(type) {
	case float64:
		return int64(v)
	case int:
		return int64(v)
	}
	return 0
}
func boolv(m map[string]any, k string, def bool) bool {
	if v, ok := m[k].(bool); ok {
		return v
	}
	return def
}
func mapSlice(m map[string]any, k string) []map[string]any {
	raw, ok := m[k].([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(raw))
	for _, x := range raw {
		if mm, ok := x.(map[string]any); ok {
			out = append(out, mm)
		}
	}
	return out
}
