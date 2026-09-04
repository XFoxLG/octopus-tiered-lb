package model

import "time"

// ModelPriceSchedule 峰谷计费规则：为匹配的模型提供按时间窗口的峰谷定价。
// 与 ModelPriceCategory（无精确价的兜底定价）职责不同：
//   - 价格分类：给查不到精确价格的模型定一个基准价（兜底）
//   - 峰谷计费：给匹配的模型按北京时间的窗口区分高峰价与空闲价（空闲 = 高峰 × off_peak_mul）
//
// 规则匹配语义与价格分类一致（exact/prefix/contains，忽略大小写），
// 按 sort_order 升序，首个命中的规则生效。运行时优先于任何硬编码预设。
type ModelPriceSchedule struct {
	ID       uint   `json:"id" gorm:"primaryKey;autoIncrement"`
	Name     string `json:"name" gorm:"size:191;not null;uniqueIndex"`
	RuleType string `json:"rule_type" gorm:"size:32;not null;default:'contains'"`
	RuleValue string `json:"rule_value" gorm:"size:191;not null"`
	// LLMPrice 为高峰价（USD/1M tokens），空闲价由 EffectiveLLMPrice 乘 OffPeakMul 得到。
	LLMPrice
	// OffPeakMul 空闲倍率：空闲价 = 高峰价 × OffPeakMul（官方 DeepSeek 为 0.5）。
	OffPeakMul float64 `json:"off_peak_mul" gorm:"not null;default:0.5"`
	// WeekendOffPeak 周末全天按空闲价：北京时间周六/周日不区分高峰时段，
	// 统一按 OffPeakMul 缩放（DeepSeek 官方 2026-08-23 起规则）。
	WeekendOffPeak bool `json:"weekend_off_peak" gorm:"not null;default:false"`
	// 高峰窗口（北京时间，自 00:00 起的分钟数，半开区间 [start, end)）。
	// start == end（或 start > end）视为无效窗口，跳过；两窗口都无效时该规则全天空闲。
	// 默认 09:00-12:00 与 14:00-18:00。
	Window1Start int `json:"window1_start" gorm:"not null;default:540"`
	Window1End   int `json:"window1_end" gorm:"not null;default:720"`
	Window2Start int `json:"window2_start" gorm:"not null;default:840"`
	Window2End   int `json:"window2_end" gorm:"not null;default:1080"`
	// SortOrder 越小越优先匹配。
	SortOrder int `json:"sort_order" gorm:"not null;default:0;index"`
	Enabled   bool `json:"enabled" gorm:"not null"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
