package customer

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"sort"

	"github.com/dishflow/zshop/internal/customerauth"
	"github.com/dishflow/zshop/internal/httpx"
	"github.com/dishflow/zshop/internal/pricing"
)

// cartItemReq 客户端购物车项（PRD §4.3：只含 SKU/选项/数量，不含可信价格）。
type cartItemReq struct {
	SKUID     int64   `json:"sku_id"`
	Quantity  int     `json:"quantity"`
	OptionIDs []int64 `json:"option_ids"`
}

// pricingReq pricing/preview 请求体（PRD §4.4.2）。
type pricingReq struct {
	Scenario     pricing.Scenario `json:"scenario"`
	TableToken   string           `json:"table_token"`
	ScheduledFor string           `json:"scheduled_for"`
	Cart         []cartItemReq    `json:"cart"`
	CustomerCouponID int64        `json:"customer_coupon_id"`
}

// PricingPreview POST /api/v1/pricing/preview（PRD §4.4）。
// 服务端重载 SKU/选项/库存/满减/券 → 算价 → 签发十分钟 quote_token。
func (h *Handlers) PricingPreview(w http.ResponseWriter, r *http.Request) {
	b, ok := h.resolveStoreByAppid(w, r)
	if !ok {
		return
	}
	var req pricingReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if req.Scenario == "" {
		req.Scenario = pricing.ScenarioPickup
	}
	// 门店休息时可浏览，但不可结算（PRD §4.4.8）。
	if !b.BusinessOpen {
		httpx.WriteError(w, r, httpx.New(httpx.CodeBadRequest, http.StatusBadRequest, "store closed; cannot checkout"))
		return
	}

	// 堂食需有效桌码（PRD §4.6）。
	if req.Scenario == pricing.ScenarioDineIn {
		if req.TableToken == "" {
			httpx.WriteError(w, r, httpx.New(httpx.CodeTableNotFound, http.StatusBadRequest, "table token required for dine-in"))
			return
		}
		if _, err := h.sf.ResolveTable(r.Context(), req.TableToken); err != nil {
			httpx.WriteError(w, r, httpx.New(httpx.CodeTableDisabled, http.StatusBadRequest, "invalid table token"))
			return
		}
	}

	// 构造算价输入：从公开菜单加载 SKU/选项。
	pinput, err := h.buildPricingInput(r, b.StoreID, req)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}

	result, err := pricing.Price(pinput)
	if err != nil {
		httpx.WriteError(w, r, mapPricingErr(err))
		return
	}

	// 签发 quote（PRD §4.4.5/§4.4.6）。
	cartHash := hashCart(req.Cart)
	summary, err := h.quotes.IssueWithLines(r.Context(),
		b.StoreID, 0 /* 匿名；领券/会员后填顾客 */, req.Scenario, req.TableToken, req.ScheduledFor, cartHash,
		result.ItemAmountCents, result.PackagingFeeCents, result.DiscountCents, result.PayableCents,
		promoID(result), couponID(req), result.Lines, b.Timezone)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"lines":              result.Lines,
		"item_amount_cents":  result.ItemAmountCents,
		"packaging_fee_cents": result.PackagingFeeCents,
		"discount":           result.Discount,
		"discount_cents":     result.DiscountCents,
		"payable_cents":      result.PayableCents,
		"scenario":           result.Scenario,
		"pickup_type":        pickupType(req),
		"scheduled_for":      req.ScheduledFor,
		"expires_at":         summary.ExpiresAt,
		"quote_token":        summary.Token,
	})
}

