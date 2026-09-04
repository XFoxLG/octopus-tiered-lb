package group

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dlclark/regexp2"
	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/channel"
	"github.com/lingyuins/octopus/internal/op/setting"
	"github.com/lingyuins/octopus/internal/utils/cache"
	"github.com/lingyuins/octopus/internal/utils/log"
	"github.com/lingyuins/octopus/internal/utils/xstrings"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var groupCache = cache.New[int, model.Group](16)
var groupMap = cache.New[string, model.Group](16)

// GetCache returns the internal group cache (for backward compatibility).
func GetCache() cache.Cache[int, model.Group] { return groupCache }

// GetNameMap returns the internal group name map (for backward compatibility).
func GetNameMap() cache.Cache[string, model.Group] { return groupMap }

// GetRegexMatchers returns the internal regex matchers map (for backward compatibility).
func GetRegexMatchers() map[string][]compiledGroupMatcher { return groupRegexMatchersByEndpoint }

const groupCacheKeySep = "\x00"

var groupRegexMatchersByEndpoint = make(map[string][]compiledGroupMatcher)
var groupRegexMatchersLock sync.RWMutex

// GetRegexMatchersLock returns the regex matchers mutex (for backward compatibility).
func GetRegexMatchersLock() *sync.RWMutex { return &groupRegexMatchersLock }

const groupRegexMatchTimeout = 250 * time.Millisecond

// GetRegexMatchTimeout returns the regex match timeout (for backward compatibility).
func GetRegexMatchTimeout() time.Duration { return groupRegexMatchTimeout }

// CompiledGroupMatcher is an exported alias for compiledGroupMatcher (for backward compatibility).
type CompiledGroupMatcher = compiledGroupMatcher

type compiledGroupMatcher struct {
	group model.Group
	Re    *regexp2.Regexp
}

func makeGroupItemDedupKey(channelID int, modelName string) string {
	return fmt.Sprintf("%d%s%s", channelID, groupCacheKeySep, strings.TrimSpace(modelName))
}

