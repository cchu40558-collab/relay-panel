package controller

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// LineSubscriptionController exposes only the opaque-token download endpoint.
// It intentionally lives outside the authenticated panel API group.
type LineSubscriptionController struct {
	lineService service.LineService
}

func NewLineSubscriptionController(g *gin.RouterGroup) *LineSubscriptionController {
	a := &LineSubscriptionController{}
	g.GET("/rp/sub/:token", a.download)
	return a
}

func (a *LineSubscriptionController) download(c *gin.Context) {
	body, filename, err := a.lineService.GetPublicLineClashSubscription(c.Param("token"))
	if err != nil {
		// A uniform 404 keeps token validity and line lifecycle private.
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
	c.Header("Pragma", "no-cache")
	c.Header("Expires", "0")
	c.Header("Content-Disposition", "attachment; filename="+filename)
	c.Data(http.StatusOK, "application/yaml; charset=utf-8", body)
}
