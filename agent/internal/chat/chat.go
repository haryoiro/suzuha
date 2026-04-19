// Package chat は port/chat への互換 shim。
// 段階移行のため呼び出し側の import path を温存し、正準定義は port/chat/ にある。
// Phase 11 以降に本 package を廃止予定。
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
