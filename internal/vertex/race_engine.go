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
	noCancelOnSuccess   bool
	failFastOnHardError bool
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

func WithNoCancelOnSuccess[T any]() RaceOption[T] {
	return func(cfg *raceConfig[T]) {
		cfg.noCancelOnSuccess = true
	}
}

// WithWinningCheck 注入成功判定：fn 返回 true 时该结果立即胜出；
// fn 返回 false 时结果被收集，所有候选结束后通过 pickBestResult 选出最佳结果。
func WithWinningCheck[T any](fn func(T) bool) RaceOption[T] {
	return func(cfg *raceConfig[T]) {
		cfg.isWinningResult = fn
	}
}

// WithFailFastOnHardError 使 RunRace 在收到不可重试硬错误时立即终止竞速（流式首帧默认行为）。
// 不注入时，硬错误只记录节点失败、继续等待其他候选（CompleteChat 语义）。
func WithFailFastOnHardError[T any]() RaceOption[T] {
	return func(cfg *raceConfig[T]) {
		cfg.failFastOnHardError = true
	}
}

// WithCollectedFinalizer 注入最终结果选择函数，在收集多个非胜出结果后调用。
// 默认行为：返回收集到的第一个结果。
func WithCollectedFinalizer[T any](fn func([]raceResult[T]) (T, error)) RaceOption[T] {
	return func(cfg *raceConfig[T]) {
		cfg.finalizeCollected = fn
	}
}

func safeResetTimer(t *time.Timer, d time.Duration) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
	t.Reset(d)
}

type raceResult[T any] struct {
	uri string
	val T
	err error
}

// errorPriority 返回错误的优先级数值（越小优先级越高）。
func errorPriority(err error) int {
	var ve *VertexError
	if errors.As(err, &ve) {
		if ve.IsGlobalHardError() {
			return 1
		}
		if ve.Kind == "ratelimit" || ve.Code == 429 {
			return 2
		}
		if ve.Code >= 500 && ve.Code < 600 {
			return 3
		}
	}
	return 4
}

// pickBestError 从多个错误中挑选优先级最高（数值最小）的一个返回。
// 若相同优先级，返回第一个遇到的。
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

