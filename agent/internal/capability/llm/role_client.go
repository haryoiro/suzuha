package llm

import (
	"context"
	"fmt"

	"github.com/mozilla-ai/any-llm-go/providers"
)

// RoleClient はロールに紐づくプロバイダで補完を実行する。
// Client への参照を保持し、呼び出し時に最新の provider を解決する。
// これにより SwapRole() の変更が即座に反映される。
type RoleClient struct {
	client *Client
	role   string
}

// resolve は呼び出し時に最新の roleProvider を取得する。
func (rc *RoleClient) resolve() roleProvider {
	rc.client.mu.RLock()
	defer rc.client.mu.RUnlock()
	if rp, ok := rc.client.roles[rc.role]; ok {
		return rp
	}
	return roleProvider{}
}

// CompleteRaw はこのロールのプロバイダで completion を実行する。
func (rc *RoleClient) CompleteRaw(ctx context.Context, messages []providers.Message) (*Response, error) {
	rp := rc.resolve()
	if rp.provider == nil {
		return nil, fmt.Errorf("llm: ロール %q にプロバイダが設定されていません", rc.role)
	}
	params := providers.CompletionParams{
		Model:    rp.model,
		Messages: messages,
	}

	var resp *providers.ChatCompletion
	err := retryOnRateLimit(ctx, rc.client.logger, func() error {
		var callErr error
		resp, callErr = rp.provider.Completion(ctx, params)
		return callErr
	})
	if err != nil {
		return nil, fmt.Errorf("llm: 補完に失敗: %w", err)
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("llm: 空のレスポンス")
	}
	choice := resp.Choices[0]
	reasoning, cleaned := parseThinkTags(choice.Message.ContentString())
	r := &Response{
		Text:         cleaned,
		Reasoning:    reasoning,
		FinishReason: choice.FinishReason,
	}
	if resp.Usage != nil {
		r.Usage = *resp.Usage
	}
	return r, nil
}

// CompleteWithTools はツール定義付きで completion を実行する。
// メッセージとツールは事前に providers 形式に変換済みであること。
func (rc *RoleClient) CompleteWithTools(ctx context.Context, messages []providers.Message, tools []providers.Tool) (*Response, error) {
	rp := rc.resolve()
	if rp.provider == nil {
		return nil, fmt.Errorf("llm: ロール %q にプロバイダが設定されていません", rc.role)
	}
	params := providers.CompletionParams{
		Model:    rp.model,
		Messages: messages,
		Tools:    tools,
	}

	var resp *providers.ChatCompletion
	err := retryOnRateLimit(ctx, rc.client.logger, func() error {
		var callErr error
		resp, callErr = rp.provider.Completion(ctx, params)
		return callErr
	})
	if err != nil {
		return nil, fmt.Errorf("llm: 補完に失敗: %w", err)
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("llm: 空のレスポンス")
	}
	choice := resp.Choices[0]
	reasoning, cleaned := parseThinkTags(choice.Message.ContentString())
	r := &Response{
		Text:         cleaned,
		Reasoning:    reasoning,
		ToolCalls:    choice.Message.ToolCalls,
		FinishReason: choice.FinishReason,
	}
	if resp.Usage != nil {
		r.Usage = *resp.Usage
	}
	return r, nil
}

// HasCapability はこのロールが指定されたケイパビリティを持つかを返す。
func (rc *RoleClient) HasCapability(cap string) bool {
	rc.client.mu.RLock()
	defer rc.client.mu.RUnlock()
	if rp, ok := rc.client.roles[rc.role]; ok {
		return rp.hasCapability(cap)
	}
	return false
}

// Model はこのロールに割り当てられたモデル名を返す。
func (rc *RoleClient) Model() string {
	return rc.resolve().model
}

// ProviderName はこのロールに割り当てられたプロバイダ名を返す。
func (rc *RoleClient) ProviderName() string {
	return rc.resolve().providerName
}

// MaxContextTokens はこのロールの最大コンテキストトークン数を返す。
func (rc *RoleClient) MaxContextTokens() int {
	return rc.resolve().maxCtx
}

// roleFallbacks はロールごとのフォールバックチェーン。
// voice は conversation の変種なので background をスキップする。
var roleFallbacks = map[string][]string{
	"voice": {"voice", "conversation"},
}

// defaultRoleFallback は roleFallbacks に登録されていないロール用。
var defaultRoleFallback = []string{"background", "conversation"}

// For はロールに割り当てられたプロバイダを返す。
// フォールバック: ロール固有チェーン → デフォルト (background → conversation)。
// 返される RoleClient は Client への参照を保持し、SwapRole() の変更が即座に反映される。
func (c *Client) For(role string) *RoleClient {
	c.mu.RLock()
	defer c.mu.RUnlock()

	chain, ok := roleFallbacks[role]
	if !ok {
		chain = append([]string{role}, defaultRoleFallback...)
	}

	resolved := role
	for _, r := range chain {
		if _, ok := c.roles[r]; ok {
			resolved = r
			break
		}
	}

	return &RoleClient{client: c, role: resolved}
}

// WithCapability はロールの capability 解決を行い、RoleClient と inline フラグを返す。
// inline=true: ロールのプロバイダがネイティブ対応。
// inline=false: capability 名のロールにフォールバック。
func (c *Client) WithCapability(role, capability string) (*RoleClient, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	rp, ok := c.roles[role]
	if !ok {
		for _, r := range []string{"background", "conversation"} {
			if rp, ok = c.roles[r]; ok {
				role = r
				break
			}
		}
	}

	if ok && rp.hasCapability(capability) {
		return &RoleClient{client: c, role: role}, true
	}

	if _, ok := c.roles[capability]; ok {
		return &RoleClient{client: c, role: capability}, false
	}

	return nil, false
}
