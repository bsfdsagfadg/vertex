package api

import (
	"context"

	"github.com/bsfdsagfadg/vertex/internal/nodes"
)

func (h *handler) listRequestNodes(ctx context.Context) ([]nodes.Node, map[string]*nodes.NodeHealth, error) {
	if h.nodePool != nil {
		return h.nodePool.List(ctx)
	}
	return nodes.LoadNodes(), nodes.LoadHealth(), nil
}

func (h *handler) importRequestNodes(ctx context.Context, values []nodes.Node, replace bool) error {
	if h.nodePool != nil {
		return h.nodePool.ImportManualNodes(ctx, values, replace)
	}
	return nodes.ImportManualNodes(values, replace)
}

func (h *handler) requestNodeName(uri string) string {
	if h.nodePool != nil {
		return h.nodePool.NodeName(uri)
	}
	return nodes.GetNodeName(uri)
}

func (h *handler) setRequestNodesDisabled(ctx context.Context, uris []string, disabled bool) (int, error) {
	if h.nodePool != nil {
		return h.nodePool.SetDisabled(ctx, uris, disabled)
	}
	nodes.BatchUpdateNodesDisabled(uris, disabled)
	return len(uris), nil
}

func (h *handler) recordRequestNodeTest(uri string, success bool, elapsed float64, message string) {
	if h.nodePool != nil {
		h.nodePool.RecordResult(uri, success, elapsed, message)
		return
	}
	nodes.RecordTest(uri, success, elapsed, message)
}

func (h *handler) deleteRequestNodes(ctx context.Context, uris []string) (int, error) {
	if h.nodePool != nil {
		return h.nodePool.Delete(ctx, uris)
	}
	nodes.BatchDeleteNodes(uris)
	return len(uris), nil
}

func (h *handler) requestNodeTestProgress() nodes.TestProgress {
	if h.nodePool != nil {
		return h.nodePool.TestProgress()
	}
	return nodes.GetTestProgress()
}

func (h *handler) startRequestNodeTest(total int) bool {
	if h.nodePool != nil {
		return h.nodePool.StartTest(total)
	}
	return nodes.StartTestProgress(total)
}

func (h *handler) checkRequestNodeTestControl() bool {
	if h.nodePool != nil {
		return h.nodePool.CheckTestControl()
	}
	return nodes.CheckTestControl()
}

func (h *handler) updateRequestNodeTest(name string, success bool) {
	if h.nodePool != nil {
		h.nodePool.UpdateTest(name, success)
		return
	}
	nodes.UpdateTestProgress(name, success)
}

func (h *handler) finishRequestNodeTest() {
	if h.nodePool != nil {
		h.nodePool.FinishTest()
		return
	}
	nodes.FinishTestProgress()
}

func (h *handler) pauseRequestNodeTest() {
	if h.nodePool != nil {
		h.nodePool.PauseTest()
		return
	}
	nodes.PauseTestProgress()
}

func (h *handler) resumeRequestNodeTest() {
	if h.nodePool != nil {
		h.nodePool.ResumeTest()
		return
	}
	nodes.ResumeTestProgress()
}

func (h *handler) terminateRequestNodeTest() {
	if h.nodePool != nil {
		h.nodePool.TerminateTest()
		return
	}
	nodes.TerminateTestProgress()
}
