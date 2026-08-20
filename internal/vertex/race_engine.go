package vertex

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/cli"
	"github.com/bsfdsagfadg/vertex/internal/config"
	"github.com/bsfdsagfadg/vertex/internal/nodes"
	"github.com/bsfdsagfadg/vertex/internal/scheduler"
	"github.com/bsfdsagfadg/vertex/internal/transport"
)

// RunRace runs a hedged race across candidate nodes.
// It coordinates candidate launching, hedge scheduling, and result adjudication.
func RunRace[T any](ctx context.Context, cfg config.ConfigProvider,
	run func(ctx context.Context, proxyURI string) (T, error),
	opts ...RaceOption[T],
) (T, error) {
	return runRaceWithDependencies(ctx, cfg, run, legacyRaceDependencies(), opts...)
}

func runRaceWithDependencies[T any](ctx context.Context, cfg config.ConfigProvider,
	run func(ctx context.Context, proxyURI string) (T, error), dependencies raceDependencies,
	opts ...RaceOption[T],
) (T, error) {
	var rc raceConfig[T]
	for _, o := range opts {
		o(&rc)
	}

	stickyPool := dependencies.nodes.StickyPool()
	raceTimeout := cfg.RaceTimeout()

	// 换批预算：关单节点重试时，重试由"换一批新节点"完成（最多 MaxRetries 批）。
	roundBudget := 0
	if cfg.ParallelPoolEnabled() && !cfg.ParallelPoolRetryEnabled() {
		roundBudget = cfg.MaxRetries()
	}

	usedURIs := make(map[string]bool)
	unlaunchedReservations := make(map[string]int)
	defer func() {
		for uri, count := range unlaunchedReservations {
			for range count {
				dependencies.nodes.DecInFlight(uri)
			}
		}
	}()
	var zero T

	selectFreshCands := func() ([]scheduler.Candidate, error) {
		selected := dependencies.nodes.Select(cfg.ParallelPoolSize(), cfg.ParallelNodeTopK(), cfg.StickyNodePriority(), true)
		fresh := make([]nodes.Node, 0, len(selected))
		for _, c := range selected {
			if !usedURIs[c.RawURI] {
				fresh = append(fresh, c)
				continue
			}
			dependencies.nodes.DecInFlight(c.RawURI)
		}
		if len(fresh) == 0 {
			return nil, nil
		}
		planned, err := dependencies.planner.PlanCandidates(cfg, fresh, scheduler.MetadataFromContext(ctx))
		if err != nil {
			for _, node := range fresh {
				dependencies.nodes.DecInFlight(node.RawURI)
			}
			return nil, err
		}

		plannedCounts := make(map[string]int, len(planned))
		for i := range planned {
			planned[i].Reserved = true
			uri := planned[i].Route.RequestNodeURI
			plannedCounts[uri]++
			unlaunchedReservations[uri]++
		}
		for _, node := range fresh {
			if plannedCounts[node.RawURI] > 0 {
				plannedCounts[node.RawURI]--
				continue
			}
			dependencies.nodes.DecInFlight(node.RawURI)
		}
		return planned, nil
	}

	if !cfg.ParallelPoolEnabled() {
		node := nodes.Node{RawURI: cfg.ActiveNodeURI(), Name: dependencies.nodes.NodeName(cfg.ActiveNodeURI())}
		planned, err := dependencies.planner.PlanCandidates(cfg, []nodes.Node{node}, scheduler.MetadataFromContext(ctx))
		if err != nil {
			return zero, err
		}
		candidate := planned[0]
		log.Printf("[Vertex] [RunParallel] 降级为单节点运行: %s", dependencies.nodes.NodeName(candidate.Route.RequestNodeURI))
		routedCtx := transport.WithEntryProxy(ctx, candidate.Route.GlobalProxyURI)
		value, err := run(routedCtx, candidate.Route.RequestNodeURI)
		if err == nil && candidate.Route.GlobalProxyURI != "" {
			_ = dependencies.planner.MarkGlobalProxySuccess(candidate.Route.GlobalProxyURI)
		}
		return value, err
	}
	cands, err := selectFreshCands()
	if err != nil {
		return zero, err
	}
	if len(cands) == 0 {
		node := nodes.Node{RawURI: cfg.ActiveNodeURI(), Name: dependencies.nodes.NodeName(cfg.ActiveNodeURI())}
		planned, planErr := dependencies.planner.PlanCandidates(cfg, []nodes.Node{node}, scheduler.MetadataFromContext(ctx))
		if planErr != nil {
			return zero, planErr
		}
		candidate := planned[0]
		log.Printf("[Vertex] [RunParallel] 无并发候选，降级为单节点运行: %s", dependencies.nodes.NodeName(candidate.Route.RequestNodeURI))
		routedCtx := transport.WithEntryProxy(ctx, candidate.Route.GlobalProxyURI)
		value, runErr := run(routedCtx, candidate.Route.RequestNodeURI)
		if runErr == nil && candidate.Route.GlobalProxyURI != "" {
			_ = dependencies.planner.MarkGlobalProxySuccess(candidate.Route.GlobalProxyURI)
		}
		return value, runErr
	}
	if cfg.DebugMode() {
		log.Printf("[Vertex] [RunParallel] 开启对冲延迟竞速, %d 个节点参与", len(cands))
		for _, c := range cands {
			log.Printf("[Vertex] [RunParallel] 参与节点: %s", c.Node.Name)
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

	recorder := newResultRecorder[T](stickyPool, scheduler.MetadataFromContext(ctx), dependencies)
	var failedErrors []error

	for round := 0; ; round++ {
		resCh := make(chan raceResult[T], len(cands))
		launcher := newCandidateLauncher[T](ctxRace, raceTimeout, rc.noCancelOnSuccess, run, dependencies.nodes)
		scheduler := newHedgeScheduler(cfg, len(cands), dependencies.nodes.AverageLatency())

		launchNext := func() bool {
			if !scheduler.HasMore() {
				return false
			}
			candidate := cands[scheduler.NextIndex()]
			usedURIs[candidate.Route.RequestNodeURI] = true
			if candidate.Reserved {
				uri := candidate.Route.RequestNodeURI
				if unlaunchedReservations[uri] <= 1 {
					delete(unlaunchedReservations, uri)
				} else {
					unlaunchedReservations[uri]--
				}
			}
			launcher.Launch(candidate, round, resCh)
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
				cancel()
				return zero, ctx.Err()

			case <-scheduler.TimerChan():
				if scheduler.HasMore() {
					if cfg.DebugMode() {
						log.Printf("[Racing] 对冲延迟唤醒，启动备份节点: %s", cands[scheduler.nextIdx].Node.Name)
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
					cancel()
					return zero, parentErr
				}
				name := dependencies.nodes.NodeName(res.uri)

				if res.err == nil {
					// 判定是否可立即胜出。
					if rc.isWinningResult == nil || rc.isWinningResult(res.val) {
						scheduler.StopTimer()
						log.Printf("[Racing] 竞速胜出节点: %s", name)
						cli.UpdateReqWinner(RequestIDFromContext(ctx), name)
						cli.UpdateReqState(RequestIDFromContext(ctx), "🟢 数据传输", "\033[32m", "已建立连接")
						recorder.Record(res)

						returnedOnWinPath = true
						if res.route.GlobalProxyURI != "" {
							_ = dependencies.planner.MarkGlobalProxySuccess(res.route.GlobalProxyURI)
						}
						launcher.CancelAllExcept(res.key)

						collectTimeout := time.Duration(min(30, 5+cfg.ParallelPoolSize())) * time.Second
						go func() {
							collectCtx, collectCancel := context.WithTimeout(context.Background(), collectTimeout)
							defer collectCancel()
							if launcher.ActiveCount() == 0 {
								if !rc.noCancelOnSuccess {
									cancel()
								}
								return
							}
							for {
								select {
								case bgRes := <-resCh:
									launcher.DecrementActive()
									recorder.Record(bgRes)
									if launcher.ActiveCount() == 0 {
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
					launcher.CancelCandidate(res.key)
					rc.collectedResults = append(rc.collectedResults, res)
					recorder.Record(res)
				} else {
					launcher.CancelCandidate(res.key)
					recorder.Record(res)
					ve := asVertexError(res.err)
					failedErrors = append(failedErrors, res.err)

					if ve != nil && ve.IsGlobalHardError() {
						if cfg.DebugMode() {
							log.Printf("[Racing] 节点 %s 返回请求级错误(%s)，终止竞速: %s", name, ve.Kind, ve.Message)
						}
						cancel()
						return zero, res.err
					}
				}

				if launcher.ActiveCount() == 0 && scheduler.HasMore() {
					if cfg.DebugMode() {
						log.Printf("[Racing] 已启动候选全部结束，立即接力节点: %s", cands[scheduler.nextIdx].Node.Name)
					}
					launchNext()
					scheduler.ResetTimer()
					continue
				}

				if launcher.ActiveCount() == 0 && !scheduler.HasMore() {
					scheduler.StopTimer()
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
						next, planErr := selectFreshCands()
						if planErr != nil {
							cancel()
							return zero, planErr
						}
						if len(next) == 0 {
							if cfg.DebugMode() {
								log.Printf("[Racing] 新鲜节点已耗尽，清空防重过滤，允许节点跨轮次重试复用...")
							}
							usedURIs = make(map[string]bool)
							next, planErr = selectFreshCands()
							if planErr != nil {
								cancel()
								return zero, planErr
							}
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
