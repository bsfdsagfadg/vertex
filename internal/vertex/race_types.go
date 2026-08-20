package vertex

import (
	"errors"
	"fmt"
	"time"

	"github.com/bsfdsagfadg/vertex/internal/transport"
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
	key     string
	uri     string
	route   transport.Route
	val     T
	err     error
	elapsed time.Duration
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
