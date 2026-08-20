package vertex

import "github.com/bsfdsagfadg/vertex/internal/recaptcha"

// SetBatchGraphqlURL overrides the batchGraphql URL for testing.
// Will be replaced by dependency injection in phase 3/4.
func SetBatchGraphqlURL(url string) {
	batchGraphqlURL = url
}

// SetTokenPool replaces the token pool for testing.
// Will be replaced by dependency injection in phase 3/4.
func (c *VertexAIClient) SetTokenPool(pool *recaptcha.TokenPool) {
	c.graphMu.Lock()
	defer c.graphMu.Unlock()
	c.pool = pool
	c.graph = nil
}