func NormalizeItems(items []model.GroupItem) []model.GroupItem {
	if len(items) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(items))
	normalized := make([]model.GroupItem, 0, len(items))
	nextPriority := 1

	for _, item := range items {
		item.ModelName = strings.TrimSpace(item.ModelName)
		if item.ChannelID <= 0 || item.ModelName == "" {
			continue
		}

		key := makeGroupItemDedupKey(item.ChannelID, item.ModelName)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		item.Priority = nextPriority
		nextPriority++
		if item.Weight <= 0 {
			item.Weight = 1
		}

		normalized = append(normalized, item)
	}

	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func normalizeGroupItemAddRequests(existingItems []model.GroupItem, itemsToUpdate []model.GroupItemUpdateRequest, itemsToDelete []int, itemsToAdd []model.GroupItemAddRequest) []model.GroupItemAddRequest {
	if len(itemsToAdd) == 0 {
		return nil
	}

	deletedItemIDs := make(map[int]struct{}, len(itemsToDelete))
	for _, id := range itemsToDelete {
		if id > 0 {
			deletedItemIDs[id] = struct{}{}
		}
	}

	updatedPriorities := make(map[int]int, len(itemsToUpdate))
	for _, item := range itemsToUpdate {
		if item.ID > 0 && item.Priority > 0 {
			updatedPriorities[item.ID] = item.Priority
		}
	}

	seen := make(map[string]struct{}, len(existingItems)+len(itemsToAdd))
	maxPriority := 0
	for _, item := range existingItems {
		if _, deleted := deletedItemIDs[item.ID]; deleted {
			continue
		}

		item.ModelName = strings.TrimSpace(item.ModelName)
		if item.ChannelID <= 0 || item.ModelName == "" {
			continue
		}

		if updatedPriority, ok := updatedPriorities[item.ID]; ok {
			item.Priority = updatedPriority
		}
		if item.Priority > maxPriority {
			maxPriority = item.Priority
		}

		seen[makeGroupItemDedupKey(item.ChannelID, item.ModelName)] = struct{}{}
	}

	normalized := make([]model.GroupItemAddRequest, 0, len(itemsToAdd))
	nextPriority := maxPriority + 1
	for _, item := range itemsToAdd {
		item.ModelName = strings.TrimSpace(item.ModelName)
		if item.ChannelID <= 0 || item.ModelName == "" {
			continue
		}

		key := makeGroupItemDedupKey(item.ChannelID, item.ModelName)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		if item.Priority <= 0 {
			item.Priority = nextPriority
		}
		if item.Priority >= nextPriority {
			nextPriority = item.Priority + 1
		}
		if item.Weight <= 0 {
			item.Weight = 1
		}

		normalized = append(normalized, item)
	}

	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func normalizeGroupItemUpdateRequests(existingItems []model.GroupItem, itemsToUpdate []model.GroupItemUpdateRequest) []model.GroupItemUpdateRequest {
	if len(itemsToUpdate) == 0 {
		return nil
	}

	existingByID := make(map[int]model.GroupItem, len(existingItems))
	for _, item := range existingItems {
		if item.ID > 0 {
			existingByID[item.ID] = item
		}
	}

	seen := make(map[int]struct{}, len(itemsToUpdate))
	normalized := make([]model.GroupItemUpdateRequest, 0, len(itemsToUpdate))
	for _, item := range itemsToUpdate {
		if item.ID <= 0 {
			continue
		}
		if _, ok := seen[item.ID]; ok {
			continue
		}
		existing, ok := existingByID[item.ID]
		if !ok {
			continue
		}
		seen[item.ID] = struct{}{}

		if item.Priority <= 0 {
			item.Priority = existing.Priority
		}
		if item.Weight <= 0 {
			item.Weight = existing.Weight
			if item.Weight <= 0 {
				item.Weight = 1
			}
		}

		normalized = append(normalized, item)
	}

	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func makeGroupCacheKey(endpointType, name string) string {
	return model.NormalizeEndpointType(endpointType) + groupCacheKeySep + strings.TrimSpace(name)
}

func looksLikeDeepSeekConversationModel(requestModel string) bool {
	normalized := strings.ToLower(strings.TrimSpace(requestModel))
	return normalized != "" && strings.Contains(normalized, model.EndpointTypeDeepSeek)
}

func looksLikeMimoConversationModel(requestModel string) bool {
	normalized := strings.ToLower(strings.TrimSpace(requestModel))
	return normalized != "" && strings.Contains(normalized, model.EndpointTypeMimo)
}

func conversationEndpointLookupOrder(endpointType string, requestModel string) []string {
	var order []string
	switch model.NormalizeEndpointType(endpointType) {
	case model.EndpointTypeChat:
		order = []string{
			model.EndpointTypeChat,
			model.EndpointTypeDeepSeek,
			model.EndpointTypeMimo,
			model.EndpointTypeResponses,
			model.EndpointTypeMessages,
		}
	case model.EndpointTypeMimo:
		order = []string{
			model.EndpointTypeMimo,
			model.EndpointTypeChat,
			model.EndpointTypeDeepSeek,
			model.EndpointTypeResponses,
			model.EndpointTypeMessages,
		}
	case model.EndpointTypeResponses:
		order = []string{
			model.EndpointTypeResponses,
			model.EndpointTypeChat,
			model.EndpointTypeDeepSeek,
			model.EndpointTypeMimo,
			model.EndpointTypeMessages,
		}
	case model.EndpointTypeMessages:
		order = []string{
			model.EndpointTypeMessages,
			model.EndpointTypeChat,
			model.EndpointTypeDeepSeek,
			model.EndpointTypeMimo,
			model.EndpointTypeResponses,
		}
	case model.EndpointTypeDeepSeek:
		order = []string{
			model.EndpointTypeDeepSeek,
			model.EndpointTypeChat,
			model.EndpointTypeMimo,
			model.EndpointTypeResponses,
			model.EndpointTypeMessages,
		}
	default:
		return nil
	}

	if looksLikeDeepSeekConversationModel(requestModel) && model.NormalizeEndpointType(endpointType) != model.EndpointTypeDeepSeek {
		return append([]string{model.EndpointTypeDeepSeek}, order...)
	}
	if looksLikeMimoConversationModel(requestModel) && model.NormalizeEndpointType(endpointType) != model.EndpointTypeMimo {
		return append([]string{model.EndpointTypeMimo}, order...)
	}

	return order
}

func normalizeGroup(group model.Group) model.Group {
	group.Name = strings.TrimSpace(group.Name)
	group.Category = strings.TrimSpace(group.Category)
	group.EndpointType = model.NormalizeEndpointType(group.EndpointType)
	group.EndpointProvider = strings.ToLower(strings.TrimSpace(group.EndpointProvider))
	group.OutboundFormat = strings.ToLower(strings.TrimSpace(group.OutboundFormat))
	group.MatchRegex = strings.TrimSpace(group.MatchRegex)
	for i := range group.Items {
		group.Items[i].ModelName = strings.TrimSpace(group.Items[i].ModelName)
		if group.Items[i].Weight <= 0 {
			group.Items[i].Weight = 1
		}
	}
	sort.SliceStable(group.Items, func(i, j int) bool {
		leftPriority := group.Items[i].Priority
		if leftPriority <= 0 {
			leftPriority = int(^uint(0) >> 1)
		}
		rightPriority := group.Items[j].Priority
		if rightPriority <= 0 {
			rightPriority = int(^uint(0) >> 1)
		}
		if leftPriority != rightPriority {
			return leftPriority < rightPriority
		}
		if group.Items[i].ChannelID != group.Items[j].ChannelID {
			return group.Items[i].ChannelID < group.Items[j].ChannelID
		}
		if group.Items[i].ModelName != group.Items[j].ModelName {
			return group.Items[i].ModelName < group.Items[j].ModelName
		}
		return group.Items[i].ID < group.Items[j].ID
	})
	return group
}

func finalizeMatchedGroup(group model.Group) model.Group {
	group = normalizeGroup(group)
	if len(group.Items) == 0 {
		group.Items = nil
		return group
	}

	enabledItems := make([]model.GroupItem, 0, len(group.Items))
	for _, item := range group.Items {
		ch, ok := channel.GetCache().Get(item.ChannelID)
		if !ok || !ch.Enabled {
			continue
		}
		enabledItems = append(enabledItems, item)
	}
	group.Items = enabledItems
	return group
}

func findGroupByEndpoint(endpointType, name string) (model.Group, bool) {
	group, ok := groupMap.Get(makeGroupCacheKey(endpointType, name))
	if ok {
		return group, true
	}

	groupRegexMatchersLock.RLock()
	matchers := groupRegexMatchersByEndpoint[model.NormalizeEndpointType(endpointType)]
	for _, matcher := range matchers {
		isMatched, err := matcher.Re.MatchString(name)
		if err != nil || !isMatched {
			continue
		}
		groupRegexMatchersLock.RUnlock()
		return matcher.group, true
	}
	groupRegexMatchersLock.RUnlock()
	return model.Group{}, false
}

func RebuildIndexes() {
	groups := groupCache.GetAll()
	groupMap.Clear()

	sortedGroups := make([]model.Group, 0, len(groups))
	for _, group := range groups {
		sortedGroups = append(sortedGroups, group)
	}
	sort.SliceStable(sortedGroups, func(i, j int) bool {
		if sortedGroups[i].ID != sortedGroups[j].ID {
			return sortedGroups[i].ID < sortedGroups[j].ID
		}
		if sortedGroups[i].Name != sortedGroups[j].Name {
			return sortedGroups[i].Name < sortedGroups[j].Name
		}
		return sortedGroups[i].EndpointType < sortedGroups[j].EndpointType
	})

	matchersByEndpoint := make(map[string][]compiledGroupMatcher)
	for _, group := range sortedGroups {
		group = normalizeGroup(group)
		groupMap.Set(makeGroupCacheKey(group.EndpointType, group.Name), group)
		regex := strings.TrimSpace(group.MatchRegex)
		if regex == "" {
			continue
		}
		re, err := regexp2.Compile(regex, regexp2.ECMAScript)
		if err != nil {
			continue
		}
		re.MatchTimeout = groupRegexMatchTimeout
		endpointType := model.NormalizeEndpointType(group.EndpointType)
		matchersByEndpoint[endpointType] = append(matchersByEndpoint[endpointType], compiledGroupMatcher{group: group, Re: re})
	}

	groupRegexMatchersLock.Lock()
	groupRegexMatchersByEndpoint = matchersByEndpoint
	groupRegexMatchersLock.Unlock()
}

func GroupList(ctx context.Context) ([]model.Group, error) {
	groups := make([]model.Group, 0, groupCache.Len())
	for _, group := range groupCache.GetAll() {
		groups = append(groups, normalizeGroup(group))
	}
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].ID != groups[j].ID {
			return groups[i].ID < groups[j].ID
		}
		if groups[i].Name != groups[j].Name {
			return groups[i].Name < groups[j].Name
		}
		return groups[i].EndpointType < groups[j].EndpointType
	})
	return groups, nil
}

