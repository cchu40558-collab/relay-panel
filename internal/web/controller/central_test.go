package controller

import (
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func TestCentralLineCounts(t *testing.T) {
	healthy, abnormal, expired := centralLineCounts([]model.LineProfile{
		{Status: "normal"},
		{Status: "active"},
		{Status: "expired"},
		{Status: "error"},
		{Status: "normal", LastError: "outbound timeout"},
	})
	if healthy != 2 || abnormal != 2 || expired != 1 {
		t.Fatalf("counts = healthy=%d abnormal=%d expired=%d", healthy, abnormal, expired)
	}
}
