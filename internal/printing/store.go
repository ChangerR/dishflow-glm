// Package printing 实现云打印配置、打印机与打印任务（PRD §13）。
//
// P6 提供配置/任务状态机骨架与 58mm 小票渲染。真实商鹏 API 调用由 PrintProvider 接口注入；
// mock 模式不调真实设备（PRD §13.1）。生产必须 fail closed。
package printing

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// PrintProvider 商鹏打印能力接口。
type PrintProvider interface {
	// Send 提交打印任务到商鹏，返回 provider job id。
	Send(ctx context.Context, storeID int64, printerSN, content string, copies int) (providerJobID string, err error)
	// Query 查询任务状态。
	Query(ctx context.Context, providerJobID string) (JobState, error)
	IsMock() bool
}

// JobState 打印任务状态（PRD §13.2）。
type JobState string

const (
	JobQueued    JobState = "QUEUED"
	JobSending   JobState = "SENDING"
	JobSubmitted JobState = "SUBMITTED"
	JobPrinted   JobState = "PRINTED"
	JobFailed    JobState = "FAILED"
)

// MockPrintProvider mock 实现（不调真实设备）。
type MockPrintProvider struct{}

func (MockPrintProvider) Send(_ context.Context, _ int64, _ string, _ string, _ int) (string, error) {
	return "mock_print_job", nil
}
func (MockPrintProvider) Query(_ context.Context, _ string) (JobState, error) { return JobPrinted, nil }
func (MockPrintProvider) IsMock() bool                                          { return true }

// Store 云打印持久化。
type Store struct {
	db       *sql.DB
	provider PrintProvider
}

// NewStore 创建打印存储。
func NewStore(db *sql.DB, provider PrintProvider) *Store {
	if provider == nil {
		provider = MockPrintProvider{}
	}
	return &Store{db: db, provider: provider}
}

// Printer 打印机。
type Printer struct {
	ID      int64
	StoreID int64
	SN      string
	Name    string
	IsDefault bool
	Copies  int
	Enabled bool
	Online  bool
}

// Config 商鹏配置状态。
type Config struct {
	StoreID    int64
	Status     string // draft/ready/disabled
	AutoPrint  bool
	MockPrint  bool
}

// GetConfig 取门店打印配置。
func (s *Store) GetConfig(ctx context.Context, storeID int64) (Config, error) {
	var c Config
	var status string
	var auto, mock int
	err := s.db.QueryRowContext(ctx,
		`SELECT store_id, status, auto_print, mock_print FROM cloud_print_config WHERE store_id=?`, storeID).
		Scan(&c.StoreID, &status, &auto, &mock)
	if errors.Is(err, sql.ErrNoRows) {
		return Config{StoreID: storeID, Status: "draft"}, nil
	}
	c.Status, c.AutoPrint, c.MockPrint = status, auto == 1, mock == 1
	return c, err
}