func GroupListModel(ctx context.Context) ([]string, error) {
	models := []string{}
	seen := make(map[string]struct{}, groupCache.Len())
	for _, group := range groupCache.GetAll() {
		name := strings.TrimSpace(group.Name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}

		// Only include groups that have at least one valid item
		// whose channel exists and is enabled.
		if !groupHasValidItem(group) {
			continue
		}

		seen[name] = struct{}{}
		models = append(models, name)
	}
	sort.Strings(models)
	return models, nil
}

// groupHasValidItem checks whether a group has at least one item whose
// referenced channel exists and is enabled.
func groupHasValidItem(group model.Group) bool {
	if len(group.Items) == 0 {
		return false
	}
	for _, item := range group.Items {
		if item.ChannelID <= 0 || item.ModelName == "" {
			continue
		}
		ch, ok := channel.GetCache().Get(item.ChannelID)
		if !ok || !ch.Enabled {
			continue
		}
		return true
	}
	return false
}

// groupMatchesRequestedEndpoint reports whether a group should be visible
// when filtering models by the given requested endpoint type.
//
// Rules (aligned with GroupGetEnabledMapByEndpoint semantics):
//   - requestedEndpoint == "*" or "" → always visible
//   - group.EndpointType == "*" → always visible
//   - group.EndpointType == requestedEndpoint → visible
//   - Both are in the conversation family (chat/deepseek/mimo/responses/messages) → visible
//   - Otherwise → not visible
func groupMatchesRequestedEndpoint(group model.Group, requestedEndpoint string) bool {
	requestedEndpoint = model.NormalizeEndpointType(requestedEndpoint)
	if requestedEndpoint == model.EndpointTypeAll {
		return true
	}

	groupEndpoint := model.NormalizeEndpointType(group.EndpointType)
	if groupEndpoint == model.EndpointTypeAll || groupEndpoint == requestedEndpoint {
		return true
	}

	if model.IsConversationEndpointType(requestedEndpoint) && model.IsConversationEndpointType(groupEndpoint) {
		return true
	}

	return false
}

// GroupListModelByEndpoint returns distinct model names for groups that:
//  1. Have at least one valid item (channel exists + enabled)
//  2. Match the requested endpoint type (or all if endpointType is empty/"*")
//
// This is the endpoint-aware variant of GroupListModel.
func GroupListModelByEndpoint(endpointType string, ctx context.Context) ([]string, error) {
	// 查询侧：空串表示"不限类型"（返回所有 endpoint 的 group），与 group 存储侧
	// 默认值（NormalizeEndpointType 空串→chat）区分。
	if strings.TrimSpace(endpointType) == "" {
		endpointType = model.EndpointTypeAll
	}
	endpointType = model.NormalizeEndpointType(endpointType)

	models := []string{}
	seen := make(map[string]struct{}, groupCache.Len())
	for _, group := range groupCache.GetAll() {
		name := strings.TrimSpace(group.Name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}

		if !groupHasValidItem(group) {
			continue
		}

		if !groupMatchesRequestedEndpoint(group, endpointType) {
			continue
		}

		seen[name] = struct{}{}
		models = append(models, name)
	}
	sort.Strings(models)
	return models, nil
}

