package api

import (
	"cmp"
	"math"
	"slices"
	"time"

	"boxpilot/internal/gen/daemon"
	"boxpilot/internal/model"
	"google.golang.org/protobuf/proto"
)

const historyLimit = 30
const logLimit = 3000
const closedLimit = 1000

type cache struct {
	Snapshot
	rows        map[string]model.ConnectionRow
	logSequence uint64
}

func newCache() *cache {
	return &cache{Snapshot: Snapshot{Phase: "connecting", StreamErrors: map[string]string{}}, rows: map[string]model.ConnectionRow{}}
}
func positive(n int64) uint64 {
	if n <= 0 {
		return 0
	}
	return uint64(n)
}
func add(a, b uint64) uint64 {
	if math.MaxUint64-a < b {
		return math.MaxUint64
	}
	return a + b
}
func (s *cache) status(msg *daemon.Status) {
	s.Status = msg
	s.UploadHistory = appendHistory(s.UploadHistory, float64(positive(msg.Uplink)))
	s.DownloadHistory = appendHistory(s.DownloadHistory, float64(positive(msg.Downlink)))
}
func appendHistory(history []float64, value float64) []float64 {
	history = append(history, value)
	if len(history) > historyLimit {
		history = history[len(history)-historyLimit:]
	}
	return history
}
func (s *cache) logs(msg *daemon.Log) {
	if msg.Reset_ {
		s.Logs = nil
	}
	for _, entry := range msg.Messages {
		if entry == nil {
			continue
		}
		s.logSequence++
		s.Logs = append(s.Logs, model.LogEntry{ID: s.logSequence, Level: entry.Level, Message: entry.Message})
	}
	if len(s.Logs) > logLimit {
		s.Logs = slices.Clone(s.Logs[len(s.Logs)-logLimit:])
	}
}
func (s *cache) connections(msg *daemon.ConnectionEvents) {
	if msg.Reset_ {
		s.rows = map[string]model.ConnectionRow{}
	}
	for _, event := range msg.Events {
		if event == nil {
			continue
		}
		id := event.Id
		if id == "" && event.Connection != nil {
			id = event.Connection.Id
		}
		if id == "" {
			continue
		}
		row, exists := s.rows[id]
		switch event.Type {
		case daemon.ConnectionEventType_CONNECTION_EVENT_NEW:
			if event.Connection == nil {
				continue
			}
			row = model.ConnectionRow{Connection: event.Connection, UplinkTotal: positive(event.Connection.UplinkTotal), DownlinkTotal: positive(event.Connection.DownlinkTotal), ClosedAt: event.Connection.ClosedAt}
			if row.ClosedAt < 0 {
				row.ClosedAt = 0
			}
		case daemon.ConnectionEventType_CONNECTION_EVENT_UPDATE:
			if !exists || row.ClosedAt != 0 {
				continue
			}
			row.UplinkRate = positive(event.UplinkDelta)
			row.DownlinkRate = positive(event.DownlinkDelta)
			row.UplinkTotal = add(row.UplinkTotal, row.UplinkRate)
			row.DownlinkTotal = add(row.DownlinkTotal, row.DownlinkRate)
		case daemon.ConnectionEventType_CONNECTION_EVENT_CLOSED:
			if event.Connection != nil {
				row = model.ConnectionRow{Connection: event.Connection, UplinkTotal: positive(event.Connection.UplinkTotal), DownlinkTotal: positive(event.Connection.DownlinkTotal)}
			} else if !exists {
				continue
			}
			row.UplinkRate, row.DownlinkRate = 0, 0
			row.ClosedAt = event.ClosedAt
			if row.ClosedAt <= 0 && event.Connection != nil {
				row.ClosedAt = event.Connection.ClosedAt
			}
			if row.ClosedAt <= 0 {
				row.ClosedAt = time.Now().UnixMilli()
			}
		default:
			continue
		}
		s.rows[id] = row
	}
	type closed struct {
		id string
		at int64
	}
	var entries []closed
	for id, row := range s.rows {
		if row.ClosedAt != 0 {
			entries = append(entries, closed{id, row.ClosedAt})
		}
	}
	if len(entries) > closedLimit {
		slices.SortFunc(entries, func(a, b closed) int {
			if n := cmp.Compare(a.at, b.at); n != 0 {
				return n
			}
			return cmp.Compare(a.id, b.id)
		})
		for _, entry := range entries[:len(entries)-closedLimit] {
			delete(s.rows, entry.id)
		}
	}
}

func clone[T proto.Message](msg T) T { return proto.Clone(msg).(T) }
func cloneList[T proto.Message](messages []T) []T {
	result := make([]T, len(messages))
	for i, msg := range messages {
		result[i] = clone(msg)
	}
	return result
}

// snapshot detaches every map, slice and protobuf, so callers can retain and
// even mutate a published snapshot without racing with stream merges.
func (s *cache) snapshot() Snapshot {
	out := s.Snapshot
	if s.ServiceStatus != nil {
		out.ServiceStatus = clone(s.ServiceStatus)
	}
	if s.Status != nil {
		out.Status = clone(s.Status)
	}
	out.UploadHistory = slices.Clone(s.UploadHistory)
	out.DownloadHistory = slices.Clone(s.DownloadHistory)
	out.Groups = cloneList(s.Groups)
	out.Outbounds = cloneList(s.Outbounds)
	out.Modes = slices.Clone(s.Modes)
	out.Logs = slices.Clone(s.Logs)
	out.StreamErrors = make(map[string]string, len(s.StreamErrors))
	for k, v := range s.StreamErrors {
		out.StreamErrors[k] = v
	}
	out.Connections = make([]model.ConnectionRow, 0, len(s.rows))
	for _, row := range s.rows {
		row.Connection = clone(row.Connection)
		out.Connections = append(out.Connections, row)
	}
	// Map iteration must not make equal-date rows jump between publications.
	slices.SortFunc(out.Connections, func(a, b model.ConnectionRow) int { return cmp.Compare(a.Connection.Id, b.Connection.Id) })
	return out
}
