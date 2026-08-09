package admin

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/dishflow/zshop/internal/authn"
	"github.com/dishflow/zshop/internal/httpx"
	"github.com/dishflow/zshop/internal/materials"
	"github.com/dishflow/zshop/internal/printing"
)

// MaterialsHandlers 物料/采购/打印 handler（PRD §12/§13）。
type MaterialsHandlers struct {
	mat *materials.Store
	prn *printing.Store
}

// NewMaterialsHandlers 构造。
func NewMaterialsHandlers(mat *materials.Store, prn *printing.Store) *MaterialsHandlers {
	return &MaterialsHandlers{mat: mat, prn: prn}
}

// ── 物料 ──────────────────────────────────────────────────────────────

func (h *MaterialsHandlers) ListMaterials(w http.ResponseWriter, r *http.Request) {
	storeID, ok := currentStore(r)
	if !ok {
		authn.Forbidden(w, r)
		return
	}
	ms, err := h.mat.ListMaterials(r.Context(), storeID, r.URL.Query().Get("search"), r.URL.Query().Get("category"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": ms})
}

func (h *MaterialsHandlers) CreateMaterial(w http.ResponseWriter, r *http.Request) {
	storeID, ok := currentStore(r)
	if !ok {
		authn.Forbidden(w, r)
		return
	}
	var req struct {
		Name     string `json:"name"`
		Unit     string `json:"unit"`
		Category string `json:"category"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	id, err := h.mat.CreateMaterial(r.Context(), storeID, req.Name, req.Unit, req.Category)
	if err != nil {
		httpx.WriteError(w, r, httpx.New(httpx.CodeConflict, http.StatusConflict, err.Error()))
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

// ── 采购清单 ──────────────────────────────────────────────────────────

func (h *MaterialsHandlers) CreatePurchaseList(w http.ResponseWriter, r *http.Request) {
	storeID, ok := currentStore(r)
	if !ok {
		authn.Forbidden(w, r)
		return
	}
	var req struct {
		BusinessDate string `json:"business_date"`
		Title        string `json:"title"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	user, _ := authn.AdminUserFrom(r.Context())
	id, err := h.mat.CreateList(r.Context(), storeID, req.BusinessDate, req.Title, user.ID)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (h *MaterialsHandlers) AddPurchaseItem(w http.ResponseWriter, r *http.Request) {
	storeID, ok := currentStore(r)
	if !ok {
		authn.Forbidden(w, r)
		return
	}
	listID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || listID <= 0 {
		httpx.WriteError(w, r, httpx.New(httpx.CodeBadRequest, http.StatusBadRequest, "invalid id"))
		return
	}
	var req struct {
		MaterialID int64   `json:"material_id"`
		Quantity   float64 `json:"quantity"`
		Note       string  `json:"note"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if err := h.mat.AddItem(r.Context(), storeID, listID, req.MaterialID, req.Quantity, req.Note); err != nil {
		httpx.WriteError(w, r, httpx.New(httpx.CodeConflict, http.StatusConflict, err.Error()))
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]string{"status": "ok"})
}

func (h *MaterialsHandlers) SubmitPurchaseList(w http.ResponseWriter, r *http.Request) {
	storeID, ok := currentStore(r)
	if !ok {
		authn.Forbidden(w, r)
		return
	}
	listID, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	user, _ := authn.AdminUserFrom(r.Context())
	var req struct {
		ExpectedVersion int64 `json:"expected_version"`
	}
	httpx.DecodeJSON(r, &req)
	if err := h.mat.SubmitList(r.Context(), storeID, listID, user.ID, req.ExpectedVersion); err != nil {
		httpx.WriteError(w, r, mapStateConflict(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "submitted"})
}

func (h *MaterialsHandlers) CompletePurchaseList(w http.ResponseWriter, r *http.Request) {
	storeID, ok := currentStore(r)
	if !ok {
		authn.Forbidden(w, r)
		return
	}
	listID, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	user, _ := authn.AdminUserFrom(r.Context())
	var req struct {
		ExpectedVersion int64 `json:"expected_version"`
		TotalAmountCents int64 `json:"total_amount_cents"`
	}
	httpx.DecodeJSON(r, &req)
	if err := h.mat.CompleteList(r.Context(), storeID, listID, user.ID, req.ExpectedVersion, req.TotalAmountCents); err != nil {
		httpx.WriteError(w, r, mapStateConflict(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "completed"})
}

// ── 云打印配置 ────────────────────────────────────────────────────────

func (h *MaterialsHandlers) GetPrintConfig(w http.ResponseWriter, r *http.Request) {
	storeID, ok := currentStore(r)
	if !ok {
		authn.Forbidden(w, r)
		return
	}
	c, err := h.prn.GetConfig(r.Context(), storeID)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	// 只返回状态，不回显密钥（PRD §13.1）。
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"status": c.Status, "auto_print": c.AutoPrint, "mock_print": c.MockPrint, "configured": c.Status != "draft",
	})
}

// mapStateConflict 把清单状态机错误映射到 HTTP。
func mapStateConflict(err error) *httpx.Error {
	msg := err.Error()
	if errors.Is(err, materials.ErrVersionConflict) || containsStr(msg, "STATE_CONFLICT") {
		return httpx.New(httpx.CodeStateConflict, http.StatusConflict, msg)
	}
	return httpx.New(httpx.CodeBadRequest, http.StatusBadRequest, msg)
}

// suppress
var _ = errors.New
