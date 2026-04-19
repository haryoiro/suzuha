package control

import (
	"github.com/haryoiro/suzuha/internal/api/control/gen"
	"github.com/samber/do/v2"
)

// Handler は gen.Handler を実装する統合ハンドラ。
// 各リソースの実装は {name}_handler.go に分離されており、ここでは
// embed による合成のみ行う。
type Handler struct {
	gen.RuntimeHandler
	gen.AgentHandler
	gen.SchedulerHandler
	gen.VoicevoxHandler
	gen.ToolsHandler
	gen.LLMHandler
	gen.DeviceHandler
}

// NewHandler は injector から各 sub-handler を取り出して合成する。
func NewHandler(i do.Injector) gen.Handler {
	return &Handler{
		RuntimeHandler:   do.MustInvoke[gen.RuntimeHandler](i),
		AgentHandler:     do.MustInvoke[gen.AgentHandler](i),
		SchedulerHandler: do.MustInvoke[gen.SchedulerHandler](i),
		VoicevoxHandler:  do.MustInvoke[gen.VoicevoxHandler](i),
		ToolsHandler:     do.MustInvoke[gen.ToolsHandler](i),
		LLMHandler:       do.MustInvoke[gen.LLMHandler](i),
		DeviceHandler:    do.MustInvoke[gen.DeviceHandler](i),
	}
}
