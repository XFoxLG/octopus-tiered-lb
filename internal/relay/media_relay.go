package relay

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"

	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lingyuins/octopus/internal/conf"
	"github.com/lingyuins/octopus/internal/helper"
	dbmodel "github.com/lingyuins/octopus/internal/model"
	ak "github.com/lingyuins/octopus/internal/op/apikey"
	ch "github.com/lingyuins/octopus/internal/op/channel"
	grp "github.com/lingyuins/octopus/internal/op/group"
	rl "github.com/lingyuins/octopus/internal/op/ratelimitstore"
	"github.com/lingyuins/octopus/internal/op/relaylog"
	"github.com/lingyuins/octopus/internal/op/setting"
	st "github.com/lingyuins/octopus/internal/op/stats"
	"github.com/lingyuins/octopus/internal/relay/balancer"
	"github.com/lingyuins/octopus/internal/relay/condition"
	"github.com/lingyuins/octopus/internal/server/resp"
	"github.com/lingyuins/octopus/internal/utils/log"
	"github.com/lingyuins/octopus/internal/utils/telemetry"
	"github.com/lingyuins/octopus/internal/utils/xurl"
)

func mediaEndpointTypeToGroupEndpointType(endpointType MediaEndpointType) string {
	switch endpointType {
	case MediaEndpointImageGeneration:
		return dbmodel.EndpointTypeImageGeneration
	case MediaEndpointImageEdit:
		return dbmodel.EndpointTypeImageGeneration
	case MediaEndpointImageVariation:
		return dbmodel.EndpointTypeImageGeneration
	case MediaEndpointAudioSpeech:
		return dbmodel.EndpointTypeAudioSpeech
	case MediaEndpointAudioTranscription:
		return dbmodel.EndpointTypeAudioTranscription
	case MediaEndpointVideoGeneration:
		return dbmodel.EndpointTypeVideoGeneration
	case MediaEndpointMusicGeneration:
		return dbmodel.EndpointTypeMusicGeneration
	case MediaEndpointSearch:
		return dbmodel.EndpointTypeSearch
	case MediaEndpointRerank:
		return dbmodel.EndpointTypeRerank
	case MediaEndpointModeration:
		return dbmodel.EndpointTypeModerations
	default:
		return dbmodel.EndpointTypeAll
	}
}