// GroupListModelCapabilities returns an aggregated view of all routable
// models with their supported endpoint types, conversation-family flag,
// and availability status.
//
// Models with the same name but different endpoint types are merged into
// a single entry with deduplicated, sorted endpoints.
func GroupListModelCapabilities(ctx context.Context) ([]model.ModelCapability, error) {
	// Collect valid groups, grouped by model name
	byName := make(map[string][]string, groupCache.Len())
	for _, group := range groupCache.GetAll() {
		name := strings.TrimSpace(group.Name)
		if name == "" {
			continue
		}
		if !groupHasValidItem(group) {
			continue
		}
		ep := model.NormalizeEndpointType(group.EndpointType)
		byName[name] = append(byName[name], ep)
	}

	caps := make([]model.ModelCapability, 0, len(byName))
	for name, endpoints := range byName {
		// Deduplicate and sort
		seen := make(map[string]struct{}, len(endpoints))
		uniq := make([]string, 0, len(endpoints))
		for _, ep := range endpoints {
			if _, ok := seen[ep]; ok {
				continue
			}
			seen[ep] = struct{}{}
			uniq = append(uniq, ep)
		}
		sort.Strings(uniq)

		// Determine conversation flag
		conversation := false
		for _, ep := range uniq {
			if model.IsConversationEndpointType(ep) {
				conversation = true
				break
			}
		}

		caps = append(caps, model.ModelCapability{
			Name:         name,
			Endpoints:    uniq,
			Conversation: conversation,
			Available:    true,
		})
	}

	sort.SliceStable(caps, func(i, j int) bool {
		return caps[i].Name < caps[j].Name
	})
	return caps, nil
}

func GroupGet(id int, ctx context.Context) (*model.Group, error) {
	group, ok := groupCache.Get(id)
	if !ok {
		return nil, fmt.Errorf("group not found")
	}
	group = normalizeGroup(group)
	return &group, nil
}

func GroupGetEnabledMapByEndpoint(endpointType string, name string, ctx context.Context) (model.Group, error) {
	endpointType = model.NormalizeEndpointType(endpointType)

	lookupOrder := conversationEndpointLookupOrder(endpointType, name)
	if len(lookupOrder) == 0 {
		lookupOrder = []string{endpointType}
	}

	for _, candidateEndpointType := range lookupOrder {
		if group, ok := findGroupByEndpoint(candidateEndpointType, name); ok {
			return finalizeMatchedGroup(group), nil
		}
	}
	if endpointType != model.EndpointTypeAll {
		if group, ok := findGroupByEndpoint(model.EndpointTypeAll, name); ok {
			return finalizeMatchedGroup(group), nil
		}
	}
	return model.Group{}, fmt.Errorf("group not found")
}

func GroupGetEnabledMap(name string, ctx context.Context) (model.Group, error) {
	return GroupGetEnabledMapByEndpoint(model.EndpointTypeAll, name, ctx)
}

func GroupCreate(group *model.Group, ctx context.Context) error {
	group.Name = strings.TrimSpace(group.Name)
	if group.Name == "" {
		return fmt.Errorf("group name is required")
	}
	if group.StreamIdleTimeout < 0 {
		return fmt.Errorf("stream idle timeout must be non-negative")
	}
	if group.ParamOverride != nil {
		if err := validateGroupParamOverride(*group.ParamOverride); err != nil {
			return err
		}
	}
	group.Category = strings.TrimSpace(group.Category)
	group.EndpointType = model.NormalizeEndpointType(group.EndpointType)
	group.EndpointProvider = strings.ToLower(strings.TrimSpace(group.EndpointProvider))
	group.OutboundFormat = strings.ToLower(strings.TrimSpace(group.OutboundFormat))
	group.MatchRegex = strings.TrimSpace(group.MatchRegex)
	group.Items = NormalizeItems(group.Items)
	if err := db.GetDB().WithContext(ctx).Create(group).Error; err != nil {
		return err
	}
	groupCache.Set(group.ID, normalizeGroup(*group))
	RebuildIndexes()
	return nil
}

// validateGroupParamOverride 校验分组级 param_override 格式：必须是 JSON object。
// 空白视为未配置，直接通过；语义校验（白名单字段）由 relay 层按现有渠道逻辑处理。
func validateGroupParamOverride(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return fmt.Errorf("param_override must be a valid JSON object: %w", err)
	}
	return nil
}

