package pricing

// Price 执行算价（PRD §4.5）。
//
// 步骤：
//  1. 校验输入（Validate）。
//  2. 计算每行单价 = SKU 基础价 + 选项加价；商品金额 = Σ 行单价×数量。
//  3. 包装费（仅自取）= Σ 菜品每份包装费 × 数量；堂食 0。
//  4. 选择最优满减（门槛 ≤ 商品金额、金额最大）。
//  5. 评估指定券（满足最低消费）。
//  6. 比较“满减折扣”与“券折扣”，取金额更大；相等取满减；不叠加。
//  7. 折扣不使应付 < 0。
//
// 返回 Result 与可能的券不可用原因（在 Result.Discount.UnavailableReason）。
func Price(in Input) (Result, error) {
	if err := in.Validate(); err != nil {
		return Result{}, err
	}

	res := Result{Scenario: in.Scenario}
	lines := make([]LineItem, 0, len(in.Cart))
	var itemAmount, packaging int64

	for _, ci := range in.Cart {
		sk := in.SKUs[ci.SKUID]
		unit := sk.PriceCents
		optIDs := append([]int64(nil), ci.OptionIDs...)
		sortInt64(optIDs)
		for _, oid := range optIDs {
			unit += in.OptionItems[oid].PriceModifierCents
		}
		lineAmt := unit * int64(ci.Quantity)
		lines = append(lines, LineItem{
			SKUID: ci.SKUID, Quantity: ci.Quantity, UnitPriceCents: unit,
			OptionIDs: optIDs, LineAmountCents: lineAmt,
		})
		itemAmount += lineAmt
		// 自取包装费 = 每份菜品包装费 × 数量（PRD §4.5）。
		if in.Scenario == ScenarioPickup {
			packaging += sk.PackagingFeeCents * int64(ci.Quantity)
		}
	}
	res.Lines = lines
	res.ItemAmountCents = itemAmount
	res.PackagingFeeCents = packaging

	// 满减前基数：PRD §4.5 满减比较的是“商品金额”。（包装费是否计入门槛以满减金额最大化为目标，
	// 生产基线按商品金额计算门槛。）
	base := itemAmount

	// 4. 最优满减（金额最大；金额相同取更早/更优）。
	var bestPromo *PromotionInput
	for i := range in.Promotions {
		p := in.Promotions[i]
		if base >= p.ThresholdCents && p.DiscountCents > 0 {
			if bestPromo == nil || p.DiscountCents > bestPromo.DiscountCents {
				bestPromo = &in.Promotions[i]
			}
		}
	}
	promoDiscount := int64(0)
	if bestPromo != nil {
		promoDiscount = bestPromo.DiscountCents
	}

	// 5. 指定券评估。最低消费以商品金额为准。
	var couponDiscount int64
	couponReason := ""
	if in.Coupon != nil {
		if base < in.Coupon.MinSpendCents {
			couponReason = "below minimum spend"
		} else {
			couponDiscount = in.Coupon.DiscountCents
		}
	}

	// 6. 比较：取金额更大；相等取满减（PRD §4.5）。
	chosen := &DiscountDetail{}
	switch {
	case promoDiscount >= couponDiscount && promoDiscount > 0:
		chosen.Kind = "promotion"
		chosen.ID = bestPromo.ID
		chosen.Name = bestPromo.Name
		chosen.AmountCents = promoDiscount
	case couponDiscount > 0:
		chosen.Kind = "coupon"
		chosen.ID = in.Coupon.CustomerCouponID
		chosen.Name = "" // 名称由调用方填充
		chosen.AmountCents = couponDiscount
	default:
		chosen = nil
	}
	// 若指定券未被采用且存在，附带不可用原因。
	if in.Coupon != nil && (chosen == nil || chosen.Kind != "coupon") {
		if couponReason == "" {
			couponReason = "promotion is better"
		}
	}

	discountCents := int64(0)
	if chosen != nil {
		discountCents = chosen.AmountCents
		res.Discount = chosen
	}
	// 折扣不得超过 (商品金额 + 包装费)；应付不得 < 0。
	prePayable := itemAmount + packaging - discountCents
	if prePayable < 0 {
		discountCents = itemAmount + packaging
		prePayable = 0
		if res.Discount != nil {
			res.Discount.AmountCents = discountCents
		}
	}
	res.DiscountCents = discountCents
	res.PayableCents = prePayable

	// 把券不可用原因附到结果（前端展示，PRD §4.4.5）。
	if in.Coupon != nil && couponReason != "" {
		if res.Discount == nil {
			res.Discount = &DiscountDetail{}
		}
		if res.Discount.Kind != "coupon" {
			res.Discount.UnavailableReason = couponReason
		}
	}

	return res, nil
}

func sortInt64(s []int64) {
	// 选项顺序不影响购物车行身份（PRD §4.3），这里仅排序以保证确定性快照。
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
