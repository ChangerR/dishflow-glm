package pricing

import (
	"errors"
	"testing"
)

func skus(sk ...SKUInput) map[int64]SKUInput {
	m := map[int64]SKUInput{}
	for _, s := range sk {
		m[s.ID] = s
	}
	return m
}

func opts(oi ...OptionItemInput) map[int64]OptionItemInput {
	m := map[int64]OptionItemInput{}
	for _, o := range oi {
		m[o.ID] = o
	}
	return m
}

func TestDineIn_NoPackagingFee(t *testing.T) {
	in := Input{
		Scenario:   ScenarioDineIn,
		TableToken: "tok",
		SKUs:       skus(SKUInput{ID: 1, PriceCents: 1000, PackagingFeeCents: 200}),
		Cart:       []CartItem{{SKUID: 1, Quantity: 2}},
	}
	res, err := Price(in)
	if err != nil {
		t.Fatal(err)
	}
	if res.ItemAmountCents != 2000 {
		t.Fatalf("item amount = %d, want 2000", res.ItemAmountCents)
	}
	if res.PackagingFeeCents != 0 {
		t.Fatalf("dine-in packaging = %d, want 0", res.PackagingFeeCents)
	}
	if res.PayableCents != 2000 {
		t.Fatalf("payable = %d, want 2000", res.PayableCents)
	}
}

func TestPickup_PackagingFee(t *testing.T) {
	in := Input{
		Scenario: ScenarioPickup,
		SKUs:     skus(SKUInput{ID: 1, PriceCents: 1000, PackagingFeeCents: 200}),
		Cart:     []CartItem{{SKUID: 1, Quantity: 3}},
	}
	res, _ := Price(in)
	if res.PackagingFeeCents != 600 {
		t.Fatalf("pickup packaging = %d, want 600", res.PackagingFeeCents)
	}
	if res.PayableCents != 3600 {
		t.Fatalf("payable = %d, want 3600", res.PayableCents)
	}
}

func TestOptionPricing_LineUnit(t *testing.T) {
	in := Input{
		Scenario: ScenarioPickup,
		SKUs:     skus(SKUInput{ID: 1, PriceCents: 1000}),
		OptionItems: opts(
			OptionItemInput{ID: 10, OptionGroupID: 100, PriceModifierCents: 300, Enabled: true},
			OptionItemInput{ID: 11, OptionGroupID: 100, PriceModifierCents: 500, Enabled: true},
		),
		OptionGroups: []OptionGroupInput{{ID: 100, SelectionType: "MULTI", MaxSelect: 2}},
		Cart:         []CartItem{{SKUID: 1, Quantity: 2, OptionIDs: []int64{11, 10}}},
	}
	res, _ := Price(in)
	// unit = 1000 + 300 + 500 = 1800；×2 = 3600
	if res.Lines[0].UnitPriceCents != 1800 {
		t.Fatalf("unit = %d, want 1800", res.Lines[0].UnitPriceCents)
	}
	if res.ItemAmountCents != 3600 {
		t.Fatalf("item amount = %d, want 3600", res.ItemAmountCents)
	}
	// 选项顺序不影响身份。
	if len(res.Lines[0].OptionIDs) != 2 {
		t.Fatal("option ids not captured")
	}
}

func TestPromotionSelection_BestThreshold(t *testing.T) {
	in := Input{
		Scenario: ScenarioPickup,
		SKUs:     skus(SKUInput{ID: 1, PriceCents: 5000}),
		Cart:     []CartItem{{SKUID: 1, Quantity: 1}},
		Promotions: []PromotionInput{
			{ID: 1, Name: "满30减5", ThresholdCents: 3000, DiscountCents: 500}, // 适用
			{ID: 2, Name: "满60减15", ThresholdCents: 6000, DiscountCents: 1500}, // 不达门槛
		},
	}
	res, _ := Price(in)
	if res.Discount == nil || res.Discount.Kind != "promotion" || res.Discount.ID != 1 {
		t.Fatalf("expected promotion 1, got %+v", res.Discount)
	}
	if res.DiscountCents != 500 {
		t.Fatalf("discount = %d, want 500", res.DiscountCents)
	}
	if res.PayableCents != 4500 {
		t.Fatalf("payable = %d, want 4500", res.PayableCents)
	}
}

