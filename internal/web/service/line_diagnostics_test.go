package service

import (
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func TestListLineDiagnosticsUsesDetailedCheckEvidence(t *testing.T) {
	setupLineValidityDB(t)
	line := &model.LineProfile{Name: "diagnostic-line", Type: LineTypeReality, ConfigJSON: `{}`}
	if err := database.GetDB().Create(line).Error; err != nil {
		t.Fatalf("create line: %v", err)
	}
	if err := database.GetDB().Create(&model.LineCheckResult{
		LineId:    line.Id,
		Status:    "failed",
		PassCount: 2,
		FailCount: 1,
		ItemsJSON: `[{"name":"Xray 入站","status":"pass","message":"line-1-in"},{"name":"Nginx 配置","status":"fail","message":"not found"}]`,
	}).Error; err != nil {
		t.Fatalf("create check result: %v", err)
	}
	if err := database.GetDB().Create(&model.LineApplyLog{LineId: line.Id, Action: "check", Level: "error", Message: "summary"}).Error; err != nil {
		t.Fatalf("create check summary log: %v", err)
	}
	if err := database.GetDB().Create(&model.LineApplyLog{LineId: line.Id, Action: "apply", Level: "info", Message: "applied", Detail: "nginx reloaded"}).Error; err != nil {
		t.Fatalf("create apply log: %v", err)
	}

	result, err := (&LineService{}).ListLineDiagnostics(LineDiagnosticsQuery{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("ListLineDiagnostics: %v", err)
	}
	if result.Total != 2 || len(result.Items) != 2 {
		t.Fatalf("events = %#v", result)
	}
	var check *LineDiagnosticEvent
	for i := range result.Items {
		if result.Items[i].Kind == "check" {
			check = &result.Items[i]
		}
	}
	if check == nil || check.Level != "error" || len(check.Items) != 2 || check.Items[1].Message != "not found" {
		t.Fatalf("check evidence = %#v", check)
	}
}
