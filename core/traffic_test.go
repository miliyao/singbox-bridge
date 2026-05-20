package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"singbox-bridge/panel"
	"singbox-bridge/singbox"

	"go.uber.org/zap"
)

type collectResponse struct {
	traffic []singbox.UserTraffic
	err     error
}

type fakeCollector struct {
	responses []collectResponse
	calls     int
}

func (f *fakeCollector) CollectTraffic(context.Context) ([]singbox.UserTraffic, error) {
	if f.calls >= len(f.responses) {
		return nil, nil
	}

	response := f.responses[f.calls]
	f.calls++
	return response.traffic, response.err
}

type fakePusher struct {
	errs     []error
	payloads [][]panel.TrafficData
	calls    int
}

func (f *fakePusher) PushTraffic(data []panel.TrafficData) error {
	cloned := append([]panel.TrafficData(nil), data...)
	f.payloads = append(f.payloads, cloned)

	var err error
	if f.calls < len(f.errs) {
		err = f.errs[f.calls]
	}
	f.calls++
	return err
}

func TestTrafficReporterBuffersFailedPushAndRetries(t *testing.T) {
	reporter := NewTrafficReporter(
		&fakeCollector{
			responses: []collectResponse{
				{traffic: []singbox.UserTraffic{{UserID: 1, Upload: 10, Download: 20}}},
				{},
			},
		},
		&fakePusher{errs: []error{errors.New("push failed"), nil}},
		zap.NewNop(),
		"",
	)

	reporter.Report(context.Background())
	if len(reporter.pending) != 1 {
		t.Fatalf("expected 1 buffered user, got %d", len(reporter.pending))
	}

	reporter.Report(context.Background())

	want := map[int]panel.TrafficData{}
	if !reflect.DeepEqual(reporter.pending, want) {
		t.Fatalf("expected pending traffic to be cleared, got %#v", reporter.pending)
	}
}

func TestTrafficReporterMergesBufferedAndFreshTraffic(t *testing.T) {
	pusher := &fakePusher{errs: []error{errors.New("push failed"), nil}}
	reporter := NewTrafficReporter(
		&fakeCollector{
			responses: []collectResponse{
				{traffic: []singbox.UserTraffic{{UserID: 1, Upload: 10, Download: 20}}},
				{traffic: []singbox.UserTraffic{
					{UserID: 1, Upload: 1, Download: 2},
					{UserID: 2, Upload: 3, Download: 4},
				}},
			},
		},
		pusher,
		zap.NewNop(),
		"",
	)

	reporter.Report(context.Background())
	reporter.Report(context.Background())

	if len(pusher.payloads) != 2 {
		t.Fatalf("expected 2 push attempts, got %d", len(pusher.payloads))
	}

	want := []panel.TrafficData{
		{UID: 1, Upload: 11, Download: 22},
		{UID: 2, Upload: 3, Download: 4},
	}
	if !reflect.DeepEqual(pusher.payloads[1], want) {
		t.Fatalf("unexpected merged payload: got %#v want %#v", pusher.payloads[1], want)
	}
}

func TestTrafficReporterPersistsBufferedTraffic(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "pending.json")
	reporter := NewTrafficReporter(
		&fakeCollector{
			responses: []collectResponse{
				{traffic: []singbox.UserTraffic{{UserID: 7, Upload: 10, Download: 20}}},
			},
		},
		&fakePusher{errs: []error{errors.New("push failed")}},
		zap.NewNop(),
		stateFile,
	)

	reporter.Report(context.Background())

	if _, err := os.Stat(stateFile); err != nil {
		t.Fatalf("expected state file to exist: %v", err)
	}

	restored := NewTrafficReporter(&fakeCollector{}, &fakePusher{}, zap.NewNop(), stateFile)
	if got := restored.pending[7]; got.Upload != 10 || got.Download != 20 {
		t.Fatalf("unexpected restored pending data: %#v", restored.pending)
	}
}

func TestTrafficReporterClearsPersistedTrafficAfterSuccess(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "pending.json")
	if err := os.WriteFile(stateFile, []byte(`[{"uid":1,"upload":5,"download":6}]`), 0o600); err != nil {
		t.Fatalf("failed to seed state file: %v", err)
	}

	reporter := NewTrafficReporter(
		&fakeCollector{},
		&fakePusher{},
		zap.NewNop(),
		stateFile,
	)

	reporter.Report(context.Background())

	if _, err := os.Stat(stateFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected state file to be removed, got err=%v", err)
	}
}

func TestTrafficReporterBacksUpCorruptPersistedTraffic(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "pending.json")
	if err := os.WriteFile(stateFile, []byte(`not-json`), 0o600); err != nil {
		t.Fatalf("failed to seed state file: %v", err)
	}

	reporter := NewTrafficReporter(&fakeCollector{}, &fakePusher{}, zap.NewNop(), stateFile)
	if len(reporter.pending) != 0 {
		t.Fatalf("expected no pending data from corrupt file, got %#v", reporter.pending)
	}
	if _, err := os.Stat(stateFile + ".corrupt"); err != nil {
		t.Fatalf("expected corrupt backup file to exist: %v", err)
	}
}

func TestTrafficReporterLimitsPendingUsers(t *testing.T) {
	reporter := NewTrafficReporterWithLimit(
		&fakeCollector{
			responses: []collectResponse{
				{traffic: []singbox.UserTraffic{
					{UserID: 1, Upload: 1},
					{UserID: 2, Upload: 2},
					{UserID: 3, Upload: 3},
				}},
			},
		},
		&fakePusher{errs: []error{errors.New("push failed")}},
		zap.NewNop(),
		"",
		2,
	)

	reporter.Report(context.Background())

	if len(reporter.pending) != 2 {
		t.Fatalf("expected 2 pending users, got %#v", reporter.pending)
	}
	if _, ok := reporter.pending[1]; ok {
		t.Fatalf("expected oldest uid to be dropped, got %#v", reporter.pending)
	}
}

func TestTrafficReporterSnapshotKeepsCollectErrorOnEmptyPayload(t *testing.T) {
	reporter := NewTrafficReporter(
		&fakeCollector{
			responses: []collectResponse{{err: errors.New("stats unavailable")}},
		},
		&fakePusher{},
		zap.NewNop(),
		"",
	)

	reporter.Report(context.Background())
	snapshot := reporter.Snapshot()

	if snapshot.LastReportOK {
		t.Fatalf("expected failed report snapshot, got %#v", snapshot)
	}
	if snapshot.LastReportError != "stats unavailable" {
		t.Fatalf("unexpected report error: %#v", snapshot)
	}
}