func TestPromotionVsCoupon_CouponBetter(t *testing.T) {
	in := Input{
		Scenario: ScenarioPickup,
		SKUs:     skus(SKUInput{ID: 1, PriceCents: 5000}),
		Cart:     []CartItem{{SKUID: 1, Quantity: 1}},
		Promotions: []PromotionInput{{ID: 1, ThresholdCents: 3000, DiscountCents: 500}},
		Coupon:    &CouponInput{CustomerCouponID: 9, DiscountCents: 2000, MinSpendCents: 3000},
	}
	res, _ := Price(in)
	if res.Discount.Kind != "coupon" || res.Discount.ID != 9 {
		t.Fatalf("expected coupon 9, got %+v", res.Discount)
	}
	if res.DiscountCents != 2000 {
		t.Fatalf("discount = %d, want 2000", res.DiscountCents)
	}
}

func TestPromotionVsCoupon_TieGoesToPromotion(t *testing.T) {
	in := Input{
		Scenario: ScenarioPickup,
		SKUs:     skus(SKUInput{ID: 1, PriceCents: 5000}),
		Cart:     []CartItem{{SKUID: 1, Quantity: 1}},
		Promotions: []PromotionInput{{ID: 1, ThresholdCents: 3000, DiscountCents: 1000}},
		Coupon:    &CouponInput{CustomerCouponID: 9, DiscountCents: 1000, MinSpendCents: 0},
	}
	res, _ := Price(in)
	if res.Discount.Kind != "promotion" {
		t.Fatalf("tie should go to promotion, got %+v", res.Discount)
	}
}

func TestCouponBelowMinSpend_UnavailableReason(t *testing.T) {
	in := Input{
		Scenario: ScenarioPickup,
		SKUs:     skus(SKUInput{ID: 1, PriceCents: 1000}),
		Cart:     []CartItem{{SKUID: 1, Quantity: 1}},
		Coupon:   &CouponInput{CustomerCouponID: 9, DiscountCents: 500, MinSpendCents: 3000},
	}
	res, _ := Price(in)
	if res.Discount == nil || res.Discount.UnavailableReason == "" {
		t.Fatalf("expected unavailable reason for coupon, got %+v", res.Discount)
	}
	if res.DiscountCents != 0 {
		t.Fatalf("discount should be 0, got %d", res.DiscountCents)
	}
}

func TestDiscountFloor_NonNegativePayable(t *testing.T) {
	in := Input{
		Scenario: ScenarioPickup,
		SKUs:     skus(SKUInput{ID: 1, PriceCents: 500, PackagingFeeCents: 100}),
		Cart:     []CartItem{{SKUID: 1, Quantity: 1}},
		Coupon:   &CouponInput{CustomerCouponID: 9, DiscountCents: 10000, MinSpendCents: 0},
	}
	res, _ := Price(in)
	if res.PayableCents != 0 {
		t.Fatalf("payable = %d, want 0 (floor)", res.PayableCents)
	}
	if res.Discount.AmountCents != 600 {
		t.Fatalf("discount should be capped to 600 (500+100), got %d", res.Discount.AmountCents)
	}
}

func TestValidation_QuantityBounds(t *testing.T) {
	in := Input{
		Scenario: ScenarioPickup,
		SKUs:     skus(SKUInput{ID: 1, PriceCents: 1000}),
		Cart:     []CartItem{{SKUID: 1, Quantity: 0}},
	}
	if _, err := Price(in); !errors.Is(err, ErrInvalidQuantity) {
		t.Fatalf("expected ErrInvalidQuantity, got %v", err)
	}
	in.Cart[0].Quantity = 100
	if _, err := Price(in); !errors.Is(err, ErrInvalidQuantity) {
		t.Fatalf("expected ErrInvalidQuantity for 100, got %v", err)
	}
}