// ApplyToolsProbeResult 按证据层级把 tools 探测结论回填到 group_items（Seller 移植）。
//   - executed：强 true 证据，覆盖所有非 true 行；
//   - accepted / required_unsupported：弱 true 证据，只写当前为 NULL 的行（不覆盖任何 false）；
//   - unsupported：≥2 确认 false，覆盖所有非 false 行；
//   - pending / required_ignored / unknown：不判定，不写列。
func ApplyToolsProbeResult(channelID int, modelName string, result model.ToolsProbeResult, keyID *int, probedAt int64) error {
	if channelID <= 0 || strings.TrimSpace(modelName) == "" {
		return nil
	}
	base := db.GetDB().Model(&model.GroupItem{}).Where("channel_id = ? AND model_name = ?", channelID, modelName)
	updates := map[string]any{
		"supports_tools_probe_key_id": keyID,
		"supports_tools_probed_at":    probedAt,
		"supports_tools_source":       result.Source,
	}
	switch result.State {
	case model.ToolsProbeStateExecuted:
		updates["supports_tools"] = true
		return base.Where("supports_tools IS NULL OR supports_tools = ?", false).Updates(updates).Error
	case model.ToolsProbeStateAccepted, model.ToolsProbeStateRequiredUnsupported:
		updates["supports_tools"] = true
		return base.Where("supports_tools IS NULL").Updates(updates).Error
	case model.ToolsProbeStateUnsupported:
		updates["supports_tools"] = false
		return base.Where("supports_tools IS NULL OR supports_tools = ?", true).Updates(updates).Error
	default:
		return nil
	}
}

func GroupUpdate(req *model.GroupUpdateRequest, ctx context.Context) (*model.Group, error) {
	group, ok := groupCache.Get(req.ID)
	if !ok {
		return nil, fmt.Errorf("group not found")
	}

	normalizedItemsToUpdate := normalizeGroupItemUpdateRequests(group.Items, req.ItemsToUpdate)
	normalizedItemsToAdd := normalizeGroupItemAddRequests(group.Items, normalizedItemsToUpdate, req.ItemsToDelete, req.ItemsToAdd)

	tx := db.GetDB().WithContext(ctx).Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			log.Errorf("panic recovered in transaction: %v", r)
		}
	}()

	var selectFields []string
	updates := model.Group{ID: req.ID}

	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			return nil, fmt.Errorf("group name is required")
		}
		selectFields = append(selectFields, "name")
		updates.Name = name
	}
	if req.Category != nil {
		selectFields = append(selectFields, "category")
		updates.Category = strings.TrimSpace(*req.Category)
	}
	if req.EndpointType != nil {
		selectFields = append(selectFields, "endpoint_type")
		updates.EndpointType = model.NormalizeEndpointType(*req.EndpointType)
	}
	if req.EndpointProvider != nil {
		selectFields = append(selectFields, "endpoint_provider")
		updates.EndpointProvider = strings.ToLower(strings.TrimSpace(*req.EndpointProvider))
	}
	if req.OutboundFormat != nil {
		selectFields = append(selectFields, "outbound_format")
		updates.OutboundFormat = strings.ToLower(strings.TrimSpace(*req.OutboundFormat))
	}
	if req.Mode != nil {
		selectFields = append(selectFields, "mode")
		updates.Mode = *req.Mode
	}
	if req.MatchRegex != nil {
		selectFields = append(selectFields, "match_regex")
		updates.MatchRegex = strings.TrimSpace(*req.MatchRegex)
	}
	if req.FirstTokenTimeOut != nil {
		selectFields = append(selectFields, "first_token_time_out")
		updates.FirstTokenTimeOut = *req.FirstTokenTimeOut
	}
	if req.AttemptTimeOut != nil {
		selectFields = append(selectFields, "attempt_time_out")
		updates.AttemptTimeOut = *req.AttemptTimeOut
	}
	if req.StreamIdleTimeout != nil {
		if *req.StreamIdleTimeout < 0 {
			return nil, fmt.Errorf("stream idle timeout must be non-negative")
		}
		selectFields = append(selectFields, "stream_idle_timeout")
		updates.StreamIdleTimeout = *req.StreamIdleTimeout
	}
	if req.SessionKeepTime != nil {
		selectFields = append(selectFields, "session_keep_time")
		updates.SessionKeepTime = *req.SessionKeepTime
	}
	if req.SortStrategy != nil {
		selectFields = append(selectFields, "sort_strategy")
		strategy := strings.ToLower(strings.TrimSpace(*req.SortStrategy))
		switch strategy {
		case "", "non_relay_balance", "non_relay_multiplier", "multiplier_balance", "balance_only":
		default:
			return nil, fmt.Errorf("invalid sort strategy: must be empty, non_relay_balance, non_relay_multiplier, multiplier_balance or balance_only")
		}
		updates.SortStrategy = strategy
	}
	if req.Condition != nil {
		selectFields = append(selectFields, "condition")
		updates.Condition = strings.TrimSpace(*req.Condition)
	}
	if req.ReasoningBufferStrategy != nil {
		selectFields = append(selectFields, "reasoning_buffer_strategy")
		strategy := strings.TrimSpace(*req.ReasoningBufferStrategy)
		if strategy != "" && strategy != "buffer" && strategy != "immediate" {
			return nil, fmt.Errorf("invalid reasoning buffer strategy: must be empty, 'buffer', or 'immediate'")
		}
		updates.ReasoningBufferStrategy = strategy
	}
	if req.ParamOverride != nil {
		if err := validateGroupParamOverride(*req.ParamOverride); err != nil {
			return nil, err
		}
		selectFields = append(selectFields, "param_override")
		updates.ParamOverride = req.ParamOverride
	}
	if req.CustomHeader != nil {
		selectFields = append(selectFields, "custom_header")
		updates.CustomHeader = *req.CustomHeader
	}

	if len(selectFields) > 0 {
		if err := tx.Model(&model.Group{}).Where("id = ?", req.ID).Select(selectFields).Updates(&updates).Error; err != nil {
			tx.Rollback()
			return nil, fmt.Errorf("failed to update group: %w", err)
		}
	}

	// 删除 items
	if len(req.ItemsToDelete) > 0 {
		if err := tx.Where("id IN ? AND group_id = ?", req.ItemsToDelete, req.ID).Delete(&model.GroupItem{}).Error; err != nil {
			tx.Rollback()
			return nil, fmt.Errorf("failed to delete items: %w", err)
		}
	}

	// 批量更新 items
	if len(normalizedItemsToUpdate) > 0 {
		ids := make([]int, len(normalizedItemsToUpdate))
		priorityCase := "CASE id"
		weightCase := "CASE id"
		for i, item := range normalizedItemsToUpdate {
			ids[i] = item.ID
			priorityCase += fmt.Sprintf(" WHEN %d THEN %d", item.ID, item.Priority)
			weightCase += fmt.Sprintf(" WHEN %d THEN %d", item.ID, item.Weight)
		}
		priorityCase += " END"
		weightCase += " END"

		if err := tx.Model(&model.GroupItem{}).
			Where("id IN ? AND group_id = ?", ids, req.ID).
			Updates(map[string]interface{}{
				"priority": gorm.Expr(priorityCase),
				"weight":   gorm.Expr(weightCase),
			}).Error; err != nil {
			tx.Rollback()
			return nil, fmt.Errorf("failed to update items: %w", err)
		}
	}

	// 批量新增 items
	if len(normalizedItemsToAdd) > 0 {
		newItems := make([]model.GroupItem, len(normalizedItemsToAdd))
		for i, item := range normalizedItemsToAdd {
			newItems[i] = model.GroupItem{
				GroupID:   req.ID,
				ChannelID: item.ChannelID,
				ModelName: item.ModelName,
				Priority:  item.Priority,
				Weight:    item.Weight,
			}
		}
		if err := tx.Create(&newItems).Error; err != nil {
			tx.Rollback()
			return nil, fmt.Errorf("failed to create items: %w", err)
		}
	}

	if err := tx.Commit().Error; err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	// 刷新缓存并返回最新数据
	if err := RefreshCacheByID(req.ID, ctx); err != nil {
		return nil, err
	}

	updatedGroup, _ := groupCache.Get(req.ID)
	return &updatedGroup, nil
}

