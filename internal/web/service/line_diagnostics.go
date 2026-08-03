package service

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// ListLineDiagnostics returns persisted check evidence and operational events.
// Check results supersede their summary log entries so a run appears once.
func (s *LineService) ListLineDiagnostics(query LineDiagnosticsQuery) (*LineDiagnosticsResponse, error) {
	if query.Page < 1 {
		query.Page = 1
	}
	if query.PageSize < 1 {
		query.PageSize = 25
	}
	if query.PageSize > 100 {
		query.PageSize = 100
	}
	query.Kind = strings.ToLower(strings.TrimSpace(query.Kind))
	query.Level = strings.ToLower(strings.TrimSpace(query.Level))

	var lines []model.LineProfile
	if err := database.GetDB().Find(&lines).Error; err != nil {
		return nil, err
	}
	lineNames := make(map[int]string, len(lines))
	for _, line := range lines {
		lineNames[line.Id] = line.Name
	}

	checkQuery := database.GetDB().Order("created_at desc")
	logQuery := database.GetDB().Where("action <> ?", "check").Order("created_at desc")
	if query.LineID > 0 {
		checkQuery = checkQuery.Where("line_id = ?", query.LineID)
		logQuery = logQuery.Where("line_id = ?", query.LineID)
	}
	var checks []model.LineCheckResult
	if err := checkQuery.Find(&checks).Error; err != nil {
		return nil, err
	}
	var logs []model.LineApplyLog
	if err := logQuery.Find(&logs).Error; err != nil {
		return nil, err
	}

	events := make([]LineDiagnosticEvent, 0, len(checks)+len(logs))
	if query.Kind == "" || query.Kind == "check" {
		for _, result := range checks {
			level := lineDiagnosticLevel(result.Status)
			if query.Level != "" && query.Level != level {
				continue
			}
			var items []LineCheckItem
			if err := json.Unmarshal([]byte(result.ItemsJSON), &items); err != nil {
				items = []LineCheckItem{{Name: "检测详情", Status: "fail", Message: fmt.Sprintf("无法读取历史检测详情: %v", err)}}
			}
			events = append(events, LineDiagnosticEvent{
				ID:        fmt.Sprintf("check-%d", result.Id),
				LineID:    result.LineId,
				LineName:  lineDiagnosticLineName(lineNames, result.LineId),
				Kind:      "check",
				Action:    "连通性检测",
				Level:     level,
				Message:   fmt.Sprintf("通过 %d，警告 %d，失败 %d", result.PassCount, result.WarnCount, result.FailCount),
				PassCount: result.PassCount,
				WarnCount: result.WarnCount,
				FailCount: result.FailCount,
				Items:     items,
				CreatedAt: result.CreatedAt,
			})
		}
	}
	if query.Kind == "" || query.Kind == "operation" {
		for _, log := range logs {
			level := normalizeLineDiagnosticLevel(log.Level)
			if query.Level != "" && query.Level != level {
				continue
			}
			events = append(events, LineDiagnosticEvent{
				ID:        fmt.Sprintf("operation-%d", log.Id),
				LineID:    log.LineId,
				LineName:  lineDiagnosticLineName(lineNames, log.LineId),
				Kind:      "operation",
				Action:    log.Action,
				Level:     level,
				Message:   log.Message,
				Detail:    log.Detail,
				CreatedAt: log.CreatedAt,
			})
		}
	}
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].CreatedAt == events[j].CreatedAt {
			return events[i].ID > events[j].ID
		}
		return events[i].CreatedAt > events[j].CreatedAt
	})
	start := (query.Page - 1) * query.PageSize
	if start >= len(events) {
		return &LineDiagnosticsResponse{Items: []LineDiagnosticEvent{}, Total: len(events)}, nil
	}
	end := start + query.PageSize
	if end > len(events) {
		end = len(events)
	}
	return &LineDiagnosticsResponse{Items: events[start:end], Total: len(events)}, nil
}

func lineDiagnosticLineName(names map[int]string, lineID int) string {
	if name := strings.TrimSpace(names[lineID]); name != "" {
		return name
	}
	return fmt.Sprintf("线路 #%d", lineID)
}

func lineDiagnosticLevel(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "failed", "apply_failed":
		return "error"
	case "warning":
		return "warning"
	default:
		return "info"
	}
}

func normalizeLineDiagnosticLevel(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "error", "warning":
		return strings.ToLower(strings.TrimSpace(level))
	default:
		return "info"
	}
}
