package telemetry

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/anup-a/byagent/internal/store"
)

func TestObserveTotals(t *testing.T) {
	rd := sdkmetric.NewManualReader()
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(rd)))
	t.Cleanup(func() { otel.SetMeterProvider(prev) })

	calls := 0
	err := ObserveTotals(func(context.Context, time.Time) (store.Totals, error) {
		calls++
		return store.Totals{Users: 3, Guests: 5, LivePages: 7, DeletedPages: 2}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	collect := func() map[string]int64 {
		var rm metricdata.ResourceMetrics
		if err := rd.Collect(context.Background(), &rm); err != nil {
			t.Fatal(err)
		}
		got := map[string]int64{}
		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				for _, dp := range m.Data.(metricdata.Gauge[int64]).DataPoints {
					got[m.Name+"/"+str(dp.Attributes, "byagent.page.state")] = dp.Value
				}
			}
		}
		return got
	}
	got := collect()
	want := map[string]int64{"byagent.users/": 3, "byagent.guests/": 5, "byagent.pages/live": 7, "byagent.pages/deleted": 2}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %d, want %d", k, got[k], v)
		}
	}
	// A second collection inside the minute reuses the count.
	collect()
	if calls != 1 {
		t.Errorf("counted %d times, want once", calls)
	}
}