func GroupDel(id int, ctx context.Context) error {
	_, ok := groupCache.Get(id)
	if !ok {
		return fmt.Errorf("group not found")
	}

	tx := db.GetDB().WithContext(ctx).Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			log.Errorf("panic recovered in transaction: %v", r)
		}
	}()

	if err := tx.Where("group_id = ?", id).Delete(&model.GroupItem{}).Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to delete group items: %w", err)
	}

	if err := tx.Delete(&model.Group{}, id).Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to delete group: %w", err)
	}

	if err := tx.Commit().Error; err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	groupCache.Del(id)
	RebuildIndexes()
	return nil
}

// UpdateGroupTestStatus 回写分组最近一次测试的成败状态（issue #113/#119）。
// passed=true 表示全部通过，false 表示存在失败。allFailed 区分全部失败与部分失败，
// 仅当 passed=false 时有意义（passed=true 时自动置 false）。由 group_probe 测试
// 完成时调用，前端据此决定是否对整张卡片灰色化。回写后刷新缓存使列表响应立即
// 携带新状态。
func UpdateGroupTestStatus(id int, passed bool, allFailed bool, ctx context.Context) error {
	if _, ok := groupCache.Get(id); !ok {
		return fmt.Errorf("group not found")
	}
	now := time.Now().Unix()
	updates := map[string]interface{}{
		"last_test_passed":     passed,
		"last_test_at":         now,
		"last_test_all_failed": false,
	}
	if !passed {
		updates["last_test_all_failed"] = allFailed
	}
	if err := db.GetDB().WithContext(ctx).
		Model(&model.Group{}).
		Where("id = ?", id).
		Updates(updates).Error; err != nil {
		return fmt.Errorf("failed to update group test status: %w", err)
	}
	return RefreshCacheByID(id, ctx)
}