// SetConfig 写商鹏凭证（密钥加密，只写不读，PRD §13.1）+ 开关。
func (s *Store) SetConfig(ctx context.Context, storeID int64, status string, auto, mock bool, appid string, appSecretCipher, appSecretNonce []byte) error {
	ready := 0
	if appid != "" && appSecretCipher != nil && status != "disabled" {
		ready = 1
	}
	if status == "" {
		status = "draft"
		if ready == 1 {
			status = "ready"
		}
	}
	a, m := 0, 0
	if auto {
		a = 1
	}
	if mock {
		m = 1
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO cloud_print_config (store_id, status, appid, app_secret_ciphertext, app_secret_nonce, auto_print, mock_print)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE status=VALUES(status), appid=VALUES(appid), app_secret_ciphertext=VALUES(app_secret_ciphertext),
		   app_secret_nonce=VALUES(app_secret_nonce), auto_print=VALUES(auto_print), mock_print=VALUES(mock_print)`,
		storeID, status, appid, appSecretCipher, appSecretNonce, a, m)
	return err
}

// AddPrinter 绑定打印机（PRD §13.1）。
func (s *Store) AddPrinter(ctx context.Context, storeID int64, sn, name string, copies int) (int64, error) {
	if sn == "" {
		return 0, errors.New("sn required")
	}
	if copies < 1 || copies > 5 {
		copies = 1
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO cloud_printers (store_id, sn, name, copies, enabled) VALUES (?, ?, ?, ?, 1)`, storeID, sn, name, copies)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListPrinters 列出门店打印机。
func (s *Store) ListPrinters(ctx context.Context, storeID int64) ([]Printer, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, store_id, sn, name, is_default, copies, enabled, online FROM cloud_printers WHERE store_id=? ORDER BY is_default DESC, id`, storeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Printer
	for rows.Next() {
		var p Printer
		var def, enabled, online int
		if err := rows.Scan(&p.ID, &p.StoreID, &p.SN, &p.Name, &def, &p.Copies, &enabled, &online); err != nil {
			return nil, err
		}
		p.IsDefault, p.Enabled, p.Online = def == 1, enabled == 1, online == 1
		out = append(out, p)
	}
	return out, rows.Err()
}

// DefaultPrinter 取默认打印机。
func (s *Store) DefaultPrinter(ctx context.Context, storeID int64) (Printer, error) {
	var p Printer
	var def, enabled, online int
	err := s.db.QueryRowContext(ctx,
		`SELECT id, store_id, sn, name, is_default, copies, enabled, online FROM cloud_printers WHERE store_id=? AND is_default=1 AND enabled=1 LIMIT 1`, storeID).
		Scan(&p.ID, &p.StoreID, &p.SN, &p.Name, &def, &p.Copies, &enabled, &online)
	if errors.Is(err, sql.ErrNoRows) {
		return Printer{}, sql.ErrNoRows
	}
	p.IsDefault, p.Enabled, p.Online = def == 1, enabled == 1, online == 1
	return p, err
}

// ── 打印任务（PRD §13.2）──────────────────────────────────────────────

// EnqueueJob 创建打印任务（QUEUED）。
func (s *Store) EnqueueJob(ctx context.Context, storeID int64, jobType string, orderID int64, printerID int64, content string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO cloud_print_jobs (store_id, job_type, status, order_id, printer_id, content) VALUES (?, ?, 'QUEUED', ?, ?, ?)`,
		storeID, jobType, nullInt64(orderID), nullInt64(printerID), content)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// RenderOrderReceipt 渲染 58mm 小票（PRD §13.2：门店/桌号或自取号/预约时间/商品/规格/选项/数量/备注/金额/测试标识）。
// 这是一个最小可用渲染；真实排版可扩展。
func RenderOrderReceipt(storeName string, tableLabel string, pickupNo *int, scheduledFor string, items []ReceiptLine, itemAmount, packaging, discount, payable int64, mock bool) string {
	var b strings.Builder
	if mock {
		b.WriteString("== 测试打印 ==\n")
	}
	b.WriteString(storeName + "\n")
	if tableLabel != "" {
		b.WriteString("桌号: " + tableLabel + "\n")
	}
	if pickupNo != nil {
		b.WriteString("取餐号: " + zeroPad3(*pickupNo) + "\n")
	}
	if scheduledFor != "" {
		b.WriteString("预约自取: " + scheduledFor + "\n")
	}
	b.WriteString("------------------------\n")
	for _, li := range items {
		b.WriteString(li.Name + " x" + itoa(li.Quantity) + "  " + centsToYuan(li.LineAmount) + "\n")
		if li.Spec != "" {
			b.WriteString("  " + li.Spec + "\n")
		}
	}
	b.WriteString("------------------------\n")
	b.WriteString("商品: " + centsToYuan(itemAmount) + "\n")
	if packaging > 0 {
		b.WriteString("包装: " + centsToYuan(packaging) + "\n")
	}
	if discount > 0 {
		b.WriteString("优惠: -" + centsToYuan(discount) + "\n")
	}
	b.WriteString("应付: " + centsToYuan(payable) + "\n")
	return b.String()
}

// ReceiptLine 小票行。
type ReceiptLine struct {
	Name       string
	Spec       string
	Quantity   int
	LineAmount int64
}

func centsToYuan(c int64) string {
	return itoa64(c/100) + "." + itoa64(c%100) + "元"
}

func itoa(i int) string { return itoa64(int64(i)) }

func itoa64(i int64) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	digits := []byte{}
	for i > 0 {
		digits = append(digits, byte('0'+i%10))
		i /= 10
	}
	out := make([]byte, 0, len(digits)+1)
	if neg {
		out = append(out, '-')
	}
	for j := len(digits) - 1; j >= 0; j-- {
		out = append(out, digits[j])
	}
	return string(out)
}

func zeroPad3(n int) string {
	s := itoa(n)
	for len(s) < 3 {
		s = "0" + s
	}
	return s
}

func nullInt64(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

// suppress
var _ = time.Now
