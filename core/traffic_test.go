package core

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"phantom-node/panel"
	"phantom-node/singbox"

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