func GroupDelAll(ctx context.Context) (int64, error) {
	tx := db.GetDB().WithContext(ctx).Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			log.Errorf("panic recovered in transaction: %v", r)
		}
	}()

	var deletedCount int64
	if err := tx.Model(&model.Group{}).Count(&deletedCount).Error; err != nil {
		tx.Rollback()
		return 0, fmt.Errorf("failed to count groups: %w", err)
	}

	if err := tx.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&model.GroupItem{}).Error; err != nil {
		tx.Rollback()
		return 0, fmt.Errorf("failed to delete group items: %w", err)
	}

	if err := tx.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&model.Group{}).Error; err != nil {
		tx.Rollback()
		return 0, fmt.Errorf("failed to delete groups: %w", err)
	}

	if err := tx.Model(&model.Setting{}).
		Where("key = ?", model.SettingKeyAIRouteGroupID).
		Update("value", "0").Error; err != nil {
		tx.Rollback()
		return 0, fmt.Errorf("failed to reset ai route group setting: %w", err)
	}
	// 确保 ai_route_group_id 设置行存在（可能被外部删除），缺失时补建。
	var settingCount int64
	if err := tx.Model(&model.Setting{}).Where("key = ?", model.SettingKeyAIRouteGroupID).Count(&settingCount).Error; err != nil {
		tx.Rollback()
		return 0, fmt.Errorf("failed to check ai route group setting: %w", err)
	}
	if settingCount == 0 {
		if err := tx.Create(&model.Setting{Key: model.SettingKeyAIRouteGroupID, Value: "0"}).Error; err != nil {
			tx.Rollback()
			return 0, fmt.Errorf("failed to recreate ai route group setting: %w", err)
		}
	}

	if err := tx.Commit().Error; err != nil {
		return 0, fmt.Errorf("failed to commit transaction: %w", err)
	}

	groupCache.Clear()
	RebuildIndexes()
	if err := setting.SetString(model.SettingKeyAIRouteGroupID, "0"); err != nil {
		// 缓存同步失败不应阻塞删除结果（DB 已提交），记录日志便于排查。
		log.Errorf("refresh ai route group setting cache after GroupDelAll: %v", err)
	}

	return deletedCount, nil
}

func GroupItemAdd(item *model.GroupItem, ctx context.Context) error {
	if _, ok := groupCache.Get(item.GroupID); !ok {
		return fmt.Errorf("group not found")
	}

	if err := db.GetDB().WithContext(ctx).Create(item).Error; err != nil {
		return err
	}

	return RefreshCacheByID(item.GroupID, ctx)
}

func GroupItemBatchAdd(groupID int, items []model.GroupIDAndLLMName, ctx context.Context) error {
	if len(items) == 0 {
		return nil
	}

	group, ok := groupCache.Get(groupID)
	if !ok {
		return fmt.Errorf("group not found")
	}

	seen := make(map[string]struct{}, len(items))
	uniq := make([]model.GroupIDAndLLMName, 0, len(items))
	for _, it := range items {
		if it.ChannelID == 0 || it.ModelName == "" {
			continue
		}
		k := fmt.Sprintf("%d|%s", it.ChannelID, it.ModelName)
		if _, exists := seen[k]; exists {
			continue
		}
		seen[k] = struct{}{}
		uniq = append(uniq, it)
	}
	if len(uniq) == 0 {
		return nil
	}

	nextPriority := 1
	for _, gi := range group.Items {
		if gi.Priority >= nextPriority {
			nextPriority = gi.Priority + 1
		}
	}

	newItems := make([]model.GroupItem, 0, len(uniq))
	for _, it := range uniq {
		newItems = append(newItems, model.GroupItem{
			GroupID:   groupID,
			ChannelID: it.ChannelID,
			ModelName: it.ModelName,
			Priority:  nextPriority,
			Weight:    1,
		})
		nextPriority++
	}

	if err := db.GetDB().WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "group_id"}, {Name: "channel_id"}, {Name: "model_name"}},
			DoNothing: true,
		}).
		Create(&newItems).Error; err != nil {
		return fmt.Errorf("failed to create group items: %w", err)
	}

	return RefreshCacheByID(groupID, ctx)
}

func GroupItemUpdate(item *model.GroupItem, ctx context.Context) error {
	if err := db.GetDB().WithContext(ctx).Model(item).
		Select("model_name", "priority", "weight").
		Updates(item).Error; err != nil {
		return err
	}

	return RefreshCacheByID(item.GroupID, ctx)
}

func GroupItemDel(id int, ctx context.Context) error {
	var item model.GroupItem
	if err := db.GetDB().WithContext(ctx).First(&item, id).Error; err != nil {
		return fmt.Errorf("group item not found")
	}

	if err := db.GetDB().WithContext(ctx).Delete(&item).Error; err != nil {
		return err
	}

	return RefreshCacheByID(item.GroupID, ctx)
}

// GroupItemBatchDelByChannelAndModels 根据渠道ID和模型名称批量删除分组项
func GroupItemBatchDelByChannelAndModels(keys []model.GroupIDAndLLMName, ctx context.Context) error {
	if len(keys) == 0 {
		return nil
	}

	conditions := make([][]interface{}, len(keys))
	for i, key := range keys {
		conditions[i] = []interface{}{key.ChannelID, key.ModelName}
	}

	var groupIDs []int
	if err := db.GetDB().WithContext(ctx).
		Model(&model.GroupItem{}).
		Distinct("group_id").
		Where("(channel_id, model_name) IN ?", conditions).
		Pluck("group_id", &groupIDs).Error; err != nil {
		return fmt.Errorf("failed to find group ids: %w", err)
	}

	if len(groupIDs) == 0 {
		return nil
	}

	if err := db.GetDB().WithContext(ctx).
		Where("(channel_id, model_name) IN ?", conditions).
		Delete(&model.GroupItem{}).Error; err != nil {
		return fmt.Errorf("failed to delete group items: %w", err)
	}

	if err := RefreshCacheByIDs(groupIDs, ctx); err != nil {
		return fmt.Errorf("failed to refresh group cache: %w", err)
	}

	return nil
}