// MediaHandler handles non-LLLM media/utility endpoints by forwarding requests
// directly to upstream channels, reusing the existing channel/group/balancer/circuit-breaker
// infrastructure without going through the Inbound/Outbound transformer pipeline.
func MediaHandler(endpointType MediaEndpointType, c *gin.Context) {
	InflightInc()
	defer InflightDec()
	cfg := getMediaEndpointConfig(endpointType)
	requestTrace := relayRequestTraceFromContext(c)

	// 1. Extract model name from the request
	requestModel, bodyBytes, streamRequested, err := extractModelFromRequest(c, cfg)
	if err != nil {
		resp.Error(c, relayRequestBodyErrorStatus(err), err.Error())
		return
	}
	if cfg.MultipartInput && c.Request.MultipartForm != nil {
		defer c.Request.MultipartForm.RemoveAll()
	}
	if requestModel == "" {
		resp.Error(c, http.StatusBadRequest, "model is required")
		return
	}
	if !dbmodel.ModelMatches(c.GetString("supported_models"), requestModel) {
		resp.Error(c, http.StatusBadRequest, "model not supported")
		return
	}

	apiKeyID := c.GetInt("api_key_id")
	clientIP := c.ClientIP()
	mediaReportedIP := reportedClientIPFromContext(c)
	userAgent := c.Request.UserAgent()
	startTime := time.Now()

	// 2. Resolve channel group
	groupEndpointType := mediaEndpointTypeToGroupEndpointType(endpointType)
	group, err := grp.GroupGetEnabledMapByEndpoint(groupEndpointType, requestModel, c.Request.Context())
	if err != nil {
		log.Infof("model not found in media relay: model=%s endpoint_type=%s reason=%v", requestModel, groupEndpointType, err)
		resp.Error(c, http.StatusNotFound, "model not found")
		return
	}
	if !apiKeyAllowsGroupCategory(c.GetString("allowed_group_categories"), group.Category) {
		log.Infof("media relay: group category not allowed for api key: model=%s category=%s", requestModel, group.Category)
		resp.Error(c, http.StatusBadRequest, "model not supported")
		return
	}
	logEndpointType := resolveRelayLogEndpointType(groupEndpointType, group.EndpointType)

	// Narrow * group items: a * group may contain items that only support
	// specific endpoint types (e.g., chat-only items don't support image_generation).
	// Filter items to only those likely compatible with the requested endpoint.
	if group.EndpointType == dbmodel.EndpointTypeAll && groupEndpointType != dbmodel.EndpointTypeAll {
		narrowed := narrowGroupItemsForEndpoint(group, groupEndpointType)
		if len(narrowed.Items) == 0 {
			log.Infof("no endpoint-matching items in '*' group: model=%s endpoint_type=%s", requestModel, groupEndpointType)
			resp.Error(c, http.StatusNotFound, "model not found")
			return
		}
		group = narrowed
	}

	// 检查条件路由：条件不匹配则跳过（与 LLM relay 保持一致）
	if group.Condition != "" {
		condCtx := condition.RequestContext{
			Model:    requestModel,
			APIKeyID: apiKeyID,
			Hour:     time.Now().UTC().Hour(),
		}
		if match, condErr := condition.Evaluate(group.Condition, condCtx); condErr != nil || !match {
			log.Infof("media relay: condition not met for group %s", group.Name)
			resp.Error(c, http.StatusNotFound, "model not found")
			return
		}
	}

	// 3. Create load balancer iterator
	iter := balancer.NewIterator(group, apiKeyID, requestModel, parseExcludedChannels(c.GetString("excluded_channels")))
	if iter.Len() == 0 {
		resp.Error(c, http.StatusServiceUnavailable, "no available channel")
		return
	}

	operationCtx, cancel := newRelayOperationContext()
	defer cancel()

	maxRouteRetries := getMaxRouteRetries(&group)
	ratelimitCooldown := getRatelimitCooldown()
	maxTotalAttempts := getMaxTotalAttempts()
	rateLimitHoldCfg := getRateLimitHoldConfig()

	var allAttempts []dbmodel.ChannelAttempt
	var lastErr error
	var routeIter *balancer.Iterator

	// 追踪最后一次实际转发的通道信息，用于全部失败时的日志记录
	var lastChannelID int
	var lastChannelName string
	var lastResolvedModel string

	for routeRound := 1; routeRound <= maxRouteRetries; routeRound++ {
		if err := operationCtx.Err(); err != nil {
			lastErr = err
			log.Infof("relay operation ended before media request completed: %v", err)
			goto mediaExhausted
		}
		select {
		case <-c.Request.Context().Done():
			lastErr = c.Request.Context().Err()
			log.Infof("request context canceled, stopping media retry")
			goto mediaExhausted
		default:
		}

		routeIter = balancer.NewIterator(group, apiKeyID, requestModel, parseExcludedChannels(c.GetString("excluded_channels")))

		for routeIter.Next() {
			if maxTotalAttempts > 0 && len(allAttempts) >= maxTotalAttempts {
				lastErr = fmt.Errorf("reached relay max total attempts: %d", maxTotalAttempts)
				goto mediaExhausted
			}
			if err := operationCtx.Err(); err != nil {
				lastErr = err
				log.Infof("relay operation ended before media retry completed: %v", err)
				goto mediaExhausted
			}
			select {
			case <-c.Request.Context().Done():
				lastErr = c.Request.Context().Err()
				log.Infof("request context canceled, stopping media retry")
				goto mediaExhausted
			default:
			}

			item := routeIter.Item()

			channel, err := ch.Get(item.ChannelID, c.Request.Context())
			if err != nil {
				log.Warnf("failed to get channel %d: %v", item.ChannelID, err)
				routeIter.Skip(item.ChannelID, 0, buildChannelName(item.ChannelID), fmt.Sprintf("channel not found: %v", err))
				continue
			}
			if !channel.Enabled {
				routeIter.Skip(channel.ID, 0, channel.Name, "channel disabled")
				continue
			}
			if channel.ConnectionConfig != nil {
				routeIter.Skip(channel.ID, 0, channel.Name, "media forwarding requires a legacy media connection")
				continue
			}

			resolvedModel := resolveCandidateModelName(requestModel, item)
			if resolvedModel == "" {
				routeIter.Skip(channel.ID, 0, channel.Name, "model not found in channel")
				continue
			}
			if rateLimited, remaining := balancer.IsChannelRateLimited(channel.ID, resolvedModel); rateLimited {
				message := "channel rate limit active"
				if remaining > 0 {
					message = fmt.Sprintf("channel rate limit active, remaining cooldown: %ds", int(remaining.Seconds()))
				}
				routeIter.Skip(channel.ID, 0, channel.Name, message)
				lastErr = fmt.Errorf("channel %s is temporarily rate limited", channel.Name)
				continue
			}
			// 渠道级 RPM（阶段1）：按候选选中消耗 1 个 token，超限跳过换下一个候选。
			if channel.RPMLimit > 0 {
				if allowed, retryAfter := rl.CheckChannelRPM(channel.ID, resolvedModel, channel.RPMLimit); !allowed {
					message := "channel rpm limit reached"
					if wait := retryAfter - int(time.Now().Unix()); wait > 0 {
						message = fmt.Sprintf("channel rpm limit reached, retry after: %ds", wait)
					}
					routeIter.Skip(channel.ID, 0, channel.Name, message)
					lastErr = fmt.Errorf("channel %s rpm limit reached", channel.Name)
					continue
				}
			}
			// 渠道并发上限（阶段1）候选级快速检查（非阻塞），与 relay.go 同语义。
			if channel.MaxConcurrency > 0 {
				if inFlight := rl.InFlightChannel(channel.ID); inFlight >= channel.MaxConcurrency {
					routeIter.Skip(channel.ID, 0, channel.Name,
						fmt.Sprintf("channel concurrency limit reached (%d/%d)", inFlight, channel.MaxConcurrency))
					lastErr = fmt.Errorf("channel %s concurrency limit reached", channel.Name)
					continue
				}
			}

			// 渠道内 Key 级重试（阶段2）：预算按候选条目逐个计算，
			// 优先级：条目 RelayRetryCountOverride > 渠道 RelayRetryCountOverride >
			// 分组 RelayRetryCount > 全局设置。
			maxKeyRetriesPerRoute := getMaxAttemptsPerCandidate(channel, &group, &item)
			var failedKeyIDs []int
			rateLimitHoldWaited := time.Duration(0)
		keyRetryLoop:
			for keyRound := 1; keyRound <= maxKeyRetriesPerRoute; keyRound++ {
				if maxTotalAttempts > 0 && len(allAttempts) >= maxTotalAttempts {
					lastErr = fmt.Errorf("reached relay max total attempts: %d", maxTotalAttempts)
					goto mediaExhausted
				}
				if err := operationCtx.Err(); err != nil {
					lastErr = err
					log.Infof("relay operation ended: %v", err)
					goto mediaExhausted
				}
				select {
				case <-c.Request.Context().Done():
					lastErr = c.Request.Context().Err()
					log.Infof("request context canceled, stopping media key retry")
					goto mediaExhausted
				default:
				}

				var usedKey dbmodel.ChannelKey
				// keyRound == 1 但 failedKeyIDs 非空 = 熔断跳过后的重选（跳过分支
				// keyRound-- 不消耗配额），必须排除已失败 key，否则确定性选 key
				// 会选回同一个 key 造成死循环（issue #192，与 relay.go 同因）。
				if keyRound == 1 && len(failedKeyIDs) == 0 {
					usedKey = channel.GetChannelKeyWithCooldown(resolvedModel, ratelimitCooldown)
				} else {
					usedKey = channel.GetChannelKeyExcludingWithCooldown(failedKeyIDs, resolvedModel, ratelimitCooldown)
				}
				if usedKey.ChannelKey == "" {
					// When the key loop exits via break without forwarding
					// (e.g. all keys in rate-limit cooldown), record a skip so the
					// relay log captures the channel info and reason.
					if keyRound == 1 {
						routeIter.Skip(channel.ID, usedKey.ID, channel.Name, "no available key (all keys in cooldown or disabled)")
						lastErr = fmt.Errorf("channel %s: no available key (all keys in cooldown or disabled)", channel.Name)
					}
					break
				}

				// 熔断跳过不消耗 Key 重试配额
				if routeIter.SkipCircuitBreak(channel.ID, usedKey.ID, channel.Name, resolvedModel) {
					failedKeyIDs = append(failedKeyIDs, usedKey.ID)
					keyRound--
					continue
				}

				log.Infof("media relay: endpoint=%d, model=%s, channel: %s model: %s key_id: %d (route R%d, key %d/%d)",
					endpointType, requestModel, channel.Name, resolvedModel, usedKey.ID,
					routeRound, keyRound, maxKeyRetriesPerRoute)

				// 渠道并发上限（阶段1）attempt 级原子占用：forwardMediaRequest 前占名额，
				// 转发结束（无论成败）后立刻释放——获取/释放之间没有任何提前退出路径。
				// 占用失败 = 竞态兜底：不算渠道故障（不记熔断/冷却），按 Skip 跳到下一候选。
				if channel.MaxConcurrency > 0 && !rl.AcquireChannelSlot(channel.ID, channel.MaxConcurrency) {
					log.Warnf("media relay: channel %s concurrency limit reached (%d), skip",
						channel.Name, channel.MaxConcurrency)
					routeIter.Skip(channel.ID, usedKey.ID, channel.Name,
						fmt.Sprintf("channel concurrency limit reached (%d/%d)", channel.MaxConcurrency, channel.MaxConcurrency))
					break
				}
				span := routeIter.StartAttempt(channel.ID, usedKey.ID, channel.Name, resolvedModel)
				statusCode, fwdErr := forwardMediaRequest(c, cfg, group, channel, usedKey.ChannelKey, bodyBytes, requestModel, resolvedModel, streamRequested, operationCtx, requestTrace, span.AttemptNumber())
				if channel.MaxConcurrency > 0 {
					rl.ReleaseChannelSlot(channel.ID)
				}

				// 记录最后一次实际转发的通道信息
				lastChannelID = channel.ID
				lastChannelName = channel.Name
				lastResolvedModel = resolvedModel

				// 客户端主动断开不是渠道故障：与 LLM 链路 errClientDisconnected
				// 豁免同语义。不记熔断/Auto 失败/离群/可用度衰减/key 冷却，
				// 不做 KeyUpdate 与失败统计，直接终态返回（客户端已走，无需回错）。
				if errors.Is(fwdErr, errClientDisconnected) {
					span.End(dbmodel.AttemptFailed, statusCode, "client disconnected")
					allAttempts = append(allAttempts, routeIter.Attempts()...)
					recordMediaRelayLog(apiKeyID, requestModel, logEndpointType, bodyBytes, channel.ID, channel.Name, resolvedModel, time.Since(startTime), allAttempts, errClientDisconnected, clientIP, mediaReportedIP, userAgent, requestTrace)
					return
				}

				written := c.Writer.Written()
				decision := ClassifyRelayError(statusCode, fwdErr, written)
				// 与 LLM 链路同语义:叠加渠道级错误策略与内置智能默认规则,未写出时放宽为可重试。
				if !written {
					decision = applyErrorPolicy(decision, channel, statusCode, extractUpstreamErrorDetail(fwdErr))
				}
				// 客户端断连豁免（对齐 relay.go attempt() 的 errClientDisconnected 语义）。
				// 媒体路径的写失败会把 statusCode 置 0，ClassifyRelayError 只能按
				// EPIPE→网络错误→ScopeNextChannel 或 written→ScopeAbortAll 归类，
				// 把客户端主动停止误判为上游故障：前者会记熔断并触发无谓的换渠道重试，
				// 后者会记熔断。
				// 必须放在 applyErrorPolicy 之后：该函数命中规则时会重建 RetryDecision，
				// 先标记会被它丢掉。
				// 媒体路径全程持有 c，但 operationCtx 来自 newRelayOperationContext()，
				// 基于 context.Background() 与 clientCtx 完全解耦（context.go），
				// 因此判定断连必须查 c.Request.Context()，不能查 operationCtx。
				markClientCancelIfGone(&decision, c.Request.Context(), fwdErr)

				// key 冷却按 (channelID, keyID, model) 维度记录（见 issue #94），不再写整 key 共享的
				// StatusCode/LastUseTimeStamp —— 那样会让某模型 429 拖累该 key 上其他模型。
				if statusCode >= 400 {
					balancer.RecordKeyCooldownWithRetryAfter(channel.ID, usedKey.ID, resolvedModel, statusCode, retryAfterFromError(fwdErr))
				}
				// 可用度衰减：按错误类型加权，仅 availability 策略生效。
				balancer.RecordKeyAvailability(channel.ID, usedKey.ID, resolvedModel, statusCode, false)

				if decision.Scope == ScopeNone && !decision.IsError {
					ch.KeyUpdate(usedKey)
					span.End(dbmodel.AttemptSuccess, statusCode, "")
					st.ChannelUpdate(channel.ID, dbmodel.StatsMetrics{
						WaitTime:       span.Duration().Milliseconds(),
						RequestSuccess: 1,
					})
					balancer.RecordSuccess(channel.ID, usedKey.ID, resolvedModel)
					balancer.RecordChannelRateLimitSuccess(channel.ID, resolvedModel)
					// 离群窗口：记录成功样本（与熔断器同级）。
					balancer.OutlierReport(channel.ID, true, statusCode, time.Now())
					balancer.RecordKeyAvailability(channel.ID, usedKey.ID, resolvedModel, statusCode, true)
					balancer.RecordAutoSuccess(channel.ID, resolvedModel)
					balancer.RecordAutoLatency(channel.ID, resolvedModel, span.Duration().Milliseconds())
					balancer.SetSticky(apiKeyID, requestModel, channel.ID, usedKey.ID, resolvedModel)

					allAttempts = append(allAttempts, routeIter.Attempts()...)
					recordMediaRelayLog(apiKeyID, requestModel, logEndpointType, bodyBytes, channel.ID, channel.Name, resolvedModel, time.Since(startTime), allAttempts, nil, clientIP, mediaReportedIP, userAgent, requestTrace)
					return
				}

				ch.KeyUpdate(usedKey)
				// 决策摘要 + 上游原始错误，使 relay log 能区分 429 等错误的真实成因（issue #93）。
				mediaFailMsg := decision.String()
				if upstreamErr := extractUpstreamErrorDetail(fwdErr); upstreamErr != "" {
					mediaFailMsg = buildErrorMessage(mediaFailMsg, upstreamErr)
				}
				span.End(dbmodel.AttemptFailed, statusCode, mediaFailMsg)
				st.ChannelUpdate(channel.ID, dbmodel.StatsMetrics{
					WaitTime:      span.Duration().Milliseconds(),
					RequestFailed: 1,
				})

				// 熔断守卫与 relay.go 复用同一个纯函数 shouldRecordChannelFailure，避免两文件
				// 判定逻辑漂移。SkipFailureAccounting（客户端断连）由上方
				// markClientCancelIfGone 写入，在此生效。
				if shouldRecordChannelFailure(decision) {
					balancer.RecordFailure(channel.ID, usedKey.ID, resolvedModel)
					balancer.RecordAutoFailure(channel.ID, resolvedModel)
					// 离群窗口：记录失败样本（与熔断器同级）。
					balancer.OutlierReport(channel.ID, false, decision.Code, time.Now())
				}
				if decision.Code == http.StatusTooManyRequests {
					channelRateLimited, remaining := balancer.RecordChannelRateLimit(
						channel.ID,
						usedKey.ID,
						resolvedModel,
						decision.RateLimitScope == RateLimitScopeChannel,
						decision.RetryAfter,
					)
					if channelRateLimited {
						log.Warnf("media channel rate limit opened: channel=%s model=%s remaining=%s",
							channel.Name, resolvedModel, remaining.Round(time.Second))
					}
				}

				if decision.IsError {
					log.Warnf("media relay: channel %s failed on key %d: %v (decision: %s)",
						channel.Name, keyRound, fwdErr, decision.Scope.String())
				}

				switch decision.Scope {
				case ScopeNone:
					lastErr = fwdErr
					allAttempts = append(allAttempts, routeIter.Attempts()...)
					recordMediaRelayLog(apiKeyID, requestModel, logEndpointType, bodyBytes, channel.ID, channel.Name, resolvedModel, time.Since(startTime), allAttempts, fwdErr, clientIP, mediaReportedIP, userAgent, requestTrace)
					// 与 LLM relay 一致：客户端错误原样回给下游，不吞成 502。
					writeClientTerminalError(c, channel, channel.Type, decision.Code, fwdErr)
					return
				case ScopeAbortAll:
					lastErr = fwdErr
					allAttempts = append(allAttempts, routeIter.Attempts()...)
					recordMediaRelayLog(apiKeyID, requestModel, logEndpointType, bodyBytes, channel.ID, channel.Name, resolvedModel, time.Since(startTime), allAttempts, fwdErr, clientIP, mediaReportedIP, userAgent, requestTrace)
					return
				case ScopeSameChannel:
					lastErr = fwdErr
					failedKeyIDs = append(failedKeyIDs, usedKey.ID)
					if decision.Code == http.StatusTooManyRequests {
						if channelRateLimited, _ := balancer.IsChannelRateLimited(channel.ID, resolvedModel); channelRateLimited {
							break keyRetryLoop
						}
						if channel.GetChannelKeyExcludingWithCooldown(failedKeyIDs, resolvedModel, ratelimitCooldown).ChannelKey != "" {
							continue
						}
					}
					// 可选：429 时在当前渠道内延时重试，而不是立刻换 Key/渠道。
					// 默认关闭，保持历史「马上 failover」行为。
					if shouldHoldOnRateLimit(rateLimitHoldCfg, decision) {
						if waitFor, canWait := rateLimitHoldWaitDuration(rateLimitHoldCfg, decision, rateLimitHoldWaited); canWait {
							if !waitRateLimitHoldFor(c.Request.Context(), rateLimitHoldCfg, channel.Name, rateLimitHoldWaited, waitFor) {
								lastErr = c.Request.Context().Err()
								log.Infof("request context canceled during rate limit hold, stopping media key retry")
								goto mediaExhausted
							}
							balancer.ClearKeyCooldown(channel.ID, usedKey.ID, resolvedModel)
							failedKeyIDs = failedKeyIDs[:len(failedKeyIDs)-1]
							rateLimitHoldWaited += waitFor
							if rateLimitHoldWaited > rateLimitHoldCfg.MaxWait {
								rateLimitHoldWaited = rateLimitHoldCfg.MaxWait
							}
							// 不消耗 keyRound 配额：这是时间维度的坚持，不是换 Key。
							keyRound--
							continue
						}
						// 等待预算耗尽：结束本渠道，转下一渠道。
						break keyRetryLoop
					}
				case ScopeNextChannel:
					lastErr = fwdErr
					failedKeyIDs = append(failedKeyIDs, usedKey.ID)
					break keyRetryLoop
				default:
					lastErr = fwdErr
					allAttempts = append(allAttempts, routeIter.Attempts()...)
					recordMediaRelayLog(apiKeyID, requestModel, logEndpointType, bodyBytes, channel.ID, channel.Name, resolvedModel, time.Since(startTime), allAttempts, fwdErr, clientIP, mediaReportedIP, userAgent, requestTrace)
					resp.Error(c, http.StatusBadGateway, lastErr.Error())
					return
				}
			}
		}
		allAttempts = append(allAttempts, routeIter.Attempts()...)
	}
	// All route rounds exhausted
	recordMediaRelayLog(apiKeyID, requestModel, logEndpointType, bodyBytes, lastChannelID, lastChannelName, lastResolvedModel, time.Since(startTime), allAttempts, lastErr, clientIP, mediaReportedIP, userAgent, requestTrace)
	// 对外返回通用文案，上游错误细节仅入日志（同 chat 路径）。
	resp.Error(c, http.StatusBadGateway, "all channels failed")
	return

mediaExhausted:
	// Only reached via goto from within the relay loop (context canceled / max attempts)
	if routeIter != nil {
		allAttempts = append(allAttempts, routeIter.Attempts()...)
	}
	recordMediaRelayLog(apiKeyID, requestModel, logEndpointType, bodyBytes, lastChannelID, lastChannelName, lastResolvedModel, time.Since(startTime), allAttempts, lastErr, clientIP, mediaReportedIP, userAgent, requestTrace)
	resp.Error(c, http.StatusBadGateway, "all channels failed")
}

