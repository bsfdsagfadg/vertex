package vertex

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/cli"
	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/nodes"
)

// raceConfig 是 RunRace 的可配置策略。
type raceConfig[T any] struct {
	noCancelOnSuccess bool
	// isWinningResult 决定某个成功结果是否可"立即胜出"。
	// nil 表示首个无错误结果立即胜出（流式默认）。
	// 非 nil：返回 true 即时胜出，false 表示结果被收集（CompleteChat 的非 STOP 结果）。
	isWinningResult func(val T) bool
	// collectedResults 累积 isWinningResult 返回 false 的成功结果。
	collectedResults []raceResult[T]
	// finalizeCollected 在收集多个非胜出结果后调用，选出最佳结果。
	// nil 时直接返回收集到的第一个结果。
	finalizeCollected func([]raceResult[T]) (T, error)
}

type RaceOption[T any] func(*raceConfig[T])

type raceRoundKey struct{}

// WithNoCancelOnSuccess 使胜出路径上不 cancel 主竞速 ctx（流式胜出后保留主 ctx 让数据流继续传完）。
func WithNoCancelOnSuccess[T any]() RaceOption[T] {
	return func(cfg *raceConfig[T]) {
		cfg.noCancelOnSuccess = true
	}
}

// WithWinningCheck 注入成功判定：fn 返回 true 时该结果立即胜出；
// fn 返回 false 时结果被收集，所有候选结束后通过 WithCollectedFinalizer 选最佳结果。
func WithWinningCheck[T any](fn func(T) bool) RaceOption[T] {
	return func(cfg *raceConfig[T]) {
		cfg.isWinningResult = fn
	}
}

// WithCollectedFinalizer 注入最终结果选择函数，在收集多个非胜出结果后调用。
// 默认行为：返回收集到的第一个结果。
func WithCollectedFinalizer[T any](fn func([]raceResult[T]) (T, error)) RaceOption[T] {
	return func(cfg *raceConfig[T]) {
		cfg.finalizeCollected = fn
	}
}

type raceResult[T any] struct {
	uri string
	val T
	err error
}

// errorPriority 返回错误的优先级数值（越小优先级越高）。
// 可重试错误优先于不可重试错误，避免一个节点的参数/安全错误掩盖
// 另一个节点返回的临时认证、限流或上游故障。
func errorPriority(err error) int {
	var ve *VertexError
	if errors.As(err, &ve) {
		if ve.IsRetryable() {
			if ve.Kind == "ratelimit" || ve.Code == 429 {
				return 1
			}
			return 2
		}
		if ve.IsGlobalHardError() {
			return 3
		}
		return 4
	}
	return 5
}

// pickBestError 从多个错误中挑选优先级最高（数值最小）的一个返回。
func pickBestError(errs []error) error {
	if len(errs) == 0 {
		return fmt.Errorf("all nodes failed")
	}
	best := errs[0]
	bestPrio := errorPriority(best)
	for _, e := range errs[1:] {
		if p := errorPriority(e); p < bestPrio {
			best = e
			bestPrio = p
		}
	}
	return best
}