// RunRace runs a hedge race across multiple candidate nodes.
//
// It handles:
//   - sticky pool acquisition (when enabled)
//   - node selection via SelectForParallel
//   - sticky pool filtering (enabled: exclude sticky URIs; disabled: prepend sticky URIs as priority)
//   - fallback to single node when pool is disabled or no candidates
//   - hedge timer with static/dynamic delay
//   - per-candidate cancellation: each launched goroutine gets an independent cancelable
//     context so losers can be cancelled immediately when a winner is found
//   - result collection configurable via WithWinningCheck:
//   - default (nil check): first nil-error result wins immediately
//   - with check: fn returning true → immediate win; fn returning false → collect for later pick
//   - background collection: when noCancelOnSuccess and winning check not used,
//     remaining results still update sticky pool (30s timeout)
//   - error classification: 429 → RecordRateLimit, others → RecordTest(ok=false)
//   - hard error (non-retryable) terminates the race early
//   - context.Canceled/DeadlineExceeded errors are not counted as failures;
//     when all active return context errors, the race terminates immediately
//   - ctx.Done() always returns ctx error, even if non-winning results were collected
func RunRace[T any](ctx context.Context, cfg config.ConfigProvider,
	run func(ctx context.Context, proxyURI string) (T, error),
	opts ...RaceOption[T],
) (T, error) {
	var rc raceConfig[T]
	for _, o := range opts {
		o(&rc)
	}

	stickyPool := nodes.GetStickyPool()

	cands := nodes.SelectForParallel(cfg.ParallelPoolSize(), cfg.ParallelNodeTopK(), cfg.DebugMode(), cfg.StickyNodePriority())

	if !cfg.ParallelPoolEnabled() || len(cands) == 0 {
		proxy := cfg.ActiveNodeURI()
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

	ctxRace, cancel := context.WithCancel(ctx)
	var returnedOnWinPath bool
	defer func() {
		if !returnedOnWinPath || !rc.noCancelOnSuccess {
			cancel()
		}
	}()

	resCh := make(chan raceResult[T], len(cands))
	var active int32
	activeKeys := make(map[string]bool)
	var mu sync.Mutex

	// candidateCancels 存储每个候选的独立取消函数，用于胜出后立即取消落败者。
	candidateCancels := make(map[string]context.CancelFunc)

	launchNode := func(uri string) {
		mu.Lock()
		if activeKeys[uri] {
			mu.Unlock()
			return
		}
		activeKeys[uri] = true
		mu.Unlock()

		atomic.AddInt32(&active, 1)
		// 每个候选获得一个从 ctxRace 派生的独立 context。
		candCtx, candCancel := context.WithCancel(ctxRace)
		mu.Lock()
		candidateCancels[uri] = candCancel
		mu.Unlock()

		go func(u string) {
			v, err := run(candCtx, u)
			select {
			case resCh <- raceResult[T]{u, v, err}:
			case <-ctxRace.Done():
			}
		}(uri)
	}

	cancelCandidate := func(uri string) {
		mu.Lock()
		if c, ok := candidateCancels[uri]; ok {
			c()
		}
		mu.Unlock()
	}

	var delay time.Duration
	var nextIdx int
	var timer *time.Timer

	if !cfg.ParallelPoolDelayDynamic() {
		for _, cand := range cands {
			launchNode(cand.RawURI)
		}
		nextIdx = len(cands)
		timer = time.NewTimer(0)
		if !timer.Stop() {
			<-timer.C
		}
	} else {
		launchNode(cands[0].RawURI)
		delay = time.Duration(nodes.GetAverageLatency()) * time.Millisecond
		timer = time.NewTimer(delay)
		nextIdx = 1
	}
	defer timer.Stop()
	var zero T
	var failedErrors []error

	for {
		select {
		case <-ctx.Done():
			cancel()
			return zero, ctx.Err()

		case <-timer.C:
			if nextIdx < len(cands) && ctxRace.Err() == nil {
				if cfg.DebugMode() {
					log.Printf("[Racing] 对冲延迟唤醒，启动备份节点: %s", cands[nextIdx].Name)
				}
				launchNode(cands[nextIdx].RawURI)
				nextIdx++
				safeResetTimer(timer, delay)
			}

		case res := <-resCh:
			atomic.AddInt32(&active, -1)
			name := nodes.GetNodeName(res.uri)

			if res.err == nil {
				// 判定是否可立即胜出。
				if rc.isWinningResult == nil || rc.isWinningResult(res.val) {
					log.Printf("[Racing] 竞速胜出节点: %s", name)
					cli.UpdateReqWinner(RequestIDFromContext(ctx), name)
					cli.UpdateReqState(RequestIDFromContext(ctx), "🟢 数据传输", "\033[32m", "已建立连接")
					nodes.RecordTest(res.uri, true, 50, "")
					stickyPool.Add(res.uri)

					returnedOnWinPath = true

					// 立即取消所有落败候选。
					for _, c := range cands {
						if c.RawURI != res.uri {
							cancelCandidate(c.RawURI)
						}
					}

					if !rc.noCancelOnSuccess {
						// 非流式：胜出即结束，不启动后台收集。
						return res.val, nil
					}

					// 流式 WithNoCancelOnSuccess：启动后台收集，胜出候选继续。
					// 收集所有已启动落败候选的结果，完成后主动退出。
					// 使用 30 秒兜底超时，防止候选忽略 context 取消时 goroutine 泄漏。
					collectCtx, collectCancel := context.WithTimeout(ctxRace, 30*time.Second)
					go func() {
						defer collectCancel()
						defer func() {
							if testHookCollectorDone != nil {
								close(testHookCollectorDone)
							}
						}()
						// 如果没有已启动但未报告的候选，直接退出。
						if atomic.LoadInt32(&active) == 0 {
							return
						}
						for {
							select {
							case bgRes, ok := <-resCh:
								if !ok {
									return
								}
								atomic.AddInt32(&active, -1)
								// 忽略 context 取消/超时错误，不更新节点状态。
								if bgRes.err == nil {
									stickyPool.Add(bgRes.uri)
									nodes.RecordTest(bgRes.uri, true, 50, "")
								} else if !errors.Is(bgRes.err, context.Canceled) && !errors.Is(bgRes.err, context.DeadlineExceeded) {
									ve := asVertexError(bgRes.err)
									if ve != nil && ve.Kind == "ratelimit" {
										nodes.RecordRateLimit(bgRes.uri, 30)
									} else {
										nodes.RecordTest(bgRes.uri, false, 0, bgRes.err.Error())
									}
									stickyPool.Evict(bgRes.uri)
								}
								if atomic.LoadInt32(&active) == 0 {
									return
								}
							case <-ctxRace.Done():
								return
							case <-collectCtx.Done():
								return
							}
						}
					}()

					return res.val, nil
				}

				// isWinningResult 返回 false：非 STOP 结果，收集。
				rc.collectedResults = append(rc.collectedResults, res)
				nodes.RecordTest(res.uri, true, 50, "")
				stickyPool.Add(res.uri)

				if nextIdx < len(cands) && ctxRace.Err() == nil {
					launchNode(cands[nextIdx].RawURI)
					nextIdx++
					safeResetTimer(timer, delay)
				}
			} else {
				if !errors.Is(res.err, context.Canceled) && !errors.Is(res.err, context.DeadlineExceeded) {
					if cfg.DebugMode() {
						log.Printf("[Racing] 节点 %s 失败: %s", name, res.err.Error())
					}

					failedErrors = append(failedErrors, res.err)

					ve := asVertexError(res.err)
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

					if rc.failFastOnHardError && ve != nil && ve.IsGlobalHardError() {
						if cfg.DebugMode() {
							log.Printf("[Racing] 节点 %s 触发请求级全局硬错误(%s)，终止竞速: %s", name, ve.Kind, ve.Message)
						}
						cancel()
						return zero, res.err
					}

					if nextIdx < len(cands) && ctxRace.Err() == nil {
						if cfg.DebugMode() {
							log.Printf("[Racing] 竞速失败触发极速对冲接力...")
						}
						launchNode(cands[nextIdx].RawURI)
						nextIdx++
						safeResetTimer(timer, delay)
					}
				} else {
					if cfg.DebugMode() {
						log.Printf("[Racing] 节点 %s 拨号取消", name)
					}
					if atomic.LoadInt32(&active) == 0 {
						cancel()
						return zero, res.err
					}
				}
			}

			if atomic.LoadInt32(&active) == 0 && (nextIdx >= len(cands) || ctxRace.Err() != nil) {
				cancel()
				if len(rc.collectedResults) > 0 {
					if rc.finalizeCollected != nil {
						return rc.finalizeCollected(rc.collectedResults)
					}
					return rc.collectedResults[0].val, nil
				}
				if len(failedErrors) > 0 {
					return zero, pickBestError(failedErrors)
				}
				if res.err != nil {
					return zero, res.err
				}
				if err := ctxRace.Err(); err != nil {
					return zero, err
				}
				return zero, fmt.Errorf("all nodes failed")
			}
		}
	}
}