// recordMediaRelayLog creates a RelayLog entry and updates global stats for media endpoints.
func recordMediaRelayLog(apiKeyID int, requestModel string, endpointType string, bodyBytes []byte, channelID int, channelName string, resolvedModel string, duration time.Duration, attempts []dbmodel.ChannelAttempt, relayErr error, clientIP string, reportedIP reportedClientIP, userAgent string, requestTrace *relayRequestTrace) {
	ctx, cancel := newRelayPersistenceContext()
	defer cancel()

	// 与 relay_log 一致地截断 attempts（issue #192 兜底），防止 media 路径同样把
	// 决策纪录无上限写入日志。
	attempts, totalAttempts := capAttemptsForLog(attempts)

	relayLog := dbmodel.RelayLog{
		Time:                   time.Now().Add(-duration).Unix(),
		RequestModelName:       requestModel,
		RequestAPIKeyID:        apiKeyID,
		ClientIP:               clientIP,
		ReportedClientIP:       reportedIP.IP,
		ReportedClientIPSource: string(reportedIP.Source),
		UserAgent:              userAgent,
		EndpointType:           endpointType,
		ChannelId:              channelID,
		ChannelName:            channelName,
		ActualModelName:        resolvedModel,
		UseTime:                int(duration.Milliseconds()),
		Attempts:               attempts,
		TotalAttempts:          totalAttempts,
		// 媒体端点（生图/生文/转写等）没有 Token 用量概念，诚实标注不适用，
		// 前端不再显示成"未知"。
		UsageState: dbmodel.RelayLogUsageNotApplicable,
	}

	if apiKey, getErr := ak.Get(apiKeyID, ctx); getErr == nil {
		relayLog.RequestAPIKeyName = apiKey.Name
	}

	if len(bodyBytes) > 0 {
		contentEnabled, _ := setting.GetBool(dbmodel.SettingKeyRelayLogContentEnabled)
		if contentEnabled {
			// 与 chat 路径 JSON 字段上限对齐，避免媒体请求 body 无界写入日志缓存。
			const mediaLogBodyMaxBytes = 16 * 1024
			if len(bodyBytes) > mediaLogBodyMaxBytes {
				relayLog.RequestContent = string(bodyBytes[:mediaLogBodyMaxBytes]) + "...(truncated)"
			} else {
				relayLog.RequestContent = string(bodyBytes)
			}
		}
	}

	if relayErr != nil {
		relayLog.Error = relayErr.Error()
	}

	if _, logErr := relaylog.RelayLogAdd(ctx, relayLog); logErr != nil {
		log.Warnf("failed to save media relay log: %v", logErr)
	}

	// Record global and API-key stats (media endpoints don't have token/cost data)
	stats := dbmodel.StatsMetrics{
		WaitTime: int64(duration.Milliseconds()),
	}
	if relayErr == nil {
		stats.RequestSuccess = 1
		log.Infof("media relay complete: model=%s, channel=%d(%s), duration=%dms, attempts=%d",
			requestModel, channelID, channelName, duration.Milliseconds(), len(attempts))
	} else {
		stats.RequestFailed = 1
		log.Infof("media relay failed: model=%s, duration=%dms, attempts=%d, error=%v",
			requestModel, duration.Milliseconds(), len(attempts), relayErr)
	}

	st.TotalUpdate(stats)
	st.HourlyUpdate(stats)
	if statsErr := st.DailyUpdate(ctx, stats); statsErr != nil {
		log.Warnf("failed to update daily stats for media relay: %v", statsErr)
	}
	st.APIKeyUpdate(apiKeyID, stats)
	telemetry.Global().RecordRequest(duration.Milliseconds(), relayErr == nil)
}

