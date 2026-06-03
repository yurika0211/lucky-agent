package cron

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseNaturalLanguage 将中文自然语言转换为 Schedule
// 支持格式:
//   - "每天9点" / "每天9:30" → DailySchedule
//   - "每小时" / "每2小时" → IntervalSchedule
//   - "每30分钟" / "每5分钟" → IntervalSchedule
//   - "每周一9点" / "每周五17:30" → CronSchedule
//   - "工作日9点" → CronSchedule (周一到周五)
//   - "明天10点" → OnceSchedule
//   - "2026-06-01 12:00" → OnceSchedule
func ParseNaturalLanguage(input string) (Schedule, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil, fmt.Errorf("empty input")
	}

	// 每天 HH:MM / 每天H点
	if strings.HasPrefix(input, "每天") {
		return parseDaily(input[6:])
	}

	// 每小时 / 每N小时
	if strings.HasPrefix(input, "每") && strings.Contains(input, "小时") {
		return parseIntervalHour(input)
	}

	// 每N分钟
	if strings.HasPrefix(input, "每") && strings.Contains(input, "分钟") {
		return parseIntervalMinute(input)
	}

	// 每周X HH:MM
	if strings.HasPrefix(input, "每周") {
		return parseWeekly(input[6:])
	}

	// 工作日 HH:MM
	if strings.HasPrefix(input, "工作日") {
		return parseWeekday(input[9:])
	}

	// 明天 HH:MM
	if strings.HasPrefix(input, "明天") {
		return parseTomorrow(input[6:])
	}

	// 日期时间格式: 2026-06-01 12:00
	if isDateTimeFormat(input) {
		return parseDateTime(input)
	}

	return nil, fmt.Errorf("无法识别的调度格式: %s\n支持: 每天H点, 每N小时, 每N分钟, 每周X H点, 工作日H点, 明天H点, YYYY-MM-DD HH:MM", input)
}

func parseDaily(rest string) (Schedule, error) {
	hour, minute, err := parseTime(rest)
	if err != nil {
		return nil, fmt.Errorf("每天: %w", err)
	}
	return DailySchedule{Hour: hour, Minute: minute}, nil
}

func parseIntervalHour(input string) (Schedule, error) {
	// "每小时" → 1h, "每2小时" → 2h
	n := 1
	if idx := strings.Index(input, "每"); idx >= 0 {
		afterMei := input[idx+len("每"):]
		if len(afterMei) > 0 && !strings.HasPrefix(afterMei, "小") {
			afterMei = normalizeChineseScheduleNumerals(afterMei)
			num := strings.TrimSuffix(afterMei, "小时")
			num = strings.TrimSuffix(num, "个")
			num = strings.TrimSpace(num)
			parsed, err := strconv.Atoi(num)
			if err == nil && parsed > 0 {
				n = parsed
			}
		}
	}
	return IntervalSchedule{Interval: time.Duration(n) * time.Hour}, nil
}

func normalizeChineseScheduleNumerals(input string) string {
	replacer := strings.NewReplacer(
		"零", "0",
		"〇", "0",
		"一", "1",
		"二", "2",
		"两", "2",
		"三", "3",
		"四", "4",
		"五", "5",
		"六", "6",
		"七", "7",
		"八", "8",
		"九", "9",
		"十", "10",
	)
	return replacer.Replace(input)
}

func parseIntervalMinute(input string) (Schedule, error) {
	// "每30分钟" → 30m
	n := 1
	if idx := strings.Index(input, "每"); idx >= 0 {
		afterMei := input[idx+len("每"):]
		if len(afterMei) > 0 && !strings.HasPrefix(afterMei, "分") {
			afterMei = normalizeChineseScheduleNumerals(afterMei)
			num := strings.TrimSuffix(afterMei, "分钟")
			num = strings.TrimSuffix(num, "个")
			num = strings.TrimSpace(num)
			parsed, err := strconv.Atoi(num)
			if err == nil && parsed > 0 {
				n = parsed
			}
		}
	}
	return IntervalSchedule{Interval: time.Duration(n) * time.Minute}, nil
}

