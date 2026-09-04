package llm

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
)

// priceScheduleCache 缓存启用且按 sort_order 排序的峰谷计费规则，供
// price.BillingWindow / EffectiveLLMPrice 热点路径匹配使用，避免每次计费
// 都访问 DB。与 priceCategoryCache 同构，由 Create/Update/Delete/Seed 维护。
// atomic.Pointer：RefreshPriceScheduleCache 与热路径 PriceScheduleMatch 并发
// 读写，普通赋值存在数据竞争（slice 头非原子）。
var priceScheduleCache atomic.Pointer[[]model.ModelPriceSchedule]

// ListPriceSchedules 返回全部峰谷规则（含禁用），按 sort_order 升序。
func ListPriceSchedules(ctx context.Context) ([]model.ModelPriceSchedule, error) {
	rows := []model.ModelPriceSchedule{}
	if err := db.GetDB().WithContext(ctx).Order("sort_order ASC, id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// listEnabledPriceSchedules 返回启用的规则快照。快照切片替换后只读，可直接共享。
func listEnabledPriceSchedules() []model.ModelPriceSchedule {
	if c := priceScheduleCache.Load(); c != nil {
		return *c
	}
	return nil
}

// RefreshPriceScheduleCache 从 DB 重载启用规则进内存缓存。
func RefreshPriceScheduleCache(ctx context.Context) error {
	rows := []model.ModelPriceSchedule{}
	if err := db.GetDB().WithContext(ctx).
		Where("enabled = ?", true).
		Order("sort_order ASC, id ASC").
		Find(&rows).Error; err != nil {
		return err
	}
	priceScheduleCache.Store(&rows)
	return nil
}

func getPriceScheduleByName(name string) (model.ModelPriceSchedule, error) {
	var row model.ModelPriceSchedule
	err := db.GetDB().Where("name = ?", strings.ToLower(strings.TrimSpace(name))).First(&row).Error
	return row, err
}

// SeedPriceSchedules 在表为空时插入默认峰谷规则（DeepSeek 官方美元价，
// 见 https://api-docs.deepseek.com/quick_start/pricing 峰谷定价），
// 保证升级后计费行为平滑过渡；之后完全由前端管理（可改可删）。表非空则跳过。
func SeedPriceSchedules(ctx context.Context) error {
	var count int64
	if err := db.GetDB().WithContext(ctx).Model(&model.ModelPriceSchedule{}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	// 官方美元价（USD/1M tokens，PEAK）：flash $0.44/$1.32/$0.014，
	// pro $1.32/$3.96/$0.044；官方 OFF-PEAK 恰为 PEAK × 0.5。
	// 窗口默认 09:00-12:00 / 14:00-18:00（北京时间）；2026-08-23 起
	// 周末（周六/周日）全天不再区分峰谷，统一按空闲价。
	seed := []model.ModelPriceSchedule{
		{
			Name:           "deepseek-v4-flash",
			RuleType:       string(model.ModelPriceCategoryRulePrefix),
			RuleValue:      "deepseek-v4-flash",
			LLMPrice:       model.LLMPrice{Input: 0.44, Output: 1.32, CacheRead: 0.014},
			OffPeakMul:     0.5,
			WeekendOffPeak: true,
			Window1Start:   540, Window1End: 720,
			Window2Start: 840, Window2End: 1080,
			SortOrder: 1,
			Enabled:   true,
		},
		{
			Name:           "deepseek-v4-pro",
			RuleType:       string(model.ModelPriceCategoryRulePrefix),
			RuleValue:      "deepseek-v4-pro",
			LLMPrice:       model.LLMPrice{Input: 1.32, Output: 3.96, CacheRead: 0.044},
			OffPeakMul:     0.5,
			WeekendOffPeak: true,
			Window1Start:   540, Window1End: 720,
			Window2Start: 840, Window2End: 1080,
			SortOrder: 2,
			Enabled:   true,
		},
	}
	for i := range seed {
		if err := db.GetDB().WithContext(ctx).Create(&seed[i]).Error; err != nil {
			return err
		}
	}
	return RefreshPriceScheduleCache(ctx)
}

// CreatePriceSchedule 创建峰谷规则，name 转小写，rule_type 校验。
func CreatePriceSchedule(s model.ModelPriceSchedule, ctx context.Context) (model.ModelPriceSchedule, error) {
	s.Name = strings.ToLower(strings.TrimSpace(s.Name))
	if s.Name == "" {
		return s, fmt.Errorf("name is required")
	}
	if err := validatePriceSchedule(s); err != nil {
		return s, err
	}
	if err := db.GetDB().WithContext(ctx).Create(&s).Error; err != nil {
		return s, err
	}
	if err := RefreshPriceScheduleCache(ctx); err != nil {
		return s, err
	}
	return s, nil
}

// UpdatePriceSchedule 更新峰谷规则。不存在的 ID 返回 error。
func UpdatePriceSchedule(s model.ModelPriceSchedule, ctx context.Context) (model.ModelPriceSchedule, error) {
	var existing model.ModelPriceSchedule
	if err := db.GetDB().WithContext(ctx).First(&existing, s.ID).Error; err != nil {
		return s, err
	}
	s.Name = strings.ToLower(strings.TrimSpace(s.Name))
	if s.Name == "" {
		return s, fmt.Errorf("name is required")
	}
	if err := validatePriceSchedule(s); err != nil {
		return s, err
	}
	if err := db.GetDB().WithContext(ctx).Save(&s).Error; err != nil {
		return s, err
	}
	if err := RefreshPriceScheduleCache(ctx); err != nil {
		return s, err
	}
	return s, nil
}

// DeletePriceSchedule 删除峰谷规则。
func DeletePriceSchedule(id uint, ctx context.Context) error {
	if err := db.GetDB().WithContext(ctx).Delete(&model.ModelPriceSchedule{}, id).Error; err != nil {
		return err
	}
	return RefreshPriceScheduleCache(ctx)
}

func validatePriceSchedule(s model.ModelPriceSchedule) error {
	switch model.ModelPriceCategoryRule(s.RuleType) {
	case model.ModelPriceCategoryRuleExact,
		model.ModelPriceCategoryRulePrefix,
		model.ModelPriceCategoryRuleContains:
	default:
		return fmt.Errorf("invalid rule_type: %s", s.RuleType)
	}
	if strings.TrimSpace(s.RuleValue) == "" {
		return fmt.Errorf("rule_value is required")
	}
	if s.OffPeakMul < 0 {
		return fmt.Errorf("off_peak_mul must be >= 0")
	}
	for _, w := range [][2]int{{s.Window1Start, s.Window1End}, {s.Window2Start, s.Window2End}} {
		if w[0] < 0 || w[1] > 24*60 || w[0] > w[1] {
			return fmt.Errorf("window out of range (0-1440, start<=end): [%d,%d)", w[0], w[1])
		}
	}
	return nil
}

// scheduleMatches 判断模型名是否命中规则（与价格分类同语义，忽略大小写）。
func scheduleMatches(s model.ModelPriceSchedule, modelName string) bool {
	rule := model.ModelPriceCategoryRule(s.RuleType)
	v := strings.ToLower(strings.TrimSpace(s.RuleValue))
	if v == "" {
		return false
	}
	switch rule {
	case model.ModelPriceCategoryRuleExact:
		return modelName == v
	case model.ModelPriceCategoryRulePrefix:
		return strings.HasPrefix(modelName, v)
	default:
		return strings.Contains(modelName, v)
	}
}

// PriceScheduleMatch 在启用规则快照中按 sort_order 找第一个命中的规则，
// 命中则返回其指针（快照只读），否则返回 nil。供 price.BillingWindow /
// EffectiveLLMPrice 及模型列表峰谷标注调用。
func PriceScheduleMatch(modelName string) *model.ModelPriceSchedule {
	rows := listEnabledPriceSchedules()
	for i := range rows {
		if scheduleMatches(rows[i], modelName) {
			return &rows[i]
		}
	}
	return nil
}