func recordPreparedCandidateSkip(iter *balancer.Iterator, item dbmodel.GroupItem, prepare PrepareCandidateResult) {
	if prepare.SkipReason == "" {
		return
	}
	// PrepareCandidate already records circuit-break rejections with cooldown details.
	if prepare.SkipStatus == dbmodel.AttemptCircuitBreak {
		return
	}

	channelID := item.ChannelID
	channelName := buildChannelName(item.ChannelID)
	keyID := 0
	if prepare.Channel != nil {
		channelID = prepare.Channel.ID
		channelName = prepare.Channel.Name
	}
	if prepare.UsedKey.ID != 0 {
		keyID = prepare.UsedKey.ID
	}
	iter.Skip(channelID, keyID, channelName, prepare.SkipReason)
}

// extractModelFromRequest extracts the model name from the request body.
// For JSON endpoints, it parses the body into a generic map.
// For multipart endpoints, it reads the form field.
func extractModelFromRequest(c *gin.Context, cfg mediaEndpointConfig) (string, []byte, bool, error) {
	if cfg.MultipartInput {
		return extractModelFromMultipart(c)
	}
	return extractModelFromJSON(c)
}

// extractModelFromJSON reads the JSON body and extracts the "model" field.
func extractModelFromJSON(c *gin.Context) (string, []byte, bool, error) {
	body, err := readLimitedRequestBody(c, getMaxRelayJSONBodyBytes())
	if err != nil {
		return "", nil, false, err
	}

	var raw map[string]any
	if err := jsonAPI.Unmarshal(body, &raw); err != nil {
		return "", nil, false, fmt.Errorf("invalid JSON body: %w", err)
	}

	model, _ := raw["model"].(string)
	streamRequested := parseMediaStreamFlag(raw["stream"])
	return model, body, streamRequested, nil
}

// extractModelFromMultipart extracts the model from a multipart/form-data request.
// When the inbound request carries a JSON body instead of a multipart form
// (e.g. SenseNova /v1/images/edits accepts JSON with base64 image data-URLs),
// it falls back to JSON extraction so the request can be forwarded as JSON.
func extractModelFromMultipart(c *gin.Context) (string, []byte, bool, error) {
	contentType := c.GetHeader("Content-Type")
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), "multipart/") {
		return extractModelFromJSON(c)
	}

	limitRequestBody(c, getMaxRelayMultipartBodyBytes())

	// Parse the multipart form
	if err := c.Request.ParseMultipartForm(32 << 20); err != nil {
		return "", nil, false, normalizeRelayRequestBodyError(err)
	}

	model := c.Request.FormValue("model")
	streamRequested := strings.EqualFold(strings.TrimSpace(c.Request.FormValue("stream")), "true")
	// We'll re-read the full multipart body in forwardMediaRequestMultipart
	return model, nil, streamRequested, nil
}

// writeErrorOutcome 把向客户端写回响应的失败归因为「客户端断开」或普通写错误。
// 客户端已断开（请求上下文被取消）时返回裸哨兵 errClientDisconnected，与 LLM 链路
// 文案一致：前端按精确文本降级为中性提示，且 MediaHandler 循环据此豁免熔断/Auto
// 失败记账（客户端主动断开不是渠道故障）。
func writeErrorOutcome(c *gin.Context, writeErr error) error {
	if c.Request.Context().Err() != nil {
		return errClientDisconnected
	}
	return writeErr
}