func parseWeekly(rest string) (Schedule, error) {
	// "一9点" → Monday 9:00
	weekdayMap := map[string]int{
		"一": 1, "二": 2, "三": 3, "四": 4, "五": 5, "六": 6, "日": 0, "天": 0,
	}

	var weekday int
	var timeStr string
	found := false

	for prefix, day := range weekdayMap {
		if strings.HasPrefix(rest, prefix) {
			weekday = day
			timeStr = rest[len(prefix):]
			found = true
			break
		}
	}

	if !found {
		return nil, fmt.Errorf("无法识别星期: %s", rest)
	}

	hour, minute, err := parseTime(timeStr)
	if err != nil {
		return nil, fmt.Errorf("每周: %w", err)
	}

	return CronSchedule{
		Minute:  []int{minute},
		Hour:    []int{hour},
		Day:     []int{},
		Month:   []int{},
		Weekday: []int{weekday},
	}, nil
}

func parseWeekday(rest string) (Schedule, error) {
	// 工作日 = 周一到周五
	hour, minute, err := parseTime(rest)
	if err != nil {
		return nil, fmt.Errorf("工作日: %w", err)
	}

	return CronSchedule{
		Minute:  []int{minute},
		Hour:    []int{hour},
		Day:     []int{},
		Month:   []int{},
		Weekday: []int{1, 2, 3, 4, 5},
	}, nil
}

func parseTomorrow(rest string) (Schedule, error) {
	hour, minute, err := parseTime(rest)
	if err != nil {
		return nil, fmt.Errorf("明天: %w", err)
	}

	now := time.Now()
	tomorrow := now.AddDate(0, 0, 1)
	at := time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), hour, minute, 0, 0, now.Location())

	return OnceSchedule{At: at}, nil
}

func parseDateTime(input string) (Schedule, error) {
	t, err := time.ParseInLocation("2006-01-02 15:04", input, time.Local)
	if err != nil {
		return nil, fmt.Errorf("日期格式错误: %w", err)
	}
	return OnceSchedule{At: t}, nil
}

func isDateTimeFormat(input string) bool {
	parts := strings.SplitN(input, " ", 2)
	if len(parts) != 2 {
		return false
	}
	_, err := time.Parse("2006-01-02", parts[0])
	return err == nil
}

// parseTime 解析中文时间表达
// 支持: "9点", "9:30", "17点30分", "17:30"
func parseTime(input string) (hour, minute int, err error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return 0, 0, nil
	}

	// "9:30" 格式
	if strings.Contains(input, ":") {
		parts := strings.SplitN(input, ":", 2)
		hour, err1 := strconv.Atoi(parts[0])
		minute, err2 := strconv.Atoi(parts[1])
		if err1 != nil || err2 != nil {
			return 0, 0, fmt.Errorf("invalid time: %s", input)
		}
		return hour, minute, validateTime(hour, minute)
	}

	// "9点30分" / "9点" / "9点半"
	if strings.Contains(input, "点") {
		parts := strings.SplitN(input, "点", 2)
		hour, err = strconv.Atoi(parts[0])
		if err != nil {
			return 0, 0, fmt.Errorf("invalid hour: %s", parts[0])
		}

		minuteStr := parts[1]
		if minuteStr == "" || minuteStr == "整" {
			minute = 0
		} else if minuteStr == "半" {
			minute = 30
		} else {
			minuteStr = strings.TrimSuffix(minuteStr, "分")
			minute, err = strconv.Atoi(minuteStr)
			if err != nil {
				return 0, 0, fmt.Errorf("invalid minute: %s", parts[1])
			}
		}
		return hour, minute, validateTime(hour, minute)
	}

	// 纯数字 → 小时
	hour, err = strconv.Atoi(input)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid time: %s", input)
	}
	return hour, 0, validateTime(hour, 0)
}

func validateTime(hour, minute int) error {
	if hour < 0 || hour > 23 {
		return fmt.Errorf("hour %d out of range [0, 23]", hour)
	}
	if minute < 0 || minute > 59 {
		return fmt.Errorf("minute %d out of range [0, 59]", minute)
	}
	return nil
}