// buildPricingInput 从公开菜单构造算价输入。
func (h *Handlers) buildPricingInput(r *http.Request, storeID int64, req pricingReq) (pricing.Input, error) {
	menu, err := h.sf.GetPublicMenu(r.Context(), storeID, "")
	if err != nil {
		return pricing.Input{}, err
	}
	skuMap := map[int64]pricing.SKUInput{}
	optionItems := map[int64]pricing.OptionItemInput{}
	var optionGroups []pricing.OptionGroupInput
	for _, d := range menu.Dishes {
		for _, sk := range d.SKUs {
			skuMap[sk.ID] = pricing.SKUInput{
				ID: sk.ID, PriceCents: sk.PriceCents, PackagingFeeCents: d.PackagingFeeCents,
				InventoryMode: sk.InventoryMode,
			}
		}
		for _, g := range d.OptionGroups {
			optionGroups = append(optionGroups, pricing.OptionGroupInput{
				ID: g.ID, SelectionType: g.SelectionType, IsRequired: g.IsRequired,
				MinSelect: g.MinSelect, MaxSelect: g.MaxSelect,
			})
			for _, oi := range g.Items {
				optionItems[oi.ID] = pricing.OptionItemInput{
					ID: oi.ID, OptionGroupID: g.ID, PriceModifierCents: oi.PriceModifierCents, Enabled: true,
				}
			}
		}
	}

	cart := make([]pricing.CartItem, 0, len(req.Cart))
	for _, ci := range req.Cart {
		cart = append(cart, pricing.CartItem{SKUID: ci.SKUID, Quantity: ci.Quantity, OptionIDs: ci.OptionIDs})
	}

	in := pricing.Input{
		Scenario: req.Scenario, TableToken: req.TableToken, ScheduledFor: req.ScheduledFor,
		SKUs: skuMap, OptionItems: optionItems, OptionGroups: optionGroups, Cart: cart,
	}
	if req.CustomerCouponID != 0 && h.mkt != nil {
		// 真实顾客券校验（属主/门店/状态/模板/有效期，PRD §4.5/§4.11）。
		// 顾客需已登录；匿名用户不能使用顾客券。
		if sess, ok := customerauth.SessionFrom(r.Context()); ok {
			cc, err := h.mkt.GetCouponForPricing(r.Context(), storeID, sess.CustomerID, req.CustomerCouponID)
			if err == nil {
				in.Coupon = &pricing.CouponInput{
					CustomerCouponID: cc.CustomerCouponID,
					DiscountCents:    cc.DiscountCents,
					MinSpendCents:    cc.MinSpendCents,
				}
			}
			// err != nil 时 Coupon 保持 nil；算价引擎会在结果中附带"券不可用原因"。
		}
	}
	return in, nil
}

func hashCart(cart []cartItemReq) string {
	// 确定性哈希：SKU + 排序后的选项 + 数量。
	lines := make([]string, 0, len(cart))
	for _, c := range cart {
		opts := append([]int64(nil), c.OptionIDs...)
		sort.Slice(opts, func(i, j int) bool { return opts[i] < opts[j] })
		s := formatID(c.SKUID) + ":" + formatQty(c.Quantity)
		for _, o := range opts {
			s += "," + formatID(o)
		}
		lines = append(lines, s)
	}
	sort.Strings(lines)
	h := sha256.New()
	for _, l := range lines {
		h.Write([]byte(l))
		h.Write([]byte("|"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func promoID(result pricing.Result) int64 {
	if result.Discount != nil && result.Discount.Kind == "promotion" {
		return result.Discount.ID
	}
	return 0
}

func couponID(req pricingReq) int64 { return req.CustomerCouponID }

func pickupType(req pricingReq) string {
	if req.ScheduledFor != "" {
		return "SCHEDULED"
	}
	return "IMMEDIATE"
}

func mapPricingErr(err error) *httpx.Error {
	switch {
	case errors.Is(err, pricing.ErrInvalidQuantity), errors.Is(err, pricing.ErrInvalidSKU),
		errors.Is(err, pricing.ErrInvalidOption), errors.Is(err, pricing.ErrMissingRequiredOption),
		errors.Is(err, pricing.ErrTooManySelected), errors.Is(err, pricing.ErrInsufficientStock):
		return httpx.New(httpx.CodeQuoteMismatch, http.StatusBadRequest, err.Error())
	default:
		return httpx.New(httpx.CodeBadRequest, http.StatusBadRequest, err.Error())
	}
}

func formatID(id int64) string {
	b := make([]byte, 0, 20)
	if id == 0 {
		return "0"
	}
	digits := []byte{}
	for id > 0 {
		digits = append(digits, byte('0'+id%10))
		id /= 10
	}
	for i := len(digits) - 1; i >= 0; i-- {
		b = append(b, digits[i])
	}
	return string(b)
}

func formatQty(q int) string { return formatID(int64(q)) }
