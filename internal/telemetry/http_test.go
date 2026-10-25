package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/anup-a/byagent/internal/pageurl"
)

var testHosts = Hosts{
	Site:  "byagent.dev",
	App:   "app.byagent.dev",
	Pages: pageurl.Builder{ShareOrigin: "https://byagent.dev", Scheme: "https", Domain: "byagent.dev", Shared: true},
}

func TestMiddlewareLabels(t *testing.T) {
	inner := http.NewServeMux()
	inner.HandleFunc("GET /api/artifacts/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hello"))
	})
	inner.HandleFunc("POST /api/artifacts", func(w http.ResponseWriter, r *http.Request) {
		MarkPublish(r.Context(), true)
		w.WriteHeader(http.StatusBadRequest)
	})
	inner.HandleFunc("GET /custom", func(w http.ResponseWriter, r *http.Request) {
		SetHostKind(r.Context(), HostCustomDomain)
	})
	inner.HandleFunc("GET /boom", func(w http.ResponseWriter, r *http.Request) {
		panic(http.ErrAbortHandler)
	})
	outer := http.NewServeMux()
	outer.Handle("/", Routed(inner))

	rd := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(rd), sdkmetric.WithView(sizeView))
	h, err := Middleware(mp, testHosts, Routed(outer))
	if err != nil {
		t.Fatal(err)
	}
	do := func(method, url string) {
		defer func() { recover() }()
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(method, url, nil))
	}
	do("GET", "https://app.byagent.dev/api/artifacts/abc")
	do("POST", "https://app.byagent.dev/api/artifacts")
	do("GET", "https://byagent.dev/nope")
	do("GET", "https://my-page.byagent.dev/custom")
	do("GET", "https://byagent.dev/boom")
	do("BREW", "https://elsewhere.test/api/artifacts/abc")

	var rm metricdata.ResourceMetrics
	if err := rd.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	type key struct {
		method, route, host string
		status              int64
	}
	got := map[key]uint64{}
	publishes := map[string]int64{}
	var active int64 = -1
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch m.Name {
			case "http.server.request.duration":
				for _, dp := range m.Data.(metricdata.Histogram[float64]).DataPoints {
					got[key{str(dp.Attributes, "http.request.method"), str(dp.Attributes, "http.route"),
						str(dp.Attributes, "byagent.host_kind"), num(dp.Attributes, "http.response.status_code")}] += dp.Count
				}
			case "http.server.response.body.size":
				for _, dp := range m.Data.(metricdata.Histogram[int64]).DataPoints {
					if len(dp.Bounds) != 0 {
						t.Errorf("response size has bucket bounds %v, want none", dp.Bounds)
					}
				}
			case "byagent.publishes":
				for _, dp := range m.Data.(metricdata.Sum[int64]).DataPoints {
					publishes[str(dp.Attributes, "byagent.publish.kind")+"/"+str(dp.Attributes, "byagent.publish.result")] += dp.Value
				}
			case "http.server.active_requests":
				active = 0
				for _, dp := range m.Data.(metricdata.Sum[int64]).DataPoints {
					active += dp.Value
				}
			}
		}
	}
	want := map[key]uint64{
		{"GET", "/api/artifacts/{id}", HostApp, 200}: 1,
		{"POST", "/api/artifacts", HostApp, 400}:     1,
		{"GET", routeUnmatched, HostSite, 404}:       1,
		{"GET", "/custom", HostCustomDomain, 200}:    1,
		{"GET", "/boom", HostSite, 500}:              1,
		{"_OTHER", routeUnmatched, HostOther, 405}:   1,
	}
	if len(got) != len(want) {
		t.Errorf("series = %v, want %v", got, want)
	}
	for k, n := range want {
		if got[k] != n {
			t.Errorf("%+v: count %d, want %d (all: %v)", k, got[k], n, got)
		}
	}
	if publishes["guest/error"] != 1 || len(publishes) != 1 {
		t.Errorf("publishes = %v, want guest/error once", publishes)
	}
	if active != 0 {
		t.Errorf("active requests = %d after all finished, want 0", active)
	}
}

func str(s attribute.Set, k string) string {
	v, _ := s.Value(attribute.Key(k))
	return v.AsString()
}

func num(s attribute.Set, k string) int64 {
	v, _ := s.Value(attribute.Key(k))
	return v.AsInt64()
}
