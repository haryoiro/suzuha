package llm

import (
	portllm "github.com/haryoiro/suzuha/internal/port/llm"
)

// portClientAdapter は *Client を port/llm.Client 契約に適合させる。
// *Client.For の戻り値は concrete *RoleClient だが、port 経由の consumer は
// interface を期待するため wrapper で adapt する。
type portClientAdapter struct {
	c *Client
}

// For は指定ロールの RoleClient を返す。
func (p *portClientAdapter) For(role string) portllm.RoleClient {
	return p.c.For(role)
}

// AsPortClient は *Client を port/llm.Client として扱えるように wrap する。
func (c *Client) AsPortClient() portllm.Client {
	return &portClientAdapter{c: c}
}

var _ portllm.Client = (*portClientAdapter)(nil)
var _ portllm.RoleClient = (*RoleClient)(nil)
