package job

import (
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// LineValidityJob starts scheduled lines and strictly disables expired lines.
// Expiry cleanup first disables the inbound in the database; restarting Xray
// immediately after reconciliation terminates already established sessions.
type LineValidityJob struct {
	lineService service.LineService
	xrayService service.XrayService
}

func NewLineValidityJob() *LineValidityJob {
	return new(LineValidityJob)
}

func (j *LineValidityJob) Run() {
	result, err := j.lineService.ReconcileLineValidity(time.Now())
	if err != nil {
		logger.Warning("reconcile line validity failed:", err)
		return
	}
	if !result.RestartXray {
		return
	}
	if err := j.xrayService.RestartXray(false); err != nil {
		logger.Warning("restart Xray after line validity change failed:", err)
	}
}
