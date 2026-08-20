package vertex

import (
	"context"
	"fmt"
	"log"

	"github.com/bsfdsagfadg/vertex/internal/cli"
	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/nodes"
)

// RunRace runs a hedged race across candidate nodes.
// It coordinates candidate scheduling, launching, and result adjudication.
func RunRace[T any](ctx context.Context, cfg config.ConfigProvider,
	run func(ctx context.Context, proxyURI string) (T, error),
	opts ...RaceOption[T],
) (T, error) {
	var rc raceConfig[T]
	for _, o := range opts {
		o(&rc)
	}

	scheduler := NewCandidateScheduler(cfg)
	cands, parallel := scheduler.SelectInitialCandidates()
	if !parallel || len(cands) == 0 {
		proxy := scheduler.FallbackProxy()
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

	ctxRace, cancelRace := context.WithCancel(ctx)
	var returnedOnWinPath bool
	defer func() {
		if !returnedOnWinPath || !rc.noCancelOnSuccess {
			cancelRace()
		}
	}()

	stickyPool := nodes.GetStickyPool()
	arbiter := NewResultArbiter[T](rc, stickyPool)
	raceTimeout := cfg.RaceTimeout()
	var zero T

	for {
		scheduler.PrepareRound(cands)
		resCh := make(chan raceResult[T], len(cands))
		launcher := NewCandidateLauncher[T](ctxRace, raceTimeout, run)

		launchNext := func() bool {
			cand, ok := scheduler.NextCandidate()
			if !ok {
				return false
			}
			launcher.Launch(cand, scheduler.CurrentRound(), resCh)
			return true
		}

		launchNext()
		if !scheduler.HasMore() {
			scheduler.StopTimer()
		}

	InnerLoop:
		for {
			select {
			case <-ctx.Done():
				scheduler.StopTimer()
				launcher.CancelAll()
				cancelRace()
				return zero, ctx.Err()

			case <-scheduler.TimerChan():
				if scheduler.HasMore() {
					if cfg.DebugMode() {
						if next, ok := scheduler.PeekNextCandidate(); ok {
							log.Printf("[Racing] 对冲延迟唤醒，启动备份节点: %s", next.Name)
						}
					}
					launchNext()
					scheduler.ResetTimer()
				}

			case res := <-resCh:
				launcher.DecrementActive()

				// 父请求取消拥有最高归属优先级。即使候选取消结果与 ctx.Done
				// 同时就绪，也不能把客户端断开误报成节点失败或 all nodes failed。
				if parentErr := ctx.Err(); parentErr != nil {
					scheduler.StopTimer()
					launcher.CancelAll()
					cancelRace()
					return zero, parentErr
				}

				name := nodes.GetNodeName(res.uri)

				if res.err == nil {
					if arbiter.IsWinning(res.val) {
						scheduler.StopTimer()
						log.Printf("[Racing] 竞速胜出节点: %s", name)
						cli.UpdateReqWinner(RequestIDFromContext(ctx), name)
						cli.UpdateReqState(RequestIDFromContext(ctx), "🟢 数据传输", "\033[32m", "已建立连接")

						arbiter.RecordWinner(res.uri)
						returnedOnWinPath = true
						launcher.CancelAllExcept(res.uri)

						arbiter.DrainBackgroundResults(launcher, resCh, cancelRace, cfg.ParallelPoolSize())
						return res.val, nil
					}

					// 非胜出成功结果（如 CompleteChat 的非 STOP 结果）：收集并继续等待
					launcher.CancelCandidate(res.uri)
					arbiter.RecordNonWinning(res)
				} else {
					launcher.CancelCandidate(res.uri)
					arbiter.RecordFailure(res)
					ve := asVertexError(res.err)

					if ve != nil && ve.IsGlobalHardError() {
						if cfg.DebugMode() {
							log.Printf("[Racing] 节点 %s 返回请求级错误(%s)，终止竞速: %s", name, ve.Kind, ve.Message)
						}
						scheduler.StopTimer()
						launcher.CancelAll()
						cancelRace()
						return zero, res.err
					}
				}

				if launcher.ActiveCount() == 0 && scheduler.HasMore() {
					if cfg.DebugMode() {
						if next, ok := scheduler.PeekNextCandidate(); ok {
							log.Printf("[Racing] 已启动候选全部结束，立即接力节点: %s", next.Name)
						}
					}
					launchNext()
					scheduler.ResetTimer()
					continue
				}

				if launcher.ActiveCount() == 0 && !scheduler.HasMore() {
					scheduler.StopTimer()
					if parentErr := ctx.Err(); parentErr != nil {
						launcher.CancelAll()
						cancelRace()
						return zero, parentErr
					}

					// 本轮全部结束：优先收敛收集到的非胜出成功结果。
					if arbiter.HasCollected() {
						cancelRace()
						return arbiter.FinalizeCollected()
					}

					// 本轮全部失败且无成功：换一批从未用过的节点再试（关单节点重试模式）。
					if nextCands, hasRound := scheduler.NextRound(); hasRound {
						cands = nextCands
						if cfg.DebugMode() {
							log.Printf("[Racing] 本轮节点全部失败，换批重试（剩余轮次 %d）", scheduler.RoundBudget())
						}
						break InnerLoop // 进入下一轮
					}

					cancelRace()
					return zero, arbiter.PickBestError()
				}
			}
		}
	}
}
