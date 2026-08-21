package vertex

import "github.com/bsfdsagfadg/vertex/internal/engine/recaptcha"

// SetBatchGraphqlURL 覆盖 batchGraphql 端点（测试专用）；传空串恢复动态计算。
func (c *VertexAIClient) SetBatchGraphqlURL(url string) {
	c.batchURL = url
}

// SetTokenPool replaces the token pool for testing.
// Will be replaced by dependency injection in phase 3/4.
func (c *VertexAIClient) SetTokenPool(pool *recaptcha.TokenPool) {
	c.pool = pool
}
