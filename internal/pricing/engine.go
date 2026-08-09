// Package pricing 实现服务端算价引擎（PRD §4.4/§4.5）。
//
// 这是系统的权威算价逻辑，试算与生产订单必须调用同一领域逻辑（PRD §7.4）。
// 设计为纯函数 + 输入结构，便于单元测试与未来扩展叠加/范围。
//
// 规则（PRD §4.5）：
//   - 所有金额 int64 分。
//   - 行单价 = SKU 基础价 + 所选有效选项加价。
//   - 商品金额 = Σ(行单价 × 数量)。
//   - 自取包装费 = 每份菜品包装费 × 数量；堂食为 0。
//   - 满减：在门槛与有效期内最优（金额最大）。
//   - 优惠券：属于顾客+门店、状态可用、模板启用、满足有效期与最低消费。
//   - 生产基线：比较“最优满减折扣”与“指定券折扣”，取金额更大者，不叠加；相等取满减。
//   - 折扣不得使应付 < 0。
//   - 0 元订单仍需服务端创建并确认支付成功，但不调微信收款（P5 处理）。
package pricing

import (
	"errors"
)

// Scenario 用餐场景（PRD §4.5）。
type Scenario string

const (
	ScenarioDineIn Scenario = "DINE_IN"
	ScenarioPickup Scenario = "PICKUP"
)

// ── 输入 ──────────────────────────────────────────────────────────────

// SKUInput 算价所需 SKU 信息。
type SKUInput struct {
	ID            int64
	PriceCents    int64
	PackagingFeeCents int64 // 所属菜品的每份包装费
	InventoryMode string
	DailyStock    int
	// 当前业务日可用量（target - reserved - sold），无限库存填 -1。
	AvailableQty int
}

// OptionItemInput 选项项信息。
type OptionItemInput struct {
	ID                 int64
	OptionGroupID      int64
	PriceModifierCents int64
	Enabled            bool
}

// OptionGroupInput 选项组信息（用于校验必选/选择范围）。
type OptionGroupInput struct {
	ID            int64
	SelectionType string // SINGLE | MULTI
	IsRequired    bool
	MinSelect     int
	MaxSelect     int
}

// CartItem 购物车项（来自客户端，仅含 SKU ID/选项 ID/数量，PRD §4.3）。
type CartItem struct {
	SKUID     int64
	Quantity  int
	OptionIDs []int64
}

// PromotionInput 满减候选。
type PromotionInput struct {
	ID             int64
	Name           string
	ThresholdCents int64
	DiscountCents  int64
}

// CouponInput 顾客券候选（已校验属主/门店/状态）。
type CouponInput struct {
	CustomerCouponID int64
	DiscountCents    int64
	MinSpendCents    int64
}

// Input 算价输入。
type Input struct {
	Scenario      Scenario
	TableToken    string   // 堂食附桌码 token
	ScheduledFor  string   // 自取预约时附服务端签发时间（RFC3339）
	SKUs          map[int64]SKUInput
	OptionItems   map[int64]OptionItemInput
	OptionGroups  []OptionGroupInput
	Cart          []CartItem
	Promotions    []PromotionInput // 有效满减
	Coupon        *CouponInput     // 指定券（可选）
}

// ── 输出 ──────────────────────────────────────────────────────────────

// LineItem 报价商品行。
type LineItem struct {
	SKUID          int64   `json:"sku_id"`
	Quantity       int     `json:"quantity"`
	UnitPriceCents int64   `json:"unit_price_cents"`
	OptionIDs      []int64 `json:"option_ids"`
	LineAmountCents int64  `json:"line_amount_cents"`
}

// DiscountDetail 折扣明细。
type DiscountDetail struct {
	Kind          string // promotion | coupon
	ID            int64
	Name          string
	AmountCents   int64
	// 券不可用原因（仅在指定券未被采用时填充）
	UnavailableReason string `json:"unavailable_reason,omitempty"`
}

// Result 报价结果。
type Result struct {
	Lines            []LineItem     `json:"lines"`
	ItemAmountCents  int64          `json:"item_amount_cents"`
	PackagingFeeCents int64         `json:"packaging_fee_cents"`
	Discount         *DiscountDetail `json:"discount,omitempty"`
	DiscountCents    int64          `json:"discount_cents"`
	PayableCents     int64          `json:"payable_cents"`
	Scenario         Scenario       `json:"scenario"`
}

// Validate 校验购物车一致性（SKU/选项存在、启用、必选满足、数量 1..99，PRD §4.3/§4.4）。
func (in Input) Validate() error {
	if in.Scenario != ScenarioDineIn && in.Scenario != ScenarioPickup {
		return errors.New("invalid scenario")
	}
	if in.Scenario == ScenarioDineIn && in.TableToken == "" {
		return errors.New("dine-in requires table token")
	}
	if in.Scenario == ScenarioDineIn && in.ScheduledFor != "" {
		return errors.New("dine-in cannot carry scheduled time")
	}
	if len(in.Cart) == 0 {
		return errors.New("empty cart")
	}
	for _, ci := range in.Cart {
		if ci.Quantity < 1 || ci.Quantity > 99 {
			return ErrInvalidQuantity
		}
		sk, ok := in.SKUs[ci.SKUID]
		if !ok {
			return ErrInvalidSKU
		}
		if sk.InventoryMode == "DAILY" && sk.AvailableQty >= 0 && ci.Quantity > sk.AvailableQty {
			return ErrInsufficientStock
		}
		// 选项存在且启用。
		for _, oid := range ci.OptionIDs {
			oi, ok := in.OptionItems[oid]
			if !ok || !oi.Enabled {
				return ErrInvalidOption
			}
		}
	}
	// 选项组必选/范围校验（按 SKU 聚合每个组的已选数）。
	return in.validateOptionGroups()
}

func (in Input) validateOptionGroups() error {
	// 统计每个选项组被选中的“不同选项项数”（不含数量，PRD §4.3 选择数）。
	groupSelectedDistinct := map[int64]map[int64]bool{}
	for _, ci := range in.Cart {
		for _, oid := range ci.OptionIDs {
			oi, ok := in.OptionItems[oid]
			if !ok {
				continue
			}
			if groupSelectedDistinct[oi.OptionGroupID] == nil {
				groupSelectedDistinct[oi.OptionGroupID] = map[int64]bool{}
			}
			groupSelectedDistinct[oi.OptionGroupID][oid] = true
		}
	}
	for _, g := range in.OptionGroups {
		m := groupSelectedDistinct[g.ID]
		sel := len(m)
		if g.IsRequired && sel < 1 {
			return ErrMissingRequiredOption
		}
		if g.SelectionType == "SINGLE" && sel > 1 {
			return ErrTooManySelected
		}
		if g.SelectionType == "MULTI" && sel > g.MaxSelect {
			return ErrTooManySelected
		}
	}
	return nil
}

// 领域错误。
var (
	ErrInvalidSKU           = errors.New("invalid or missing sku")
	ErrInvalidOption        = errors.New("invalid or disabled option")
	ErrInvalidQuantity      = errors.New("quantity must be 1..99")
	ErrInsufficientStock    = errors.New("insufficient stock")
	ErrMissingRequiredOption = errors.New("missing required option")
	ErrTooManySelected      = errors.New("too many options selected")
)
