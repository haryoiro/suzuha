package mcpapps

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

const defaultRegistryURL = "https://registry.modelcontextprotocol.io/v0.1"

// RegistryClient queries the official MCP Registry API.
type RegistryClient struct {
	baseURL string
	client  *http.Client
}

// NewRegistryClient creates a client for the MCP Registry.
func NewRegistryClient() *RegistryClient {
	return &RegistryClient{
		baseURL: defaultRegistryURL,
		client:  &http.Client{},
	}
}

// SearchResult is the response from the registry search endpoint.
type SearchResult struct {
	Servers  []ServerResponse `json:"servers"`
	Metadata struct {
		NextCursor string `json:"nextCursor"`
		Count      int    `json:"count"`
	} `json:"metadata"`
}

// ServerResponse wraps a server with registry metadata.
type ServerResponse struct {
	Server ServerJSON `json:"server"`
}

// ServerJSON describes an MCP server in the registry.
type ServerJSON struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Title       string    `json:"title"`
	Version     string    `json:"version"`
	Packages    []Package `json:"packages"`
	Repository  *struct {
		URL string `json:"url"`
	} `json:"repository,omitempty"`
}

// Package describes how to install and run an MCP server.
type Package struct {
	RegistryType         string                `json:"registryType"` // npm, pypi, oci
	Identifier           string                `json:"identifier"`
	Version              string                `json:"version"`
	RuntimeHint          string                `json:"runtimeHint"` // npx, uvx, docker
	Transport            PackageTransport      `json:"transport"`
	PackageArguments     []PackageArgument     `json:"packageArguments"`
	EnvironmentVariables []EnvironmentVariable `json:"environmentVariables"`
}

// PackageTransport defines the communication protocol.
type PackageTransport struct {
	Type string `json:"type"` // stdio, sse, streamable-http
}

// PackageArgument represents a CLI argument for the MCP server.
type PackageArgument struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	IsRequired  bool   `json:"isRequired"`
	Default     string `json:"default"`
	Type        string `json:"type"` // named, positional
	Value       string `json:"value"`
}

// EnvironmentVariable represents an env var the MCP server needs.
type EnvironmentVariable struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	IsRequired  bool   `json:"isRequired"`
	Value       string `json:"value"`
}

// Search queries the MCP Registry for servers matching the query.
func (c *RegistryClient) Search(ctx context.Context, query string, limit int) (*SearchResult, error) {
	if limit <= 0 || limit > 20 {
		limit = 10
	}

	u, _ := url.Parse(c.baseURL + "/servers")
	q := u.Query()
	if query != "" {
		q.Set("search", query)
	}
	q.Set("limit", strconv.Itoa(limit))
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("リクエストの作成に失敗: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("レジストリリクエストに失敗: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("レジストリがステータス %d を返しました", resp.StatusCode)
	}

	var result SearchResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("レスポンスのデコードに失敗: %w", err)
	}
	return &result, nil
}

// GetServer fetches details for a specific server from the registry.
func (c *RegistryClient) GetServer(ctx context.Context, name string) (*ServerJSON, error) {
	u := c.baseURL + "/servers/" + url.PathEscape(name) + "/versions/latest"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("リクエストの作成に失敗: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("レジストリリクエストに失敗: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("サーバー %q がレジストリに見つかりません", name)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("レジストリがステータス %d を返しました", resp.StatusCode)
	}

	var sr ServerResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, fmt.Errorf("レスポンスのデコードに失敗: %w", err)
	}
	return &sr.Server, nil
}