// forwardMediaRequest builds and sends the upstream request, then streams the response back.
func forwardMediaRequest(
	c *gin.Context,
	cfg mediaEndpointConfig,
	group dbmodel.Group,
	channel *dbmodel.Channel,
	key string,
	bodyBytes []byte,
	requestModel string,
	resolvedModel string,
	streamRequested bool,
	operationCtx context.Context,
	requestTrace *relayRequestTrace,
	attemptNumber int,
) (int, error) {
	if cfg.MultipartInput && len(bodyBytes) == 0 {
		return forwardMediaRequestMultipart(c, cfg, channel, key, requestModel, resolvedModel, streamRequested, operationCtx, requestTrace, attemptNumber)
	}
	return forwardMediaRequestJSON(c, cfg, group, channel, key, bodyBytes, requestModel, resolvedModel, streamRequested, operationCtx, requestTrace, attemptNumber)
}

// forwardMediaRequestJSON handles JSON-based media endpoint forwarding.
func forwardMediaRequestJSON(
	c *gin.Context,
	cfg mediaEndpointConfig,
	group dbmodel.Group,
	channel *dbmodel.Channel,
	key string,
	bodyBytes []byte,
	requestModel string,
	resolvedModel string,
	streamRequested bool,
	operationCtx context.Context,
	requestTrace *relayRequestTrace,
	attemptNumber int,
) (int, error) {
	ctx := operationCtx

	// Replace model name in the JSON body
	modifiedBody, err := replaceModelInJSON(bodyBytes, requestModel, resolvedModel)
	if err != nil {
		return 0, fmt.Errorf("failed to replace model in request: %w", err)
	}

	// Apply provider-specific body rewrite for image generation
	modifiedBody, cfg = rewriteImageRequestByProvider(group, cfg, modifiedBody)

	// Apply provider-specific path rewrite for video generation
	cfg = rewriteVideoRequestByProvider(group, cfg)

	// Apply provider-specific body + path rewrite for audio speech
	modifiedBody, cfg = rewriteAudioSpeechRequestByProvider(group, cfg, modifiedBody)

	// Apply provider-specific body + path rewrite for music generation
	modifiedBody, cfg.UpstreamPath, err = rewriteMusicRequestByProvider(group, cfg, modifiedBody, resolvedModel)
	if err != nil {
		return 0, fmt.Errorf("failed to rewrite music request: %w", err)
	}

	// Build upstream URL
	upstreamURL, err := buildMediaUpstreamURL(channel.GetBaseUrl(), cfg.UpstreamPath)
	if err != nil {
		return 0, fmt.Errorf("failed to build upstream URL: %w", err)
	}

	// 透传（信息体）：保留客户端原始请求路径与查询串，仅改写请求体中的
	// model 字段（与对话分组的出站格式 "raw" 语义一致）。
	if strings.EqualFold(strings.TrimSpace(group.EndpointProvider), "raw") {
		upstreamURL, err = buildRawPassthroughUpstreamURL(channel.GetBaseUrl(), c)
		if err != nil {
			return 0, fmt.Errorf("failed to build raw passthrough url: %w", err)
		}
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, upstreamURL, bytes.NewReader(modifiedBody))
	if err != nil {
		return 0, fmt.Errorf("failed to create request: %w", err)
	}

	copyMediaForwardHeaders(req, c, channel, key, "application/json", streamRequested)

	// MiMo Chat Completions API only accepts application/json, but the
	// upstream TTS client (e.g. OpenAI SDK) sends Accept: audio/mpeg.
	// Override after copyMediaForwardHeaders to prevent 406 responses.
	if strings.EqualFold(strings.TrimSpace(group.EndpointProvider), "mimo") && cfg.UpstreamPath == "/v1/chat/completions" {
		req.Header.Set("Accept", "application/json")
	}

	// SSRF 防护（issue #219）
	safeCtx, err := xurl.AssertSafeRequestWithPin(req)
	if err != nil {
		return 0, fmt.Errorf("upstream url is not allowed: %w", err)
	}
	req = req.WithContext(safeCtx)

	// Send request
	httpClient, err := helper.ChannelHttpClient(channel)
	if err != nil {
		return 0, fmt.Errorf("failed to get http client: %w", err)
	}

	response, err := httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("failed to send request: %w", err)
	}
	// 上游可能发完响应头后把 body 永久 hang 住（或极慢涓流）。default 档 client
	// 的 http.Client.Timeout 已改为 0（不限时，见 internal/client/http.go），
	// ResponseHeaderTimeout 只覆盖「等响应头」，operationCtx 在未设
	// OCTOPUS_RELAY_UPSTREAM_TIMEOUT_SECONDS 时也无 deadline（context.go），
	// 因此 body 读取必须自己带上限，否则请求 goroutine 永久挂住。
	//
	// 必须在下面的 defer **之前**替换：Go 在 defer 语句执行时就固定方法接收者，
	// 先包装才能让 defer 调到 watchdog 的 Close（否则 watchdog 收不到退出信号）。
	response.Body = wrapMediaResponseBody(response.Body, mediaBodyIdleTimeout)
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response.StatusCode, readUpstreamHTTPError(response, maxErrorBodyBytes)
	}

	// Stream response back to client
	if cfg.BinaryResponse {
		provider := strings.ToLower(strings.TrimSpace(group.EndpointProvider))
		if provider == "mimo" && cfg.UpstreamPath == "/v1/chat/completions" {
			return handleMimoTTSResponse(c, response, cfg.AudioFormat)
		}
		return handleBinaryResponse(c, response)
	}
	if isMediaSSEResponse(response) {
		return handleSSEResponse(c, response)
	}
	return handleJSONResponse(c, response)
}

// forwardMediaRequestMultipart handles multipart/form-data media endpoint forwarding.
func forwardMediaRequestMultipart(
	c *gin.Context,
	cfg mediaEndpointConfig,
	channel *dbmodel.Channel,
	key string,
	requestModel string,
	resolvedModel string,
	streamRequested bool,
	operationCtx context.Context,
	requestTrace *relayRequestTrace,
	attemptNumber int,
) (int, error) {
	ctx := operationCtx

	// Build upstream URL
	upstreamURL, err := buildMediaUpstreamURL(channel.GetBaseUrl(), cfg.UpstreamPath)
	if err != nil {
		return 0, fmt.Errorf("failed to build upstream URL: %w", err)
	}

	bodyReader, contentType := buildMultipartForwardBody(c.Request.MultipartForm, resolvedModel)

	// Create upstream request
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, upstreamURL, bodyReader)
	if err != nil {
		bodyReader.Close() // 关闭 pipe reader 以释放 writer goroutine
		return 0, fmt.Errorf("failed to create request: %w", err)
	}

	copyMediaForwardHeaders(req, c, channel, key, contentType, streamRequested)

	// SSRF 防护（issue #219）
	safeCtx, err := xurl.AssertSafeRequestWithPin(req)
	if err != nil {
		return 0, fmt.Errorf("upstream url is not allowed: %w", err)
	}
	req = req.WithContext(safeCtx)

	// Send request
	httpClient, err := helper.ChannelHttpClient(channel)
	if err != nil {
		return 0, fmt.Errorf("failed to get http client: %w", err)
	}

	response, err := httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("failed to send request: %w", err)
	}
	// 同 forwardMediaRequestJSON：multipart 路径的响应体读取同样必须有空闲上限，
	// 且同样必须在 defer 之前替换（defer 语句执行时即固定方法接收者）。
	response.Body = wrapMediaResponseBody(response.Body, mediaBodyIdleTimeout)
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response.StatusCode, readUpstreamHTTPError(response, maxErrorBodyBytes)
	}

	if isMediaSSEResponse(response) {
		return handleSSEResponse(c, response)
	}
	return handleJSONResponse(c, response)
}

