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
	preserveRaceCtxOnWin bool // 仅控制 RunRace 返回时是否 cancel 主竞速 ctx；落败候选的关停由 cancelCandidate 独立完成，与此字段无关
	failFastOnHardError  bool
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

// WithPreserveRaceCtxOnWin 使 RaceCtx 在胜出路径上不被 cancel（默认 defer cancel() 总是执行）。
// 此 flag 仅控制主竞速 context 的 defer cancel；落败候选的关停由 cancelCandidate 独立完成，与此选项无关。
// 流式路径（StreamParallel）用此选项，使胜出后保留主 ctx 让胜出节点的流继续传完。
func WithPreserveRaceCtxOnWin[T any]() RaceOption[T] {
	return func(cfg *raceConfig[T]) {
		cfg.preserveRaceCtxOnWin = true
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
// 核心原则：可重试错误优先级高于不可重试错误。
// 当任意节点返回可重试错误时，客户端可据此判断应重试，
// 而不是被不可重试错误直接中断。
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
//   - node selection via SelectForParallel
//   - fallback to single node when pool is disabled or no candidates
//   - hedge timer with static/dynamic delay
//   - per-candidate cancellation: each launched goroutine gets an independent cancelable
//     context so losers can be cancelled immediately when a winner is found
//   - result collection configurable via WithWinningCheck:
//   - default (nil check): first nil-error result wins immediately
//   - with check: fn returning true → immediate win; fn returning false → collect for later pick
//   - result collection configurable via WithWinningCheck:
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

	cands := nodes.SelectForParallel(cfg.ParallelPoolSize(), cfg.DebugMode())

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
	// returnedOnWinPath 仅在本函数胜出路径（return res.val, nil 前）被置 true，
	// 其余返回路径保持 false。
	var returnedOnWinPath bool
	defer func() {
		// 非胜出路径返回 → 总是 cancel()；
		// 胜出路径返回但 preserveRaceCtxOnWin=false → 仍 cancel()；
		// 胜出路径返回且 preserveRaceCtxOnWin=true → 不 cancel，保留主 ctxRace
		// 以让胜出节点那条流继续传完。
		//
		// 本 flag 只管主 ctxRace 的 defer cancel；落败候选始终由
		// cancelCandidate 循环独立关停，与此 flag 无关。
		if !returnedOnWinPath || !rc.preserveRaceCtxOnWin {
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

		nodes.IncInFlight(uri)
		atomic.AddInt32(&active, 1)
		candCtx, candCancel := context.WithCancel(ctxRace)
		mu.Lock()
		candidateCancels[uri] = candCancel
		mu.Unlock()

		go func(u string) {
			defer nodes.DecInFlight(u)
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

					returnedOnWinPath = true

					for _, c := range cands {
						if c.RawURI != res.uri {
							cancelCandidate(c.RawURI)
						}
					}

					return res.val, nil
				}

				rc.collectedResults = append(rc.collectedResults, res)
				nodes.RecordTest(res.uri, true, 50, "")

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
					if ve != nil {
						switch ve.Kind {
						case "ratelimit":
							if cfg.DebugMode() {
								log.Printf("[Racing] 节点 %s 触发 429 API 限制，进入 30 秒短时歇息", name)
							}
							nodes.RecordRateLimit(res.uri, 30)
						case "auth":
							log.Printf("[Racing] 节点 %s 触发认证失败 (502)，即将禁用", name)
							nodes.RecordTest(res.uri, false, 0, res.err.Error())
							nodes.BatchUpdateNodesDisabled([]string{res.uri}, true)
						default:
							nodes.RecordTest(res.uri, false, 0, res.err.Error())
						}
					} else {
						nodes.RecordTest(res.uri, false, 0, res.err.Error())
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
					// 仅在尚未积累任何有效结果/错误时提前终止（保持“Context 错误不触发对冲”语义）；
					// 若已有 collectedResults 或 failedErrors，则交由下方统一终结评估点按优先级收敛。
					if atomic.LoadInt32(&active) == 0 && len(rc.collectedResults) == 0 && len(failedErrors) == 0 {
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
