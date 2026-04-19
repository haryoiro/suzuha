// Package chat は port/chat への互換 shim。
// 正準定義は port/chat/ にあり、本 file は既存呼び出し側の import path を
// 温存するための暫定 alias のみを持つ。callers 移行完了後に削除予定。
package chat

import port "github.com/haryoiro/suzuha/internal/port/chat"

// port/chat への型エイリアス群 (legacy 名保持)。
type (
	Sender       = port.Sender
	Interface    = port.Interface
	Replier      = port.Replier
	IDSender     = port.IDSender
	Typer        = port.Typer
	VoiceSpeaker = port.VoiceSpeaker
)
