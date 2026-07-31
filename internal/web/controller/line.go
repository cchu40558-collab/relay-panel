package controller

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/mhsanaei/3x-ui/v3/internal/web/service"

	"github.com/gin-gonic/gin"
)

// LineController exposes the simplified line-deployment API.
type LineController struct {
	lineService service.LineService
	xrayService service.XrayService
}

// NewLineController creates a new LineController and sets up its routes.
func NewLineController(g *gin.RouterGroup) *LineController {
	a := &LineController{}
	a.initRouter(g)
	return a
}

func (a *LineController) initRouter(g *gin.RouterGroup) {
	g.GET("/line-types", a.getLineTypes)
	g.GET("/lines", a.listLines)
	g.GET("/lines/", a.listLines)

	lines := g.Group("/lines")
	lines.GET("/:id", a.getLine)
	lines.POST("", a.createLine)
	lines.POST("/prepare", a.prepareLine)
	lines.POST("/:id/apply", a.applyLine)
	lines.POST("/:id/check", a.checkLine)
	lines.POST("/:id/delete", a.deleteLine)
	lines.POST("/batch-delete", a.batchDeleteLines)
	lines.GET("/:id/share", a.shareLine)
	lines.POST("/:id", a.updateLine)
}

func (a *LineController) getLineTypes(c *gin.Context) {
	jsonObj(c, a.lineService.GetLineTypes(), nil)
}

func (a *LineController) listLines(c *gin.Context) {
	lines, err := a.lineService.ListLines()
	jsonObj(c, lines, err)
}

func (a *LineController) getLine(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		pureJsonMsg(c, http.StatusOK, false, "线路 ID 不正确")
		return
	}

	line, err := a.lineService.GetLine(id)
	jsonObj(c, line, err)
}

func (a *LineController) createLine(c *gin.Context) {
	var req service.LineSaveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonMsg(c, "保存线路", err)
		return
	}

	line, err := a.lineService.CreateLine(req)
	jsonMsgObj(c, "线路草稿已保存", line, err)
}

func (a *LineController) updateLine(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		pureJsonMsg(c, http.StatusOK, false, "线路 ID 不正确")
		return
	}

	var req service.LineSaveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonMsg(c, "保存线路", err)
		return
	}

	line, err := a.lineService.UpdateLine(id, req)
	jsonMsgObj(c, "线路草稿已保存", line, err)
}

func (a *LineController) applyLine(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		pureJsonMsg(c, http.StatusOK, false, "线路 ID 不正确")
		return
	}

	line, err := a.lineService.ApplyLine(id)
	if err == nil && line != nil && line.Status == "pending_check" {
		a.xrayService.SetToNeedRestart()
	}
	jsonMsgObj(c, "线路配置已写入，Xray 将自动重载", line, err)
}

func (a *LineController) checkLine(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		pureJsonMsg(c, http.StatusOK, false, "线路 ID 不正确")
		return
	}

	result, err := a.lineService.CheckLine(id)
	jsonMsgObj(c, "线路检测完成", result, err)
}

func (a *LineController) shareLine(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		pureJsonMsg(c, http.StatusOK, false, "线路 ID 不正确")
		return
	}

	share, err := a.lineService.GetLineShare(id)
	jsonMsgObj(c, "分享链接已生成", share, err)
}

func (a *LineController) deleteLine(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		pureJsonMsg(c, http.StatusOK, false, "线路 ID 不正确")
		return
	}
	result, err := a.lineService.DeleteLine(id)
	if err == nil && result != nil && result.Success {
		a.xrayService.SetToNeedRestart()
	}
	jsonMsgObj(c, "线路已删除", result, err)
}

type batchDeleteLinesRequest struct {
	IDs []int `json:"ids"`
}

func (a *LineController) batchDeleteLines(c *gin.Context) {
	var req batchDeleteLinesRequest
	if err := c.ShouldBindJSON(&req); err != nil || len(req.IDs) == 0 {
		pureJsonMsg(c, http.StatusOK, false, "请选择要删除的线路")
		return
	}
	results := a.lineService.DeleteLines(req.IDs)
	successCount := 0
	for _, result := range results {
		if result.Success {
			successCount++
		}
	}
	if successCount > 0 {
		a.xrayService.SetToNeedRestart()
	}
	if successCount != len(results) {
		jsonMsgObj(c, "部分线路删除失败", results, fmt.Errorf("%d/%d lines deleted", successCount, len(results)))
		return
	}
	jsonMsgObj(c, "线路已删除", results, nil)
}

type prepareLineRequest struct {
	Type string `json:"type"`
}

type prepareLineResponse struct {
	Type         string `json:"type"`
	Name         string `json:"name"`
	EntryPort    int    `json:"entryPort"`
	OutboundType string `json:"outboundType"`
	ChainText    string `json:"chainText"`
}

func (a *LineController) prepareLine(c *gin.Context) {
	var req prepareLineRequest
	_ = c.ShouldBindJSON(&req)
	if req.Type != service.LineTypeCloudflare && req.Type != service.LineTypeReality {
		req.Type = service.LineTypeCloudflare
	}

	resp := prepareLineResponse{
		Type:         req.Type,
		Name:         "新线路",
		EntryPort:    443,
		OutboundType: "socks5",
		ChainText:    "用户 -> VPS -> 住宅出口",
	}
	switch req.Type {
	case "cloudflare_ws_tls":
		resp.Name = "Cloudflare 主线路"
		resp.EntryPort = 8443
		resp.ChainText = "用户 -> Cloudflare -> Nginx:8443 -> Xray 本地入站 -> SOCKS5/HTTP/HTTPS 住宅出口"
	case "reality_direct":
		resp.Name = "Reality 直连"
		resp.ChainText = "用户 -> VPS Reality:443 -> SOCKS5/HTTP/HTTPS 住宅出口"
	}

	jsonObj(c, resp, nil)
}
