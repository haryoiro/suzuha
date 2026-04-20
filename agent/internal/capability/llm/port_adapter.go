package llm

import (
	"context"

	portllm "github.com/haryoiro/suzuha/internal/port/llm"
)

// portClientAdapter は *Client を port/llm.Client 契約に適合させる。
// *Client.For / WithCapability の戻り値は concrete *RoleClient だが、port 経由の
// consumer は interface を期待するため wrapper で adapt する。
type portClientAdapter struct {
	c *Client
}

// For は指定ロールの RoleClient を返す。
func (p *portClientAdapter) For(role string) portllm.RoleClient {
	return p.c.For(role)
}

// WithCapability は指定ロール+capability を持つ RoleClient を返す。
func (p *portClientAdapter) WithCapability(role, cap string) (portllm.RoleClient, bool) {
	rc, ok := p.c.WithCapability(role, cap)
	if !ok {
		return nil, false
	}
	return rc, true
}

// DescribeImage は画像 URL を vision capable な LLM に説明させる。
func (p *portClientAdapter) DescribeImage(ctx context.Context, imageURL string, prompts ...string) (string, error) {
	return p.c.DescribeImage(ctx, imageURL, prompts...)
}

// AsPortClient は *Client を port/llm.Client として扱えるように wrap する。
func (c *Client) AsPortClient() portllm.Client {
	return &portClientAdapter{c: c}
}

var _ portllm.Client = (*portClientAdapter)(nil)
var _ portllm.RoleClient = (*RoleClient)(nil)
