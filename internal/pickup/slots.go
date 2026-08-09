// Package pickup 实现预约自取时段生成与容量规则（PRD §4.6；reservation-pickup §5）。
//
// 纯函数生成，便于单元测试。
package pickup

import (
	"errors"
	"time"
)

// Config 门店预约配置。
type Config struct {
	Enabled       bool
	BusinessHours string // "HH:mm-HH:mm"
	AdvanceDays   int    // 1..30
	SlotMinutes   int    // 5..120
	SlotCapacity  int    // 1..999
	MinLeadMinutes int   // 0..1440
	Timezone      string
}

// Slot 一个时段。
type Slot struct {
	StartsAt  time.Time `json:"starts_at"`
	Label     string    `json:"label"`
	Capacity  int       `json:"capacity"`
	Remaining int       `json:"remaining"`
	Available bool      `json:"available"`
}

// ParseBusinessHours 解析 "HH:mm-HH:mm" 为 (start, end)。
// 不支持跨午夜（PRD §4.6）。
func ParseBusinessHours(s string) (start, end time.Duration, err error) {
	// "HH:mm-HH:mm" 长度 11；s[2]=':' s[5]='-' s[8]=':'
	if len(s) != 11 || s[2] != ':' || s[5] != '-' || s[8] != ':' {
		return 0, 0, errors.New("invalid business hours format (expected HH:mm-HH:mm)")
	}
	start, err = parseHHMM(s[0:5])
	if err != nil {
		return 0, 0, err
	}
	end, err = parseHHMM(s[6:11])
	if err != nil {
		return 0, 0, err
	}
	if end <= start {
		return 0, 0, errors.New("business hours end must be after start; cross-midnight not supported")
	}
	return start, end, nil
}

func parseHHMM(s string) (time.Duration, error) {
	if len(s) != 5 || s[2] != ':' {
		return 0, errors.New("invalid HH:mm")
	}
	hh := int(s[0]-'0')*10 + int(s[1]-'0')
	mm := int(s[3]-'0')*10 + int(s[4]-'0')
	if hh > 23 || mm > 59 {
		return 0, errors.New("HH:mm out of range")
	}
	return time.Duration(hh)*time.Hour + time.Duration(mm)*time.Minute, nil
}

// GenerateSlots 生成某日期（门店本地）的时段列表（reservation-pickup §5.2）。
// now 是当前门店本地墙钟时间；reservedByStart 键为时段开始时间（门店本地）的
// "2006-01-02 15:04" 字符串，值为已占用订单数。
func GenerateSlots(cfg Config, date time.Time, now time.Time, reservedByStart map[string]int) ([]Slot, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if cfg.SlotMinutes < 5 || cfg.SlotMinutes > 120 {
		return nil, errors.New("invalid slot minutes")
	}
	if cfg.SlotCapacity < 1 {
		return nil, errors.New("invalid slot capacity")
	}
	startOffset, endOffset, err := ParseBusinessHours(cfg.BusinessHours)
	if err != nil {
		return nil, err
	}
	loc := now.Location()
	dateLocal := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, loc)
	dayStart := dateLocal.Add(startOffset)
	dayEnd := dateLocal.Add(endOffset)

	cutoff := now.Add(time.Duration(cfg.MinLeadMinutes) * time.Minute)

	var slots []Slot
	cur := dayStart
	for cur.Before(dayEnd) {
		// 开始时间必须严格晚于 now + lead。
		available := cur.After(cutoff)
		reserved := reservedByStart[cur.Format("2006-01-02 15:04")]
		remaining := cfg.SlotCapacity - reserved
		if remaining < 0 {
			remaining = 0
		}
		slots = append(slots, Slot{
			StartsAt:  cur,
			Label:     cur.Format("15:04"),
			Capacity:  cfg.SlotCapacity,
			Remaining: remaining,
			Available: available && remaining > 0,
		})
		cur = cur.Add(time.Duration(cfg.SlotMinutes) * time.Minute)
	}
	return slots, nil
}

// DateRange 生成从 today 起 advanceDays 天的日期列表（含今天，PRD §4.6）。
func DateRange(today time.Time, advanceDays int) []time.Time {
	if advanceDays < 1 {
		advanceDays = 1
	}
	loc := today.Location()
	out := make([]time.Time, 0, advanceDays)
	d0 := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, loc)
	for i := 0; i < advanceDays; i++ {
		out = append(out, d0.AddDate(0, 0, i))
	}
	return out
}

// ParseDate 解析 "YYYY-MM-DD" 为门店本地日期。
func ParseDate(s, tz string) (time.Time, error) {
	loc, err := time.LoadLocation(tz)
	if err != nil || loc == nil {
		loc = time.UTC
	}
	t, err := time.ParseInLocation("2006-01-02", s, loc)
	if err != nil {
		return time.Time{}, err
	}
	return t, nil
}
