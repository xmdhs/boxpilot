package api

import (
	"fmt"
	"math"
	"testing"

	"boxpilot/internal/gen/daemon"
)

func TestConnectionMerge(t *testing.T) {
	s := newCache()
	apply := func(events ...*daemon.ConnectionEvent) { s.connections(&daemon.ConnectionEvents{Events: events}) }
	apply(&daemon.ConnectionEvent{Type: daemon.ConnectionEventType_CONNECTION_EVENT_UPDATE, Id: "missing", UplinkDelta: 3})
	if len(s.rows) != 0 {
		t.Fatal("update invented connection")
	}
	apply(&daemon.ConnectionEvent{Id: "a", Connection: &daemon.Connection{Id: "a", UplinkTotal: 100, DownlinkTotal: 200, Uplink: 900}})
	row := s.rows["a"]
	if row.UplinkRate != 0 || row.UplinkTotal != 100 {
		t.Fatal(row)
	}
	apply(&daemon.ConnectionEvent{Type: daemon.ConnectionEventType_CONNECTION_EVENT_UPDATE, Id: "a", UplinkDelta: 10, DownlinkDelta: 20})
	row = s.rows["a"]
	if row.UplinkRate != 10 || row.DownlinkRate != 20 || row.UplinkTotal != 110 || row.DownlinkTotal != 220 {
		t.Fatal(row)
	}
	// Rates are replaced on every update; negative deltas cannot underflow totals.
	apply(&daemon.ConnectionEvent{Type: daemon.ConnectionEventType_CONNECTION_EVENT_UPDATE, Id: "a", UplinkDelta: -1, DownlinkDelta: 2})
	row = s.rows["a"]
	if row.UplinkRate != 0 || row.DownlinkRate != 2 || row.UplinkTotal != 110 || row.DownlinkTotal != 222 {
		t.Fatal(row)
	}
	apply(&daemon.ConnectionEvent{Type: daemon.ConnectionEventType_CONNECTION_EVENT_CLOSED, Id: "a", ClosedAt: 1000})
	row = s.rows["a"]
	if row.ClosedAt != 1000 || row.DownlinkRate != 0 || row.DownlinkTotal != 222 {
		t.Fatal(row)
	}
	apply(&daemon.ConnectionEvent{Type: daemon.ConnectionEventType_CONNECTION_EVENT_UPDATE, Id: "a", UplinkDelta: 500})
	if s.rows["a"].UplinkTotal != 110 {
		t.Fatal("late update changed closed row")
	}
	apply(&daemon.ConnectionEvent{Type: daemon.ConnectionEventType_CONNECTION_EVENT_CLOSED, Id: "b", ClosedAt: 2000, Connection: &daemon.Connection{Id: "b", UplinkTotal: 999}})
	if s.rows["b"].UplinkTotal != 999 || s.rows["b"].ClosedAt != 2000 {
		t.Fatal(s.rows["b"])
	}
	apply(&daemon.ConnectionEvent{Type: daemon.ConnectionEventType_CONNECTION_EVENT_CLOSED, Id: "missing"})
	if len(s.rows) != 2 {
		t.Fatal("close invented missing connection")
	}
	s.connections(&daemon.ConnectionEvents{Reset_: true, Events: []*daemon.ConnectionEvent{{Id: "fresh", Connection: &daemon.Connection{Id: "fresh"}}}})
	if len(s.rows) != 1 || s.rows["fresh"].Connection == nil {
		t.Fatal("reset failed")
	}
	if add(math.MaxUint64, 1) != math.MaxUint64 {
		t.Fatal("overflow")
	}
}

func TestCacheBoundsAndSnapshotIsolation(t *testing.T) {
	s := newCache()
	var events []*daemon.ConnectionEvent
	for i := 0; i < 1105; i++ {
		id := fmt.Sprint(i)
		events = append(events, &daemon.ConnectionEvent{Id: id, Connection: &daemon.Connection{Id: id, ClosedAt: int64(i + 1)}})
	}
	for i := 0; i < 1050; i++ {
		id := fmt.Sprintf("active-%d", i)
		events = append(events, &daemon.ConnectionEvent{Id: id, Connection: &daemon.Connection{Id: id}})
	}
	s.connections(&daemon.ConnectionEvents{Events: events})
	if len(s.rows) != 2050 {
		t.Fatalf("retained %d rows", len(s.rows))
	}
	if _, ok := s.rows["0"]; ok {
		t.Fatal("oldest closed row retained")
	}
	if _, ok := s.rows["104"]; ok {
		t.Fatal("old closed row retained")
	}
	if _, ok := s.rows["105"]; !ok {
		t.Fatal("new closed row pruned")
	}
	messages := make([]*daemon.Log_Message, 3100)
	for i := range messages {
		messages[i] = &daemon.Log_Message{Message: fmt.Sprint(i)}
	}
	s.logs(&daemon.Log{Messages: messages})
	if len(s.Logs) != 3000 || s.Logs[0].Message != "100" {
		t.Fatal("log bound")
	}
	lastID := s.Logs[len(s.Logs)-1].ID
	s.logs(&daemon.Log{Reset_: true, Messages: []*daemon.Log_Message{{Message: "new"}}})
	if len(s.Logs) != 1 || s.Logs[0].ID <= lastID {
		t.Fatal("log reset/sequence")
	}
	for i := range 45 {
		s.status(&daemon.Status{Uplink: int64(i), Downlink: int64(i + 10)})
	}
	if len(s.UploadHistory) != 30 || s.UploadHistory[0] != 15 || s.DownloadHistory[29] != 54 {
		t.Fatal("history bound")
	}
	s.ServiceStatus = &daemon.ServiceStatus{Status: daemon.ServiceStatus_STARTED}
	s.Groups = []*daemon.Group{{Tag: "g", Items: []*daemon.GroupItem{{Tag: "item"}}}}
	s.Outbounds = []*daemon.GroupItem{{Tag: "out"}}
	s.Modes = []string{"rule"}
	s.StreamErrors["test"] = "old"
	first := s.snapshot()
	second := s.snapshot()
	first.ServiceStatus.Status = daemon.ServiceStatus_FATAL
	first.Status.Uplink = 999
	first.UploadHistory[0] = 999
	first.Logs[0].Message = "changed"
	first.Connections[0].Connection.Id = "changed"
	first.Groups[0].Items[0].Tag = "changed"
	first.Modes[0] = "changed"
	first.Outbounds[0].Tag = "changed"
	first.StreamErrors["test"] = "changed"
	if s.Status.Uplink != 44 || s.Groups[0].Items[0].Tag != "item" || second.Connections[0].Connection.Id == "changed" || s.Logs[0].Message != "new" || s.StreamErrors["test"] != "old" || s.Modes[0] != "rule" {
		t.Fatal("published mutable aliases")
	}
}
