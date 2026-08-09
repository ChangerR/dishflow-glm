package pickup

import (
	"testing"
	"time"
)

func loc() *time.Location {
	l, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.UTC
	}
	return l
}

func TestParseBusinessHours(t *testing.T) {
	s, e, err := ParseBusinessHours("09:00-22:00")
	if err != nil {
		t.Fatal(err)
	}
	if s != 9*time.Hour || e != 22*time.Hour {
		t.Fatalf("got %v %v", s, e)
	}
	// 结束不晚于开始应失败。
	if _, _, err := ParseBusinessHours("22:00-09:00"); err == nil {
		t.Fatal("cross-midnight should fail")
	}
	if _, _, err := ParseBusinessHours("invalid"); err == nil {
		t.Fatal("invalid format should fail")
	}
}

func TestGenerateSlots_Basic(t *testing.T) {
	cfg := Config{Enabled: true, BusinessHours: "09:00-10:00", AdvanceDays: 7, SlotMinutes: 15, SlotCapacity: 5, MinLeadMinutes: 0, Timezone: "Asia/Shanghai"}
	today := time.Date(2026, 8, 1, 0, 0, 0, 0, loc())
	now := time.Date(2026, 8, 1, 0, 0, 0, 0, loc()) // 凌晨，全部时段可选
	slots, err := GenerateSlots(cfg, today, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	// 09:00-10:00 15min 间隔 = 4 个时段。
	if len(slots) != 4 {
		t.Fatalf("got %d slots, want 4", len(slots))
	}
	for _, s := range slots {
		if !s.Available {
			t.Fatalf("slot %s should be available", s.Label)
		}
		if s.Remaining != 5 {
			t.Fatalf("remaining = %d, want 5", s.Remaining)
		}
	}
	if slots[0].Label != "09:00" {
		t.Fatalf("first label = %q", slots[0].Label)
	}
}

func TestGenerateSlots_LeadTimeCutoff(t *testing.T) {
	cfg := Config{Enabled: true, BusinessHours: "09:00-10:00", SlotMinutes: 15, SlotCapacity: 5, MinLeadMinutes: 30, Timezone: "Asia/Shanghai"}
	today := time.Date(2026, 8, 1, 0, 0, 0, 0, loc())
	// now = 09:05，lead 30min → 仅 09:35 之后的可选。
	now := time.Date(2026, 8, 1, 9, 5, 0, 0, loc())
	slots, err := GenerateSlots(cfg, today, now, nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(slots) < 4 {
		t.Fatalf("expected >=4 slots, got %d: %+v", len(slots), slots)
	}
	// 时段：09:00/09:15/09:30/09:45；cutoff=09:35；严格晚于 → 09:45 可选。
	last := slots[len(slots)-1]
	if last.Label != "09:45" || !last.Available {
		t.Fatalf("09:45 should be available, got %+v", last)
	}
	if slots[0].Available {
		t.Fatal("09:00 should be past cutoff")
	}
}

func TestGenerateSlots_FullSlotNotAvailable(t *testing.T) {
	cfg := Config{Enabled: true, BusinessHours: "09:00-09:30", SlotMinutes: 15, SlotCapacity: 1, MinLeadMinutes: 0, Timezone: "Asia/Shanghai"}
	today := time.Date(2026, 8, 1, 0, 0, 0, 0, loc())
	now := time.Date(2026, 8, 1, 0, 0, 0, 0, loc())
	reserved := map[string]int{"2026-08-01 09:00": 1}
	slots, err := GenerateSlots(cfg, today, now, reserved)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(slots) == 0 {
		t.Fatal("expected slots")
	}
	if slots[0].Remaining != 0 {
		t.Fatalf("remaining = %d, want 0", slots[0].Remaining)
	}
	if slots[0].Available {
		t.Fatal("full slot should not be available")
	}
}

func TestGenerateSlots_Disabled(t *testing.T) {
	cfg := Config{Enabled: false, BusinessHours: "09:00-10:00", SlotMinutes: 15, SlotCapacity: 5}
	slots, err := GenerateSlots(cfg, time.Now(), time.Now(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if slots != nil {
		t.Fatal("disabled pickup should return no slots")
	}
}

func TestDateRange(t *testing.T) {
	today := time.Date(2026, 8, 1, 12, 0, 0, 0, loc())
	days := DateRange(today, 3)
	if len(days) != 3 {
		t.Fatalf("got %d days", len(days))
	}
	want := []string{"2026-08-01", "2026-08-02", "2026-08-03"}
	for i, d := range days {
		if d.Format("2006-01-02") != want[i] {
			t.Fatalf("day %d = %s, want %s", i, d.Format("2006-01-02"), want[i])
		}
	}
}