func parseMediaStreamFlag(raw any) bool {
	switch value := raw.(type) {
	case bool:
		return value
	case string:
		return strings.EqualFold(strings.TrimSpace(value), "true")
	default:
		return false
	}
}

func buildMultipartForwardBody(form *multipart.Form, resolvedModel string) (io.ReadCloser, string) {
	reader, writer := io.Pipe()
	mpWriter := multipart.NewWriter(writer)
	contentType := mpWriter.FormDataContentType()

	go func() {
		defer writer.Close()
		defer mpWriter.Close()
		defer func() {
			if r := recover(); r != nil {
				_ = writer.CloseWithError(fmt.Errorf("panic in multipart builder: %v", r))
			}
		}()

		if form == nil {
			return
		}

		for fieldName, values := range form.Value {
			for _, value := range values {
				fieldValue := value
				if fieldName == "model" && resolvedModel != "" {
					fieldValue = resolvedModel
				}
				if err := mpWriter.WriteField(fieldName, fieldValue); err != nil {
					_ = writer.CloseWithError(fmt.Errorf("failed to write field %s: %w", fieldName, err))
					return
				}
			}
		}

		for fieldName, fileHeaders := range form.File {
			for _, fileHeader := range fileHeaders {
				file, err := fileHeader.Open()
				if err != nil {
					_ = writer.CloseWithError(fmt.Errorf("failed to open uploaded file: %w", err))
					return
				}

				part, err := mpWriter.CreateFormFile(fieldName, fileHeader.Filename)
				if err != nil {
					file.Close()
					_ = writer.CloseWithError(fmt.Errorf("failed to create form file: %w", err))
					return
				}
				if _, err := io.Copy(part, file); err != nil {
					file.Close()
					_ = writer.CloseWithError(fmt.Errorf("failed to copy file content: %w", err))
					return
				}
				file.Close()
			}
		}
	}()

	return reader, contentType
}

// replaceModelInJSON replaces the model field value in a JSON body.
func replaceModelInJSON(body []byte, originalModel, resolvedModel string) ([]byte, error) {
	if resolvedModel == "" || resolvedModel == originalModel {
		return body, nil
	}

	var raw map[string]any
	if err := jsonAPI.Unmarshal(body, &raw); err != nil {
		log.Debugf("replaceModelInJSON: failed to parse JSON body, returning original: %v", err)
		return body, nil
	}

	raw["model"] = resolvedModel
	return jsonAPI.Marshal(raw)
}

// buildMediaUpstreamURL constructs the full upstream URL from base URL and path.
func buildMediaUpstreamURL(baseURL, path string) (string, error) {
	parsed, err := url.Parse(strings.TrimSuffix(baseURL, "/"))
	if err != nil {
		return "", fmt.Errorf("failed to parse base url: %w", err)
	}

	basePath := strings.TrimSuffix(parsed.Path, "/")
	normalizedPath := path
	if strings.HasSuffix(basePath, "/v1") && strings.HasPrefix(normalizedPath, "/v1/") {
		normalizedPath = strings.TrimPrefix(normalizedPath, "/v1")
	}

	parsed.Path = basePath + normalizedPath
	return parsed.String(), nil
}

// buildRawPassthroughUpstreamURL 将客户端原始请求路径与查询串拼接到渠道
// base URL 上（透传（信息体）模式）。路径拼接复用 buildMediaUpstreamURL 的
// /v1 去重规则，保证 base URL 以 /v1 结尾时不会重复版本段。
func buildRawPassthroughUpstreamURL(baseURL string, c *gin.Context) (string, error) {
	rawPath := ""
	query := ""
	if c != nil && c.Request != nil && c.Request.URL != nil {
		rawPath = c.Request.URL.Path
		query = c.Request.URL.RawQuery
	}
	if !strings.HasPrefix(rawPath, "/") {
		rawPath = "/" + rawPath
	}

	upstreamURL, err := buildMediaUpstreamURL(baseURL, rawPath)
	if err != nil {
		return "", err
	}
	if query != "" {
		separator := "?"
		if strings.Contains(upstreamURL, "?") {
			separator = "&"
		}
		upstreamURL += separator + query
	}
	return upstreamURL, nil
}

// applyChannelHeaders applies channel custom headers to the request.
func applyChannelHeaders(req *http.Request, channel *dbmodel.Channel) {
	if len(channel.CustomHeader) > 0 {
		for _, header := range channel.CustomHeader {
			req.Header.Set(header.HeaderKey, header.HeaderValue)
		}
	}
}

func copyMediaForwardHeaders(req *http.Request, c *gin.Context, channel *dbmodel.Channel, key string, contentType string, streamRequested bool) {
	for headerKey, values := range c.Request.Header {
		if hopByHopHeaders[strings.ToLower(headerKey)] {
			continue
		}
		if strings.EqualFold(headerKey, "Authorization") || strings.EqualFold(headerKey, "Content-Type") || strings.EqualFold(headerKey, "Content-Length") {
			continue
		}
		for _, value := range values {
			req.Header.Add(headerKey, value)
		}
	}

	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if streamRequested {
		req.Header.Set("Accept", "text/event-stream")
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	applyChannelHeaders(req, channel)
}

// defaultMediaBodyIdleTimeout 是媒体转发路径等待上游响应体「下一批字节」的最长空闲时长。
//
// 取值 600s 与 default 档 http.Client.Timeout 的旧值一致，这是有意为之：
// 旧值是「响应头 + 整个 body」的**总时长**上限，而这里是「两次读到字节之间」的
// **空闲**上限。因为空闲时长恒 <= 总时长，所以新机制对任何在旧 600s 总上限内能
// 正常完成的工作负载都严格更宽松（不会新引入误杀），同时又恢复了「有限上限」这
// 一性质（不再永久挂住）。媒体端点包含视频 / 音乐生成，响应体可能又大又慢，
// 用总时长上限会误杀合法的慢速下载，用空闲上限则只杀「上游彻底 stall」。
const defaultMediaBodyIdleTimeout = 600 * time.Second

// mediaBodyIdleTimeoutEnv 覆盖 defaultMediaBodyIdleTimeout（单位：秒）。
//
// 与 OCTOPUS_RELAY_UPSTREAM_TIMEOUT_SECONDS / OCTOPUS_HTTP_RESPONSE_HEADER_TIMEOUT_SECONDS
// 同属「绕过 Viper、直接 os.Getenv、在 init() 一次性读取」的一批开关
// （见 context.go 的既有写法）。取值 <=0 或非法时保持默认值。
func mediaBodyIdleTimeoutEnv() string {
	return strings.ToUpper(conf.APP_NAME) + "_RELAY_MEDIA_BODY_IDLE_TIMEOUT_SECONDS"
}

// mediaBodyIdleTimeout 是实际生效的媒体响应体空闲超时。
// 声明为 var 而非 const，测试可临时改小（与 relayUpstreamTimeout 的既有模式一致）。
var mediaBodyIdleTimeout = defaultMediaBodyIdleTimeout

func init() {
	mediaBodyIdleTimeout = resolveMediaBodyIdleTimeout(os.Getenv(mediaBodyIdleTimeoutEnv()), defaultMediaBodyIdleTimeout)
}

// resolveMediaBodyIdleTimeout 解析环境变量覆盖值（单位：秒）。
// 空值 / 非法数字 / <=0 一律回退到 fallback，绝不产生「无上限」的退化值。
func resolveMediaBodyIdleTimeout(raw string, fallback time.Duration) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 {
		return fallback
	}
	return time.Duration(seconds) * time.Second
}

// wrapMediaResponseBody 在上游响应体上安装空闲超时 watchdog。
// idleTimeout <= 0 时原样返回，不引入任何包装与 goroutine。
func wrapMediaResponseBody(src io.ReadCloser, idleTimeout time.Duration) io.ReadCloser {
	if src == nil || idleTimeout <= 0 {
		return src
	}
	return newMediaIdleTimeoutBody(src, idleTimeout)
}

