// Package orders 实现订单创建事务、状态机、工作台与查询（PRD §4.7/§6）。
//
// 订单状态机（PRD §6.2）：
//   主链路：PENDING_PAYMENT → PAID → ACCEPTED → PREPARING → READY → COMPLETED
//   分支：
//     PENDING_PAYMENT → CANCELLED
//     PAID → REFUNDING → REFUNDED
//     ACCEPTED → CANCEL_REQUESTED → ACCEPTED（驳回）/ REFUNDING（通过）
//     ACCEPTED/PREPARING/READY/COMPLETED → REFUNDING → REFUNDED（门店主动退款）
package orders

import "errors"

// State 履约状态。
type State string

const (
	StatePendingPayment State = "PENDING_PAYMENT"
	StatePaid           State = "PAID"
	StateAccepted       State = "ACCEPTED"
	StatePreparing      State = "PREPARING"
	StateReady          State = "READY"
	StateCompleted      State = "COMPLETED"
	StateCancelled      State = "CANCELLED"
	StateRefunding      State = "REFUNDING"
	StateRefunded       State = "REFUNDED"
	StateCancelRequested State = "CANCEL_REQUESTED"
)

// Transition 表示一条合法状态迁移。
type Transition struct {
	From State
	To   State
	// 角色约束：customer（顾客可触发）/ staff（店员以上）/ system（系统/Worker/回调）
	Actor string
}

// 合法迁移表（PRD §6.2）。禁止跳级/回退/以客户端状态覆盖服务端状态。
var transitions = map[State]map[State]string{
	StatePaid:      {StateAccepted: "staff", StateCancelled: "system", StateRefunding: "system"}, // PAID 自动退款/系统关单
	StateAccepted:  {StatePreparing: "staff", StateCancelRequested: "customer", StateRefunding: "staff"},
	StateCancelRequested: {StateAccepted: "staff", StateRefunding: "staff"},
	StatePreparing: {StateReady: "staff", StateRefunding: "staff"},
	StateReady:     {StateCompleted: "staff", StateRefunding: "staff"},
	StateCompleted: {StateRefunding: "staff"},
}

// ErrInvalidTransition 非法状态迁移。
var ErrInvalidTransition = errors.New("invalid state transition")

// CanTransition 判断 (from → to) 是否合法且 actor 允许。
// actor: "customer"/"staff"/"system"。
func CanTransition(from, to State, actor string) bool {
	// PENDING_PAYMENT → CANCELLED：顾客/系统均可。
	if from == StatePendingPayment && to == StateCancelled {
		return actor == "customer" || actor == "system"
	}
	allowed, ok := transitions[from][to]
	if !ok {
		return false
	}
	if allowed == "staff" {
		return actor == "staff" || actor == "system"
	}
	return true
}
