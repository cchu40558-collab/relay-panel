package service

import (
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

// LineTrafficSnapshot is produced by the single Xray traffic collection job.
// It contains rates for the last collection interval, not rates inferred from
// a browser request. That makes one value authoritative for every open panel.
type LineTrafficSnapshot struct {
	LineID            int   `json:"lineId"`
	InboundSpeedUp    int64 `json:"inboundSpeedUp"`
	InboundSpeedDown  int64 `json:"inboundSpeedDown"`
	OutboundSpeedUp   int64 `json:"outboundSpeedUp"`
	OutboundSpeedDown int64 `json:"outboundSpeedDown"`
	CollectedAt       int64 `json:"collectedAt"`
}

var lineTrafficSnapshotStore = struct {
	sync.RWMutex
	lastCollectedAt time.Time
	snapshots       map[int]LineTrafficSnapshot
}{
	snapshots: make(map[int]LineTrafficSnapshot),
}

// RecordLineTrafficDeltas accepts Xray's per-poll deltas. Xray already owns
// the cumulative-stat baseline, so this function must be called only by the
// Xray traffic job and never by REST handlers.
func RecordLineTrafficDeltas(traffics []*xray.Traffic, collectedAt time.Time) []LineTrafficSnapshot {
	lineTrafficSnapshotStore.Lock()
	defer lineTrafficSnapshotStore.Unlock()

	previous := lineTrafficSnapshotStore.lastCollectedAt
	lineTrafficSnapshotStore.lastCollectedAt = collectedAt
	if previous.IsZero() || !collectedAt.After(previous) {
		return cloneLineTrafficSnapshotsLocked()
	}

	seconds := collectedAt.Sub(previous).Seconds()
	for id, snapshot := range lineTrafficSnapshotStore.snapshots {
		snapshot.InboundSpeedUp = 0
		snapshot.InboundSpeedDown = 0
		snapshot.OutboundSpeedUp = 0
		snapshot.OutboundSpeedDown = 0
		snapshot.CollectedAt = collectedAt.Unix()
		lineTrafficSnapshotStore.snapshots[id] = snapshot
	}

	for _, traffic := range traffics {
		if traffic == nil {
			continue
		}
		lineID, direction, ok := parseLineTrafficTag(traffic.Tag)
		if !ok {
			continue
		}
		snapshot := lineTrafficSnapshotStore.snapshots[lineID]
		snapshot.LineID = lineID
		snapshot.CollectedAt = collectedAt.Unix()
		if direction == "in" && traffic.IsInbound {
			snapshot.InboundSpeedUp += lineTrafficRate(traffic.Up, seconds)
			snapshot.InboundSpeedDown += lineTrafficRate(traffic.Down, seconds)
		}
		if direction == "out" && traffic.IsOutbound {
			snapshot.OutboundSpeedUp += lineTrafficRate(traffic.Up, seconds)
			snapshot.OutboundSpeedDown += lineTrafficRate(traffic.Down, seconds)
		}
		lineTrafficSnapshotStore.snapshots[lineID] = snapshot
	}

	return cloneLineTrafficSnapshotsLocked()
}

func GetLineTrafficSnapshot(lineID int) LineTrafficSnapshot {
	lineTrafficSnapshotStore.RLock()
	defer lineTrafficSnapshotStore.RUnlock()
	return lineTrafficSnapshotStore.snapshots[lineID]
}

func cloneLineTrafficSnapshotsLocked() []LineTrafficSnapshot {
	snapshots := make([]LineTrafficSnapshot, 0, len(lineTrafficSnapshotStore.snapshots))
	for _, snapshot := range lineTrafficSnapshotStore.snapshots {
		snapshots = append(snapshots, snapshot)
	}
	sort.Slice(snapshots, func(i, j int) bool { return snapshots[i].LineID < snapshots[j].LineID })
	return snapshots
}

func parseLineTrafficTag(tag string) (int, string, bool) {
	parts := strings.Split(tag, "-")
	if len(parts) != 3 || parts[0] != "line" || (parts[2] != "in" && parts[2] != "out") {
		return 0, "", false
	}
	lineID, err := strconv.Atoi(parts[1])
	if err != nil || lineID <= 0 {
		return 0, "", false
	}
	return lineID, parts[2], true
}

func lineTrafficRate(delta int64, seconds float64) int64 {
	if delta <= 0 || seconds <= 0 {
		return 0
	}
	return int64(float64(delta) / seconds)
}