// mediaIdleTimeoutBody 包装上游响应体：连续 idleTimeout 没有任何新字节到达时，
// 关闭底层 body，强制正阻塞在 Read 上的 io.Copy / io.ReadAll / bufio.ReadSlice
// 立刻返回，从而终结「上游发完响应头就 hang 住 body」造成的请求 goroutine 永久挂死。
//
// 为什么必须靠 Close() 而不是 SetReadDeadline：http.Response.Body 的实际类型是
// net/http 内部的 *bodyEOFSignal（外层）套 *transfer.body（内层），拿不到底层
// net.Conn，无法可靠地设置读截止时间；而 bodyEOFSignal.Read 在进入真正阻塞的
// 底层 Read **之前**就已释放自身互斥锁，因此从另一个 goroutine 调 Close() 不会
// 与并发的 Read 互相死锁。这与 relay.go 的 client-gone 宽限超时后
// response.Body.Close() 强制收尾是同一套机制。
type mediaIdleTimeoutBody struct {
	src io.ReadCloser

	idleTimeout time.Duration

	// base 是构造时刻的时间戳，只写入一次。**它必须保留 time.Now() 自带的单调时钟
	// 读数**：后续所有经过时间都由 time.Since(base) 得出，因而基于单调时钟，
	// 不受系统墙钟回拨 / NTP 前跳影响。
	// 若改用 UnixNano 存取，会丢掉单调读数（time.Unix 构造出的是纯墙钟时间），
	// 一次 NTP 前跳就可能把正在正常下载的视频当成「空闲」误杀。
	base time.Time

	// lastProgressOffsetNanos 记录最近一次读到字节时距 base 的纳秒数。
	// 存偏移量而非绝对时间戳：既能保持单调语义，又避免在热路径上分配 *time.Time。
	lastProgressOffsetNanos atomic.Int64
	// fired 标记 watchdog 已因空闲超时关闭了底层 body。
	fired atomic.Bool

	// stop 由 Close() 关闭，通知 watchdog 退出；stopOnce 保证 Close 可重入。
	stopOnce sync.Once
	stop     chan struct{}

	// errIdleTimeout 是超时后统一上报的错误。文案刻意包含 "timeout"，
	// 使 type.go 的 isTimeoutError 命中 → ClassifyRelayError 归为 ScopeNextChannel。
	errIdleTimeout error
}

func newMediaIdleTimeoutBody(src io.ReadCloser, idleTimeout time.Duration) *mediaIdleTimeoutBody {
	b := &mediaIdleTimeoutBody{
		src:            src,
		idleTimeout:    idleTimeout,
		stop:           make(chan struct{}),
		errIdleTimeout: fmt.Errorf("media relay: upstream response body idle timeout (no data for %s)", idleTimeout),
	}
	b.base = time.Now()
	b.lastProgressOffsetNanos.Store(0)
	go b.watch()
	return b
}

// watch 是空闲超时 watchdog。退出路径只有两条，且都会退出：
//  1. b.stop 被关闭 —— 由调用方的 defer response.Body.Close() 触发，覆盖正常完成、
//     读失败、写失败、客户端断连、panic 展开（defer 在 panic 时同样执行）全部路径；
//  2. watchdog 自己触发超时 —— 关闭底层 body 后立刻 return。
//
// 因此不存在 goroutine 泄漏：每个响应体至多产生一个 watchdog，且其生命周期被
// 响应体的 Close 严格包住。
func (b *mediaIdleTimeoutBody) watch() {
	// 用「到期后按需重新武装的 timer」而非周期 ticker：
	//   - 触发时刻精确落在「最后一次有进展起算 idleTimeout」那一刻，没有轮询超调；
	//   - 唤醒次数更少：持续有进展的下载每个空闲周期至多醒一次，而 ticker
	//     每 idleTimeout/4 就醒一次；
	//   - 不需要在热路径（Read）上碰定时器，避免争用。
	//
	// 计时基于 time.Since(b.base)，而 base 保留了 time.Now() 的单调时钟读数，
	// 因此不受系统墙钟回拨 / NTP 校时影响。
	timer := time.NewTimer(b.idleTimeout)
	defer timer.Stop()

	for {
		select {
		case <-b.stop:
			return
		case <-timer.C:
			// 距最后一次「真正读到字节」已经过去多久。
			idle := time.Since(b.base) - time.Duration(b.lastProgressOffsetNanos.Load())
			if remaining := b.idleTimeout - idle; remaining > 0 {
				// 期间上游仍在产出：按剩余时间重新武装，继续等。
				timer.Reset(remaining)
				continue
			}
			// 先置 fired 再 Close：Read 侧据此把随后到来的
			// "use of closed network connection" 归一成空闲超时错误。
			b.fired.Store(true)
			// 关闭失败也无从补救（底层连接已不可用），忽略返回值。
			_ = b.src.Close()
			return
		}
	}
}

func (b *mediaIdleTimeoutBody) Read(p []byte) (int, error) {
	if b.fired.Load() {
		return 0, b.errIdleTimeout
	}
	n, err := b.src.Read(p)
	if n > 0 {
		// 只有真正读到字节才算「有进展」：n==0 且 err==nil 的合法空读不重置计时，
		// 否则上游可以用零长度读把 watchdog 永久喂住。
		// 存的是相对 base 的偏移（单调），不存墙钟时间戳。
		b.lastProgressOffsetNanos.Store(time.Since(b.base).Nanoseconds())
	}
	// 读失败与 watchdog 触发几乎同时发生（正是 watchdog 关闭 body 造成的失败）时，
	// 统一上报为空闲超时。io.EOF 必须原样放行：那是响应正常结束，不是故障。
	if err != nil && !errors.Is(err, io.EOF) && b.fired.Load() {
		return n, b.errIdleTimeout
	}
	return n, err
}

// Close 停止 watchdog 并关闭底层 body。可安全重复调用：stopOnce 保证 stop 只关一次，
// 而 net/http 的 bodyEOFSignal.Close / transfer.body.Close 对已关闭的 body 直接返回 nil
// （已实测确认），所以 watchdog 先关、调用方 defer 再关不会出错也不会死锁。
func (b *mediaIdleTimeoutBody) Close() error {
	b.stopOnce.Do(func() { close(b.stop) })
	return b.src.Close()
}

// handleBinaryResponse streams a binary response (e.g. audio) back to the client.
func handleBinaryResponse(c *gin.Context, response *http.Response) (int, error) {
	// Copy relevant headers
	if ct := response.Header.Get("Content-Type"); ct != "" {
		c.Header("Content-Type", ct)
	}
	c.Header("Content-Disposition", response.Header.Get("Content-Disposition"))

	_, err := io.Copy(c.Writer, response.Body)
	if err != nil {
		return 0, writeErrorOutcome(c, fmt.Errorf("failed to stream binary response: %w", err))
	}

	return response.StatusCode, nil
}

func isMediaSSEResponse(response *http.Response) bool {
	if response == nil {
		return false
	}
	return strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream")
}

func handleSSEResponse(c *gin.Context, response *http.Response) (int, error) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	reader := getReader(response.Body)
	defer putReader(reader)
	for {
		// ReadSlice 在单行超过内部缓冲时返回 bufio.ErrBufferFull 且不再扩容，
		// 用其代替 ReadBytes 以限制恶意上游「无限长一行」的内存放大。
		line, err := reader.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			return 0, fmt.Errorf("sse line exceeds %d bytes", reader.Size())
		}
		if len(line) > 0 {
			if _, writeErr := c.Writer.Write(line); writeErr != nil {
				return 0, writeErrorOutcome(c, fmt.Errorf("failed to stream sse response: %w", writeErr))
			}
			c.Writer.Flush()
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return response.StatusCode, nil
			}
			return 0, fmt.Errorf("failed to read sse response: %w", err)
		}
	}
}