func TestValidation_DailyStock(t *testing.T) {
	in := Input{
		Scenario: ScenarioPickup,
		SKUs:     skus(SKUInput{ID: 1, PriceCents: 1000, InventoryMode: "DAILY", AvailableQty: 2}),
		Cart:     []CartItem{{SKUID: 1, Quantity: 3}},
	}
	if _, err := Price(in); !errors.Is(err, ErrInsufficientStock) {
		t.Fatalf("expected ErrInsufficientStock, got %v", err)
	}
}

func TestValidation_InvalidOption(t *testing.T) {
	in := Input{
		Scenario: ScenarioPickup,
		SKUs:     skus(SKUInput{ID: 1, PriceCents: 1000}),
		OptionItems: opts(OptionItemInput{ID: 10, OptionGroupID: 100, PriceModifierCents: 0, Enabled: false}),
		Cart:       []CartItem{{SKUID: 1, Quantity: 1, OptionIDs: []int64{10}}},
	}
	if _, err := Price(in); !errors.Is(err, ErrInvalidOption) {
		t.Fatalf("expected ErrInvalidOption, got %v", err)
	}
}

func TestValidation_MissingRequiredOption(t *testing.T) {
	in := Input{
		Scenario: ScenarioPickup,
		SKUs:     skus(SKUInput{ID: 1, PriceCents: 1000}),
		OptionItems: opts(OptionItemInput{ID: 10, OptionGroupID: 100, PriceModifierCents: 0, Enabled: true}),
		OptionGroups: []OptionGroupInput{{ID: 100, SelectionType: "SINGLE", IsRequired: true, MinSelect: 1, MaxSelect: 1}},
		Cart:         []CartItem{{SKUID: 1, Quantity: 1}}, // 未选必选项
	}
	if _, err := Price(in); !errors.Is(err, ErrMissingRequiredOption) {
		t.Fatalf("expected ErrMissingRequiredOption, got %v", err)
	}
}

func TestValidation_DineInRequiresTable(t *testing.T) {
	in := Input{
		Scenario: ScenarioDineIn,
		SKUs:     skus(SKUInput{ID: 1, PriceCents: 1000}),
		Cart:     []CartItem{{SKUID: 1, Quantity: 1}},
	}
	if _, err := Price(in); err == nil {
		t.Fatal("dine-in without table token should fail")
	}
}

func TestValidation_DineInNoSchedule(t *testing.T) {
	in := Input{
		Scenario:     ScenarioDineIn,
		TableToken:   "tok",
		ScheduledFor: "2026-08-01T10:00:00Z",
		SKUs:         skus(SKUInput{ID: 1, PriceCents: 1000}),
		Cart:         []CartItem{{SKUID: 1, Quantity: 1}},
	}
	if _, err := Price(in); err == nil {
		t.Fatal("dine-in with scheduled time should fail")
	}
}

func TestZeroPayableOrder(t *testing.T) {
	in := Input{
		Scenario: ScenarioPickup,
		SKUs:     skus(SKUInput{ID: 1, PriceCents: 500}),
		Cart:     []CartItem{{SKUID: 1, Quantity: 1}},
		Coupon:   &CouponInput{CustomerCouponID: 9, DiscountCents: 500, MinSpendCents: 0},
	}
	res, _ := Price(in)
	if res.PayableCents != 0 {
		t.Fatalf("payable = %d, want 0", res.PayableCents)
	}
	// 0 元订单仍应产出有效报价（P5 不调微信收款）。
	if res.Discount == nil {
		t.Fatal("discount detail missing")
	}
}

func TestUnlimitedStockNotJudgedSoldOut(t *testing.T) {
	// 无限库存不应因 AvailableQty 缺失被判售罄（PRD §7.3）。
	in := Input{
		Scenario: ScenarioPickup,
		SKUs:     skus(SKUInput{ID: 1, PriceCents: 1000, InventoryMode: "UNLIMITED"}), // AvailableQty 未设=0
		Cart:     []CartItem{{SKUID: 1, Quantity: 5}},
	}
	if _, err := Price(in); err != nil {
		t.Fatalf("unlimited stock should not be sold out, got %v", err)
	}
}