// RunRace runs a hedged race across candidate nodes.
//
// 模型：每轮先启动首个候选，其余候选按固定或动态延迟接力；当前已启动候选
// 全部提前失败时立即启动下一个候选，不额外等待计时器。
//
// 轮次换批（重试）：
//   - 单节点重试关闭（ParallelPoolRetryEnabled=false）：每轮节点全部失败后，
//     重新 SelectForParallel 换一批从未用过的节点再试，最多 MaxRetries 批
//     （总轮数 = MaxRetries + 1，每轮至多并发数个节点）。
//   - 单节点重试开启：重试在节点内 attempt 循环完成，竞速层不换批（roundBudget=0）。
//
// 其他行为：
//   - 单节点独立超时（RaceTimeout>0）：某节点超时即单独淘汰，不影响其他节点与整轮推进。
//   - sticky pool 优先/降级单节点（并发池关闭或无候选）。
//   - 429 → RecordRateLimit(30s) + Evict；其他失败 → RecordTest(false) + Evict。
//   - 单个候选的不可重试错误只记录，全部候选结束后按错误优先级收敛。
//   - 胜出后其余节点在后台收集（30s 上限），不影响已胜出节点的数据流。
//   - context.Canceled 不视为失败。
func RunRace[T any](ctx context.Context, cfg config.ConfigProvider,
	run func(ctx context.Context, proxyURI string) (T, error),
	opts ...RaceOption[T],
) (T, error) {
	var rc raceConfig[T]
	for _, o := range opts {
		o(&rc)
	}

	stickyPool := nodes.GetStickyPool()
	raceTimeout := cfg.RaceTimeout()

	// 换批预算：关单节点重试时，重试由"换一批新节点"完成（最多 MaxRetries 批）。
	roundBudget := 0
	if cfg.ParallelPoolEnabled() && !cfg.ParallelPoolRetryEnabled() {
		roundBudget = cfg.MaxRetries()
	}

	usedURIs := make(map[string]bool)
	var zero T

	selectFreshCands := func() []nodes.Node {
		cands := nodes.SelectForParallel(cfg.ParallelPoolSize(), cfg.ParallelNodeTopK(), cfg.DebugMode(), cfg.StickyNodePriority())
		fresh := make([]nodes.Node, 0, len(cands))
		for _, c := range cands {
			if !usedURIs[c.RawURI] {
				fresh = append(fresh, c)
			}
		}
		return fresh
	}

	cands := selectFreshCands()
	if !cfg.ParallelPoolEnabled() || len(cands) == 0 {
		proxy := cfg.ActiveNodeURI()
		if proxy == "" {
			proxy = cfg.ProxyURL()
		}
		log.Printf("[Vertex] [RunParallel] 降级为单节点运行: %s", nodes.GetNodeName(proxy))
		return run(ctx, proxy)
	}
	if cfg.DebugMode() {
		log.Printf("[Vertex] [RunParallel] 开启对冲延迟竞速, %d 个节点参与", len(cands))
		for _, c := range cands {
			log.Printf("[Vertex] [RunParallel] 参与节点: %s", c.Name)
		}
	}

	cli.UpdateReqState(RequestIDFromContext(ctx), "⚡ 并发竞速", "\033[33m", fmt.Sprintf("并行节点: %d", len(cands)))

	ctxRace, cancel := context.WithCancel(ctx) //nolint:govet // cancel called on error paths; win path relies on parent ctx
	var returnedOnWinPath bool
	defer func() {
		if !returnedOnWinPath || !rc.noCancelOnSuccess {
			cancel()
		}
	}()

	cancels := make(map[string]context.CancelFunc)
	var cancelsMu sync.Mutex
	cancelCandidate := func(uri string) {
		cancelsMu.Lock()
		cancelFn := cancels[uri]
		cancelsMu.Unlock()
		if cancelFn != nil {
			cancelFn()
		}
	}

	recordResult := func(res raceResult[T]) {
		if res.err == nil {
			stickyPool.Add(res.uri)
			return
		}
		if errors.Is(res.err, context.Canceled) {
			return
		}

		ve := asVertexError(res.err)
		if ve != nil && ve.IsGlobalHardError() {
			return
		}
		if ve != nil && ve.Kind == "ratelimit" {
			nodes.RecordRateLimit(res.uri, 30)
			stickyPool.Evict(res.uri)
			return
		}

		nodes.RecordTest(res.uri, false, 0, res.err.Error())
		stickyPool.Evict(res.uri)
	}

	var failedErrors []error

	for round := 0; ; round++ {
		resCh := make(chan raceResult[T], len(cands))
		var active int32

		launchCandidate := func(c nodes.Node) {
			uri := c.RawURI
			usedURIs[uri] = true

			// candCtx 只表达候选生命周期，不携带 race_timeout deadline。
			// race_timeout 由下方独立计时器约束“run 返回首个可判定结果”的等待阶段；
			// 流式候选首包胜出后继续沿用 candCtx，不会在固定总时长后被截断。
			candCtx, candCancel := context.WithCancel(ctxRace)
			candCtx = context.WithValue(candCtx, raceRoundKey{}, round)

			cancelsMu.Lock()
			cancels[uri] = candCancel
			cancelsMu.Unlock()

			atomic.AddInt32(&active, 1)
			go func(u string, candidateCtx context.Context, candidateCancel context.CancelFunc) {
				nodes.IncInFlight(u)
				defer nodes.DecInFlight(u)

				resultReady := make(chan raceResult[T], 1)
				go func() {
					result := raceResult[T]{uri: u}
					func() {
						defer func() {
							if recovered := recover(); recovered != nil {
								result.err = NewInternalError(fmt.Sprintf("节点 %s 候选执行 panic: %v", nodes.GetNodeName(u), recovered))
							}
						}()
						result.val, result.err = run(candidateCtx, u)
					}()
					resultReady <- result
				}()

				if raceTimeout <= 0 {
					select {
					case result := <-resultReady:
						resCh <- result
					case <-candidateCtx.Done():
						select {
						case result := <-resultReady:
							resCh <- result
						default:
							resCh <- raceResult[T]{uri: u, err: candidateCtx.Err()}
						}
					}
					return
				}

				timer := time.NewTimer(time.Duration(raceTimeout) * time.Second)
				defer timer.Stop()
				select {
				case result := <-resultReady:
					resCh <- result
				case <-timer.C:
					candidateCancel()
					resCh <- raceResult[T]{
						uri: u,
						err: NewUnavailableError(fmt.Sprintf("节点 %s 竞速超时（%d 秒），已淘汰", nodes.GetNodeName(u), raceTimeout)),
					}
				case <-candidateCtx.Done():
					select {
					case result := <-resultReady:
						resCh <- result
					default:
						resCh <- raceResult[T]{uri: u, err: candidateCtx.Err()}
					}
				}
			}(uri, candCtx, candCancel)
		}

		delay := time.Duration(cfg.ParallelPoolDelayMs()) * time.Millisecond
		if cfg.ParallelPoolDelayDynamic() {
			delay = time.Duration(nodes.GetAverageLatency()) * time.Millisecond
		}
		if delay < 0 {
			delay = 0
		}

		nextIdx := 0
		launchNext := func() bool {
			if nextIdx >= len(cands) {
				return false
			}
			candidate := cands[nextIdx]
			nextIdx++
			launchCandidate(candidate)
			return true
		}
		launchNext()

		timer := time.NewTimer(delay)
		if nextIdx >= len(cands) {
			if !timer.Stop() {
				<-timer.C
			}
		}
		resetTimer := func() {
			if nextIdx >= len(cands) {
				return
			}
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(delay)
		}

	InnerLoop:
		for {
			select {
			case <-ctx.Done():
				timer.Stop()
				cancel()
				return zero, ctx.Err()

			case <-timer.C:
				if nextIdx < len(cands) {
					if cfg.DebugMode() {
						log.Printf("[Racing] 对冲延迟唤醒，启动备份节点: %s", cands[nextIdx].Name)
					}
					launchNext()
					resetTimer()
				}

			case res := <-resCh:
				atomic.AddInt32(&active, -1)
				// 父请求取消拥有最高归属优先级。即使候选取消结果与 ctx.Done
				// 同时就绪，也不能把客户端断开误报成节点失败或 all nodes failed。
				if parentErr := ctx.Err(); parentErr != nil {
					timer.Stop()
					cancel()
					return zero, parentErr
				}
				name := nodes.GetNodeName(res.uri)

				if res.err == nil {
					// 判定是否可立即胜出。
					if rc.isWinningResult == nil || rc.isWinningResult(res.val) {
						timer.Stop()
						log.Printf("[Racing] 竞速胜出节点: %s", name)
						cli.UpdateReqWinner(RequestIDFromContext(ctx), name)
						cli.UpdateReqState(RequestIDFromContext(ctx), "🟢 数据传输", "\033[32m", "已建立连接")
						nodes.RecordTest(res.uri, true, 50, "")
						stickyPool.Add(res.uri)

						returnedOnWinPath = true

						cancelsMu.Lock()
						for u, cancelFn := range cancels {
							if u != res.uri {
								cancelFn()
							}
						}
						cancelsMu.Unlock()

						collectTimeout := time.Duration(min(30, 5+cfg.ParallelPoolSize())) * time.Second
						go func() {
							collectCtx, collectCancel := context.WithTimeout(context.Background(), collectTimeout)
							defer collectCancel()
							if atomic.LoadInt32(&active) == 0 {
								if !rc.noCancelOnSuccess {
									cancel()
								}
								return
							}
							for {
								select {
								case bgRes := <-resCh:
									atomic.AddInt32(&active, -1)
									recordResult(bgRes)
									if atomic.LoadInt32(&active) == 0 {
										if !rc.noCancelOnSuccess {
											cancel()
										}
										return
									}
								case <-collectCtx.Done():
									if !rc.noCancelOnSuccess {
										cancel()
									}
									return
								}
							}
						}()

						return res.val, nil
					}

					// 非胜出成功结果：收集（CompleteChat 非 STOP 结果），继续等其余候选。
					cancelCandidate(res.uri)
					rc.collectedResults = append(rc.collectedResults, res)
					nodes.RecordTest(res.uri, true, 50, "")
					stickyPool.Add(res.uri)
				} else {
					cancelCandidate(res.uri)
					if !errors.Is(res.err, context.Canceled) {
						if cfg.DebugMode() {
							log.Printf("[Racing] 节点 %s 失败: %s", name, res.err.Error())
						}

						ve := asVertexError(res.err)
						failedErrors = append(failedErrors, res.err)

						if ve != nil && ve.IsGlobalHardError() {
							if cfg.DebugMode() {
								log.Printf("[Racing] 节点 %s 返回请求级错误(%s)，终止竞速: %s", name, ve.Kind, ve.Message)
							}
							cancel()
							return zero, res.err
						}
						if ve != nil && ve.Kind == "ratelimit" {
							if cfg.DebugMode() {
								log.Printf("[Racing] 节点 %s 触发 429 API 限制，进入 30 秒短时歇息", name)
							}
							nodes.RecordRateLimit(res.uri, 30)
							stickyPool.Evict(res.uri)
						} else {
							nodes.RecordTest(res.uri, false, 0, res.err.Error())
							stickyPool.Evict(res.uri)
						}

					} else {
						if cfg.DebugMode() {
							log.Printf("[Racing] 节点 %s 拨号取消", name)
						}
					}
				}

				if atomic.LoadInt32(&active) == 0 && nextIdx < len(cands) {
					if cfg.DebugMode() {
						log.Printf("[Racing] 已启动候选全部结束，立即接力节点: %s", cands[nextIdx].Name)
					}
					launchNext()
					resetTimer()
					continue
				}

				if atomic.LoadInt32(&active) == 0 && nextIdx >= len(cands) {
					timer.Stop()
					if parentErr := ctx.Err(); parentErr != nil {
						cancel()
						return zero, parentErr
					}
					// 本轮全部结束：优先收敛收集到的非胜出成功结果。
					if len(rc.collectedResults) > 0 {
						cancel()
						if rc.finalizeCollected != nil {
							return rc.finalizeCollected(rc.collectedResults)
						}
						return rc.collectedResults[0].val, nil
					}

					// 本轮全部失败且无成功：换一批从未用过的节点再试（关单节点重试模式）。
					if roundBudget > 0 {
						next := selectFreshCands()
						if len(next) == 0 {
							if cfg.DebugMode() {
								log.Printf("[Racing] 新鲜节点已耗尽，清空防重过滤，允许节点跨轮次重试复用...")
							}
							usedURIs = make(map[string]bool)
							next = selectFreshCands()
						}
						if len(next) == 0 {
							cancel()
							if len(failedErrors) > 0 {
								return zero, pickBestError(failedErrors)
							}
							return zero, fmt.Errorf("all nodes failed")
						}
						roundBudget--
						cands = next
						if cfg.DebugMode() {
							log.Printf("[Racing] 本轮 %d 个节点全部失败，换批重试（剩余轮次 %d）", len(cands), roundBudget)
						}
						break InnerLoop // 进入下一轮
					}
					cancel()
					if len(failedErrors) > 0 {
						return zero, pickBestError(failedErrors)
					}
					return zero, fmt.Errorf("all nodes failed")
				}
			}
		}
	}
}