// handleJSONResponse streams a JSON response back to the client.
func handleJSONResponse(c *gin.Context, response *http.Response) (int, error) {
	// For large responses (e.g. image generation with base64), stream directly
	if ct := response.Header.Get("Content-Type"); ct != "" {
		c.Header("Content-Type", ct)
	}

	_, err := io.Copy(c.Writer, response.Body)
	if err != nil {
		return 0, writeErrorOutcome(c, fmt.Errorf("failed to stream response: %w", err))
	}

	return response.StatusCode, nil
}

type musicGenerationChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func rewriteMusicRequestByProvider(group dbmodel.Group, cfg mediaEndpointConfig, body []byte, resolvedModel string) ([]byte, string, error) {
	if cfg.UpstreamPath != "/v1/music/generations" {
		return body, cfg.UpstreamPath, nil
	}
	provider := strings.ToLower(strings.TrimSpace(group.EndpointProvider))
	if provider == "" || provider == "auto" {
		return body, cfg.UpstreamPath, nil
	}

	if provider != "newapi" && provider != "minimax" {
		return body, cfg.UpstreamPath, nil
	}

	var raw map[string]any
	if err := jsonAPI.Unmarshal(body, &raw); err != nil {
		return nil, "", err
	}

	raw["model"] = resolvedModel
	if _, ok := raw["messages"]; !ok {
		prompt := strings.TrimSpace(fmt.Sprintf("%v", raw["prompt"]))
		if prompt != "" && prompt != "<nil>" {
			raw["messages"] = []musicGenerationChatMessage{{Role: "user", Content: prompt}}
		}
	}
	delete(raw, "prompt")

	converted, err := jsonAPI.Marshal(raw)
	if err != nil {
		return nil, "", err
	}
	return converted, "/v1/music_generation", nil
}

// rewriteImageRequestByProvider adjusts the request body for image generation
// based on the group's EndpointProvider setting. Agnes requires response_format
// to be placed inside extra_body rather than at the top level; leaving it at the
// top level triggers a 400 UnsupportedParamsError from the upstream.
func rewriteImageRequestByProvider(group dbmodel.Group, cfg mediaEndpointConfig, body []byte) ([]byte, mediaEndpointConfig) {
	if cfg.UpstreamPath != "/v1/images/generations" {
		return body, cfg
	}
	provider := strings.ToLower(strings.TrimSpace(group.EndpointProvider))
	if provider != "agnes" {
		return body, cfg
	}

	var raw map[string]any
	if err := jsonAPI.Unmarshal(body, &raw); err != nil {
		return body, cfg
	}

	topRF, hasTop := raw["response_format"]
	if !hasTop || topRF == nil {
		return body, cfg
	}

	extra, _ := raw["extra_body"].(map[string]any)
	if extra == nil {
		extra = map[string]any{}
	}
	// Preserve an explicitly set extra_body.response_format over the top-level one.
	if _, exists := extra["response_format"]; !exists {
		extra["response_format"] = topRF
	}
	raw["extra_body"] = extra
	delete(raw, "response_format")

	converted, err := jsonAPI.Marshal(raw)
	if err != nil {
		return body, cfg
	}
	return converted, cfg
}

// rewriteVideoRequestByProvider adjusts the upstream path for video generation
// based on the group's EndpointProvider setting.
// Agnes Video V2.0 uses POST /v1/videos instead of the standard /v1/videos/generations.
func rewriteVideoRequestByProvider(group dbmodel.Group, cfg mediaEndpointConfig) mediaEndpointConfig {
	if cfg.UpstreamPath != "/v1/videos/generations" {
		return cfg
	}
	provider := strings.ToLower(strings.TrimSpace(group.EndpointProvider))
	switch provider {
	case "agnes":
		cfg.UpstreamPath = "/v1/videos"
	}
	return cfg
}

// rewriteAudioSpeechRequestByProvider converts the request body and path for
// provider-specific TTS implementations. MiMo TTS uses the Chat Completions API
// format (POST /v1/chat/completions) instead of the standard /v1/audio/speech.
func rewriteAudioSpeechRequestByProvider(group dbmodel.Group, cfg mediaEndpointConfig, body []byte) ([]byte, mediaEndpointConfig) {
	if cfg.UpstreamPath != "/v1/audio/speech" {
		return body, cfg
	}
	provider := strings.ToLower(strings.TrimSpace(group.EndpointProvider))
	if provider != "mimo" {
		return body, cfg
	}

	var raw map[string]any
	if err := jsonAPI.Unmarshal(body, &raw); err != nil {
		return body, cfg
	}

	input, _ := raw["input"].(string)
	voice, _ := raw["voice"].(string)
	format, _ := raw["response_format"].(string)
	model, _ := raw["model"].(string)

	if format == "" {
		format = "wav"
	}
	// MiMo TTS only supports wav, mp3, pcm, pcm16.
	// Map unsupported formats (opus, flac, aac, etc.) to mp3.
	if format != "wav" && format != "mp3" && format != "pcm" && format != "pcm16" {
		format = "mp3"
	}
	cfg.AudioFormat = format
	if voice == "" {
		voice = "mimo_default"
	}

	mimoReq := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "assistant", "content": input},
		},
		"audio": map[string]string{
			"format": format,
			"voice":  voice,
		},
	}

	converted, err := jsonAPI.Marshal(mimoReq)
	if err != nil {
		return body, cfg
	}
	cfg.UpstreamPath = "/v1/chat/completions"
	return converted, cfg
}

// maxMimoTTSResponseBytes 是 MiMo TTS 响应体的大小上限（32 MiB）。
// 采用 max+1 模式：读出长度超过上限即视为异常超大响应，拒绝而非 OOM
// （参照 relay.go maxErrorBodyBytes 的既有模式）。正常 TTS 音频远小于此。
const maxMimoTTSResponseBytes = 32 * 1024 * 1024

// mimoTTSChatResponse represents the relevant fields of a MiMo TTS chat completion response.
type mimoTTSChatResponse struct {
	Choices []struct {
		Message struct {
			Audio *struct {
				Data string `json:"data"`
			} `json:"audio"`
		} `json:"message"`
	} `json:"choices"`
}

// handleMimoTTSResponse extracts the base64-encoded audio from a MiMo chat
// completion JSON response and sends it as binary audio to the client.
func handleMimoTTSResponse(c *gin.Context, response *http.Response, audioFormat string) (int, error) {
	// 限制响应体大小，避免异常超大 TTS 响应全量载入导致 OOM（base64 解码还会
	// 再分配一份等大内存）。采用 max+1 模式区分"恰好上限"与"超过上限"。
	respBody, err := io.ReadAll(io.LimitReader(response.Body, int64(maxMimoTTSResponseBytes)+1))
	if err != nil {
		return 0, fmt.Errorf("failed to read MiMo TTS response: %w", err)
	}
	if len(respBody) > maxMimoTTSResponseBytes {
		return response.StatusCode, fmt.Errorf("MiMo TTS response exceeds %d bytes limit", maxMimoTTSResponseBytes)
	}

	var mimoResp mimoTTSChatResponse
	if err := jsonAPI.Unmarshal(respBody, &mimoResp); err != nil {
		return response.StatusCode, fmt.Errorf("failed to parse MiMo TTS response: %w", err)
	}

	if len(mimoResp.Choices) == 0 || mimoResp.Choices[0].Message.Audio == nil {
		return response.StatusCode, fmt.Errorf("MiMo TTS response contains no audio data")
	}

	audioData, err := base64.StdEncoding.DecodeString(mimoResp.Choices[0].Message.Audio.Data)
	if err != nil {
		return response.StatusCode, fmt.Errorf("failed to decode MiMo TTS audio: %w", err)
	}

	// Set Content-Type based on the resolved audio format.
	contentType := "audio/wav"
	switch audioFormat {
	case "mp3":
		contentType = "audio/mpeg"
	case "pcm", "pcm16":
		contentType = "audio/pcm"
	}
	c.Header("Content-Type", contentType)
	_, err = c.Writer.Write(audioData)
	if err != nil {
		return 0, fmt.Errorf("failed to write MiMo TTS audio: %w", err)
	}

	return response.StatusCode, nil
}
