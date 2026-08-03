package controller

import (
	"fmt"
	"io"
	"mime/multipart"
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
	lines.GET("/metrics", a.listLineMetrics)
	lines.GET("/diagnostics", a.listLineDiagnostics)
	lines.GET("/:id", a.getLine)
	lines.GET("/:id/metrics", a.getLineMetrics)
	lines.POST("", a.createLine)
	lines.POST("/prepare", a.prepareLine)
	lines.POST("/:id/origin-certificate", a.uploadOriginCertificate)
	lines.POST("/:id/apply", a.applyLine)
	lines.POST("/:id/validity", a.updateLineValidity)
	lines.POST("/:id/renew", a.renewLine)
	lines.POST("/:id/check", a.checkLine)
	lines.POST("/:id/delete", a.deleteLine)
	lines.POST("/batch-delete", a.batchDeleteLines)
	lines.GET("/:id/share", a.shareLine)
	lines.GET("/:id/clash-subscription", a.getClashSubscription)
	lines.GET("/:id/clash-subscription/yaml", a.downloadClashSubscription)
	lines.POST("/:id/clash-subscription/reset", a.resetClashSubscription)
	lines.POST("/:id", a.updateLine)
}

const maxOriginCertificateUploadSize = 2*1024*1024 + 64*1024

func (a *LineController) uploadOriginCertificate(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		pureJsonMsg(c, http.StatusOK, false, "Invalid line ID")
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxOriginCertificateUploadSize)
	certificateFile, err := c.FormFile("certificate")
	if err != nil {
		jsonMsg(c, "Upload origin certificate", fmt.Errorf("certificate file is required and must be smaller than 1 MiB"))
		return
	}
	privateKeyFile, err := c.FormFile("privateKey")
	if err != nil {
		jsonMsg(c, "Upload origin certificate", fmt.Errorf("private key file is required and must be smaller than 1 MiB"))
		return
	}
	if certificateFile.Size <= 0 || certificateFile.Size > 1024*1024 || privateKeyFile.Size <= 0 || privateKeyFile.Size > 1024*1024 {
		jsonMsg(c, "Upload origin certificate", fmt.Errorf("each file must be smaller than 1 MiB"))
		return
	}

	certificate, err := readMultipartFile(certificateFile)
	if err != nil {
		jsonMsg(c, "Upload origin certificate", err)
		return
	}
	privateKey, err := readMultipartFile(privateKeyFile)
	if err != nil {
		jsonMsg(c, "Upload origin certificate", err)
		return
	}

	result, err := a.lineService.StageCloudflareOriginCertificate(id, certificate, privateKey)
	jsonMsgObj(c, "Origin certificate uploaded", result, err)
}

func readMultipartFile(fileHeader *multipart.FileHeader) ([]byte, error) {
	file, err := fileHeader.Open()
	if err != nil {
		return nil, fmt.Errorf("open upload: %w", err)
	}
	defer file.Close()
	content, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("read upload: %w", err)
	}
	return content, nil
}

func (a *LineController) getLineTypes(c *gin.Context) {
	jsonObj(c, a.lineService.GetLineTypes(), nil)
}

func (a *LineController) listLines(c *gin.Context) {
	lines, err := a.lineService.ListLines()
	jsonObj(c, lines, err)
}

func (a *LineController) listLineMetrics(c *gin.Context) {
	metrics, err := a.lineService.ListLineMetrics()
	jsonObj(c, metrics, err)
}

func (a *LineController) listLineDiagnostics(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "25"))
	lineID, _ := strconv.Atoi(c.Query("lineId"))
	result, err := a.lineService.ListLineDiagnostics(service.LineDiagnosticsQuery{
		Page:     page,
		PageSize: pageSize,
		LineID:   lineID,
		Kind:     c.Query("kind"),
		Level:    c.Query("level"),
	})
	jsonObj(c, result, err)
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

func (a *LineController) getLineMetrics(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		pureJsonMsg(c, http.StatusOK, false, "线路 ID 不正确")
		return
	}

	metrics, err := a.lineService.GetLineMetrics(id)
	jsonObj(c, metrics, err)
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

func (a *LineController) updateLineValidity(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		pureJsonMsg(c, http.StatusOK, false, "Invalid line ID")
		return
	}
	var req service.LineValidityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonMsg(c, "Update line validity", err)
		return
	}
	line, err := a.lineService.UpdateLineValidity(id, req)
	jsonMsgObj(c, "Line validity updated", line, err)
}

func (a *LineController) renewLine(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		pureJsonMsg(c, http.StatusOK, false, "Invalid line ID")
		return
	}
	var req service.LineValidityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonMsg(c, "Renew line", err)
		return
	}
	if _, err := a.lineService.RenewLine(id, req); err != nil {
		jsonMsgObj(c, "Renew line", nil, err)
		return
	}
	line, err := a.lineService.ApplyLine(id)
	if err == nil {
		err = a.xrayService.RestartXray(false)
	}
	jsonMsgObj(c, "Line renewed and re-enabled", line, err)
}

func (a *LineController) checkLine(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		pureJsonMsg(c, http.StatusOK, false, "线路 ID 不正确")
		return
	}

	var req struct {
		InboundLatencyMs int64 `json:"inboundLatencyMs"`
	}
	_ = c.ShouldBindJSON(&req)

	result, err := a.lineService.CheckLineWithInboundLatency(id, req.InboundLatencyMs)
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

func (a *LineController) getClashSubscription(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		pureJsonMsg(c, http.StatusOK, false, "Invalid line ID")
		return
	}
	share, err := a.lineService.GetLineClashSubscriptionShare(id)
	jsonMsgObj(c, "Clash subscription ready", share, err)
}

func (a *LineController) resetClashSubscription(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		pureJsonMsg(c, http.StatusOK, false, "Invalid line ID")
		return
	}
	share, err := a.lineService.ResetLineClashSubscription(id)
	jsonMsgObj(c, "Clash subscription reset", share, err)
}

func (a *LineController) downloadClashSubscription(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		pureJsonMsg(c, http.StatusOK, false, "Invalid line ID")
		return
	}
	body, filename, err := a.lineService.GetLineClashSubscriptionYAML(id)
	if err != nil {
		jsonMsg(c, "Download Clash subscription", err)
		return
	}
	c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
	c.Header("Pragma", "no-cache")
	c.Header("Expires", "0")
	c.Header("Content-Disposition", "attachment; filename="+filename)
	c.Data(http.StatusOK, "application/yaml; charset=utf-8", body)
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
	if req.Type != service.LineTypeCloudflare && req.Type != service.LineTypeBunny && req.Type != service.LineTypeReality {
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
	case "bunny_ws_tls":
		resp.Name = "Bunny CDN WS"
		resp.EntryPort = 443
		resp.ChainText = "用户 -> Bunny CDN -> Nginx 源站 -> Xray 本地入站 -> 住宅出口"
	case "reality_direct":
		resp.Name = "Reality 直连"
		resp.ChainText = "用户 -> VPS Reality:443 -> SOCKS5/HTTP/HTTPS 住宅出口"
	}

	jsonObj(c, resp, nil)
}
