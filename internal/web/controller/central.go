package controller

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mhsanaei/3x-ui/v3/internal/config"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

const centralProtocolVersion = 1

// CentralController exposes a deliberately small, read-only contract for the
// standalone Relay Panel central site. It is not a general remote-control API.
type CentralController struct {
	BaseController
	lineService    service.LineService
	serverService  service.ServerService
	settingService service.SettingService
}

func NewCentralController(g *gin.RouterGroup) *CentralController {
	a := &CentralController{}
	central := g.Group("/central")
	central.Use(a.requireCentralReadOnly)
	central.GET("/capabilities", a.capabilities)
	central.GET("/summary", a.summary)
	central.GET("/lines", a.lines)
	return a
}

func (a *CentralController) requireCentralReadOnly(c *gin.Context) {
	if ok, _ := c.Get("central_read_only"); ok == true {
		c.Next()
		return
	}
	c.AbortWithStatus(http.StatusForbidden)
}

func (a *CentralController) capabilities(c *gin.Context) {
	nodeID, err := a.settingService.GetPanelGuid()
	if err != nil {
		jsonObj(c, nil, err)
		return
	}
	jsonObj(c, gin.H{
		"product": "relay-panel", "role": "node", "centralProtocolVersion": centralProtocolVersion,
		"panelVersion": config.GetPanelVersion(), "nodeId": nodeID, "readOnly": true,
		"features": []string{"summary", "lines"},
	}, nil)
}

func (a *CentralController) summary(c *gin.Context) {
	lines, err := a.lineService.ListLines()
	if err != nil {
		jsonObj(c, nil, err)
		return
	}
	metrics, err := a.lineService.ListLineMetrics()
	if err != nil {
		jsonObj(c, nil, err)
		return
	}
	nodeID, err := a.settingService.GetPanelGuid()
	if err != nil {
		jsonObj(c, nil, err)
		return
	}

	totalTraffic := int64(0)
	for _, metric := range metrics {
		if metric.TotalTraffic > 0 {
			totalTraffic += metric.TotalTraffic
		}
	}
	healthy, abnormal, expired := centralLineCounts(lines)
	xrayState, xrayError := a.serverService.CurrentXrayStatus()

	jsonObj(c, gin.H{
		"centralProtocolVersion": centralProtocolVersion,
		"nodeId":                 nodeID,
		"panelVersion":           config.GetPanelVersion(),
		"sampledAt":              time.Now().UTC().Format(time.RFC3339),
		"xray":                   gin.H{"state": xrayState, "error": xrayError},
		"lines":                  gin.H{"total": len(lines), "healthy": healthy, "abnormal": abnormal, "expired": expired},
		"traffic":                gin.H{"totalBytes": totalTraffic},
	}, nil)
}

func (a *CentralController) lines(c *gin.Context) {
	lines, err := a.lineService.ListLines()
	if err != nil {
		jsonObj(c, nil, err)
		return
	}
	metrics, err := a.lineService.ListLineMetrics()
	if err != nil {
		jsonObj(c, nil, err)
		return
	}
	metricByLine := make(map[int]service.LineMetrics, len(metrics))
	for _, metric := range metrics {
		metricByLine[metric.LineID] = metric
	}
	result := make([]gin.H, 0, len(lines))
	for _, line := range lines {
		metric := metricByLine[line.Id]
		result = append(result, gin.H{
			"id": line.Id, "name": line.Name, "type": line.Type, "status": line.Status,
			"validFrom": line.ValidFrom, "validUntil": line.ValidUntil,
			"manualReenableRequired": line.ManualReenableRequired, "totalTraffic": metric.TotalTraffic,
			"inboundLatencyMs": metric.InboundLatencyMs, "outboundLatencyMs": metric.OutboundLatencyMs,
			"lastCheckedAt": metric.LastCheckedAt, "lastError": line.LastError,
		})
	}
	jsonObj(c, result, nil)
}

func centralLineCounts(lines []model.LineProfile) (healthy, abnormal, expired int) {
	for _, line := range lines {
		if line.Status == "expired" {
			expired++
			continue
		}
		if line.Status == "error" || line.Status == "failed" || line.LastError != "" {
			abnormal++
			continue
		}
		healthy++
	}
	return healthy, abnormal, expired
}