func GroupItemList(groupID int, ctx context.Context) ([]model.GroupItem, error) {
	var items []model.GroupItem
	if err := db.GetDB().WithContext(ctx).
		Where("group_id = ?", groupID).
		Order("priority ASC").
		Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

func RefreshAllCache(ctx context.Context) error {
	groups := []model.Group{}
	if err := db.GetDB().WithContext(ctx).
		Preload("Items").
		Find(&groups).Error; err != nil {
		return err
	}
	groupCache.Clear()
	for _, group := range groups {
		group = normalizeGroup(group)
		groupCache.Set(group.ID, group)
	}
	RebuildIndexes()
	return nil
}

func RefreshCacheByID(id int, ctx context.Context) error {
	var group model.Group
	if err := db.GetDB().WithContext(ctx).
		Preload("Items").
		First(&group, id).Error; err != nil {
		return err
	}
	group = normalizeGroup(group)
	groupCache.Set(group.ID, group)
	RebuildIndexes()
	return nil
}

func RefreshCacheByIDs(ids []int, ctx context.Context) error {
	if len(ids) == 0 {
		return nil
	}
	for _, id := range ids {
		groupCache.Del(id)
	}
	var groups []model.Group
	if err := db.GetDB().WithContext(ctx).
		Preload("Items").
		Where("id IN ?", ids).
		Find(&groups).Error; err != nil {
		return err
	}
	for _, group := range groups {
		group = normalizeGroup(group)
		groupCache.Set(group.ID, group)
	}
	RebuildIndexes()
	return nil
}

// PurgeUnavailableReason 标记某个分组项为什么被判定为不可用。
type PurgeUnavailableReason string

const (
	// PurgeReasonChannelMissing 渠道已被删除，分组项是残留数据。
	PurgeReasonChannelMissing PurgeUnavailableReason = "channel_missing"
	// PurgeReasonChannelDisabled 渠道存在但已被禁用。
	PurgeReasonChannelDisabled PurgeUnavailableReason = "channel_disabled"
	// PurgeReasonModelMissing 渠道存在且启用，但其声明的模型列表已不再包含该模型
	// （通常是渠道更新/同步后模型消失）。
	PurgeReasonModelMissing PurgeUnavailableReason = "model_missing"
)

// PurgeUnavailableResult 汇总一次清理操作的结果。
type PurgeUnavailableResult struct {
	DeletedCount    int `json:"deleted_count"`
	ChannelMissing  int `json:"channel_missing"`
	ChannelDisabled int `json:"channel_disabled"`
	ModelMissing    int `json:"model_missing"`
	AffectedGroups  int `json:"affected_groups"`
	ScannedGroups   int `json:"scanned_groups"`
	ScannedItems    int `json:"scanned_items"`
}

// PurgeUnavailableItems 扫描所有分组，删除指向不可用模型的分组项：
//  1. 渠道已删除（缓存中找不到对应渠道）；
//  2. 渠道已禁用；
//  3. 渠道更新后其声明的模型列表里不再包含该模型。
//
// 删除在单条 IN 语句内完成，随后刷新受影响分组的缓存与索引。
func PurgeUnavailableItems(ctx context.Context) (PurgeUnavailableResult, error) {
	result := PurgeUnavailableResult{}

	// 预先缓存每个渠道声明的模型集合，避免重复 split。
	channelModelSets := make(map[int]map[string]struct{})
	getChannelModels := func(ch model.Channel) map[string]struct{} {
		if set, ok := channelModelSets[ch.ID]; ok {
			return set
		}
		set := make(map[string]struct{})
		for _, name := range xstrings.SplitTrimCompact(",", ch.Model, ch.CustomModel) {
			set[name] = struct{}{}
		}
		channelModelSets[ch.ID] = set
		return set
	}

	groups := groupCache.GetAll()
	var itemIDsToDelete []int
	affectedGroups := make(map[int]struct{})

	for _, group := range groups {
		result.ScannedGroups++
		for _, item := range group.Items {
			result.ScannedItems++
			ch, ok := channel.GetCache().Get(item.ChannelID)
			if !ok {
				itemIDsToDelete = append(itemIDsToDelete, item.ID)
				affectedGroups[group.ID] = struct{}{}
				result.ChannelMissing++
				continue
			}
			if !ch.Enabled {
				itemIDsToDelete = append(itemIDsToDelete, item.ID)
				affectedGroups[group.ID] = struct{}{}
				result.ChannelDisabled++
				continue
			}
			if _, ok := getChannelModels(ch)[item.ModelName]; !ok {
				itemIDsToDelete = append(itemIDsToDelete, item.ID)
				affectedGroups[group.ID] = struct{}{}
				result.ModelMissing++
				continue
			}
		}
	}

	result.DeletedCount = len(itemIDsToDelete)
	result.AffectedGroups = len(affectedGroups)
	if len(itemIDsToDelete) == 0 {
		return result, nil
	}

	if err := db.GetDB().WithContext(ctx).
		Where("id IN ?", itemIDsToDelete).
		Delete(&model.GroupItem{}).Error; err != nil {
		return PurgeUnavailableResult{}, fmt.Errorf("failed to delete group items: %w", err)
	}

	affectedIDs := make([]int, 0, len(affectedGroups))
	for id := range affectedGroups {
		affectedIDs = append(affectedIDs, id)
	}
	if err := RefreshCacheByIDs(affectedIDs, ctx); err != nil {
		return PurgeUnavailableResult{}, fmt.Errorf("failed to refresh group cache: %w", err)
	}

	return result, nil
}
