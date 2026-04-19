// Package tool の正準 interface / 型は port/tool/ にある。
// 本 file は呼び出し側の import path 温存のための互換 shim。
package tool

import port "github.com/haryoiro/suzuha/internal/port/tool"

// port/tool への型エイリアス群。
type (
	Tool         = port.Tool
	ReadOnlyTool = port.ReadOnlyTool
	ToolResult   = port.ToolResult
	Content      = port.Content
)

// Helper の再エクスポート。Go は関数エイリアスを直接サポートしないので変数束縛で代用。
var (
	IsReadOnly  = port.IsReadOnly
	TextResult  = port.TextResult
	StopResult  = port.StopResult
	ErrorResult = port.ErrorResult
)
