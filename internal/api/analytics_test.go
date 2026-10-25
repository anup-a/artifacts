package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/anup-a/byagent/internal/usage"
)

const browserUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15"

// view requests a page the way a browser, or something pretending to be
// one, would.
func view(t *testing.T, method, url string, hdr map[string]string) int {
	t.Helper()
	req, _ := http.NewRequest(method, url, nil)
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,*/*;q=0.8")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode
}

func getReport(t *testing.T, url, tok string) (int, analyticsReport) {
	t.Helper()
	res, err := http.DefaultClient.Do(authReq(t, "GET", url, tok))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var rep analyticsReport
	if res.StatusCode == 200 {
		if err := json.NewDecoder(res.Body).Decode(&rep); err != nil {
			t.Fatal(err)
		}
	}
	return res.StatusCode, rep
}

func TestAnalyticsEndToEnd(t *testing.T) {
	var views *usage.Counter
	srv, d, tok := newTestServerWith(t, false, func(d *Deps) {
		views = usage.New(d.Store)
		d.OnView = views.Record
	})
	id := publishOne(t, srv.URL, tok, "<p>hello</p>")
	other := publishOne(t, srv.URL, tok, "<p>other</p>")
	page := srv.URL + "/a/" + id + "/"
	view(t, "GET", page, map[string]string{"Referer": "https://news.ycombinator.com/item?id=1"})
	view(t, "GET", page, map[string]string{"Referer": "https://news.ycombinator.com/item?id=1"}) // same visitor again
	view(t, "GET", page, nil)
	view(t, "GET", srv.URL+"/a/"+other+"/", nil)
	// None of these are a person reading the page.
	view(t, "HEAD", page, nil)
	view(t, "GET", page, map[string]string{"User-Agent": "Mozilla/5.0 (compatible; Googlebot/2.1)"})
	view(t, "GET", page, map[string]string{"Accept": "*/*"})
	view(t, "GET", page+"?"+usage.PreviewParam+"=1", nil)
	view(t, "GET", page, map[string]string{"CF-IPCountry": "FR"}) // counted, but the header is not trusted here
	if err := views.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}

	code, rep := getReport(t, srv.URL+"/api/artifacts/"+id+"/analytics?range=7d", tok)
	if code != 200 {
		t.Fatalf("page report: %d", code)
	}
	if rep.ID != id || rep.Range != "7d" || rep.Bucket != "day" || len(rep.Series) != 7 || rep.Totals.RangeViews != 4 || rep.Totals.RangeVisitors != 1 || rep.Totals.AllTimeViews != 4 {
		t.Fatalf("page report %+v", rep)
	}
	if last := rep.Series[6]; last.Views != 4 || last.Visitors != 1 {
		t.Fatalf("today %+v", last)
	}
	if len(rep.TopReferrers) != 2 || rep.TopReferrers[0] != (referrerCount{Referrer: usage.Direct, Views: 2}) && rep.TopReferrers[0] != (referrerCount{Referrer: "news.ycombinator.com", Views: 2}) {
		t.Fatalf("referrers %+v", rep.TopReferrers)
	}
	if len(rep.TopCountries) != 0 || len(rep.TopPaths) != 1 || rep.TopPaths[0].Path != "/" || rep.TopPaths[0].ID != "" || rep.TopPages != nil {
		t.Fatalf("lists %+v %+v %+v", rep.TopCountries, rep.TopPaths, rep.TopPages)
	}
	if len(rep.TopDevices) != 1 || rep.TopDevices[0].Device != "desktop" {
		t.Fatalf("devices %+v", rep.TopDevices)
	}

	code, rep = getReport(t, srv.URL+"/api/artifacts/"+id+"/analytics?range=24h", tok)
	if code != 200 || rep.Bucket != "hour" || len(rep.Series) != 24 || rep.Totals.RangeViews != 4 {
		t.Fatalf("24h: %d %+v", code, rep)
	}

	code, rep = getReport(t, srv.URL+"/api/analytics", tok)
	if code != 200 || rep.Range != "30d" || rep.ID != "" || rep.Totals.RangeViews != 5 || rep.TopPages == nil || len(*rep.TopPages) != 2 {
		t.Fatalf("account: %d %+v", code, rep)
	}
	if top := (*rep.TopPages)[0]; top.ID != id || top.Views != 4 || top.URL != "http://share.test/a/"+id+"/" {
		t.Fatalf("top page %+v", top)
	}
	if len(rep.TopPaths) != 2 || rep.TopPaths[0].ID != id {
		t.Fatalf("account paths %+v", rep.TopPaths)
	}

	// Another workspace sees neither the page nor its numbers.
	outsider := mintKey(t, d, "personal")
	if code, _ := getReport(t, srv.URL+"/api/artifacts/"+id+"/analytics", outsider); code != 404 {
		t.Fatalf("outsider page report: %d", code)
	}
	if code, rep := getReport(t, srv.URL+"/api/analytics?range=all", outsider); code != 200 || rep.Totals.AllTimeViews != 0 || len(*rep.TopPages) != 0 || rep.FirstViewDay != nil {
		t.Fatalf("outsider account report: %d %+v", code, rep)
	}
	for _, bad := range []string{"1y", "7D", "24h%20OR%201=1"} {
		if code, _ := getReport(t, srv.URL+"/api/analytics?range="+bad, tok); code != 400 {
			t.Errorf("range %q: %d", bad, code)
		}
	}
	if code, _ := getReport(t, srv.URL+"/api/analytics", ""); code != 401 {
		t.Fatalf("no key: %d", code)
	}

	// Delete erases the numbers along with the page.
	res, err := http.DefaultClient.Do(authReq(t, "DELETE", srv.URL+"/api/artifacts/"+id, tok))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if code, _ := getReport(t, srv.URL+"/api/artifacts/"+id+"/analytics", tok); code != 410 {
		t.Fatalf("deleted page report: %d", code)
	}
	if code, rep := getReport(t, srv.URL+"/api/analytics?range=all", tok); code != 200 || rep.Totals.AllTimeViews != 1 || len(*rep.TopPages) != 1 {
		t.Fatalf("account after delete: %d %+v", code, rep)
	}
}

func TestAnalyticsTrustsCountryOnlyBehindProxy(t *testing.T) {
	var views *usage.Counter
	srv, _, tok := newTestServerWith(t, false, func(d *Deps) {
		views = usage.New(d.Store)
		views.TrustProxy = true
		d.OnView = views.Record
	})
	id := publishOne(t, srv.URL, tok, "<p>hello</p>")
	view(t, "GET", srv.URL+"/a/"+id+"/", map[string]string{"CF-IPCountry": "FR", "CF-Connecting-IP": "203.0.113.9"})
	view(t, "GET", srv.URL+"/a/"+id+"/", map[string]string{"CF-IPCountry": "DE", "CF-Connecting-IP": "203.0.113.10"})
	if err := views.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, rep := getReport(t, srv.URL+"/api/artifacts/"+id+"/analytics", tok)
	if len(rep.TopCountries) != 2 || rep.Totals.RangeVisitors != 2 {
		t.Fatalf("countries %+v visitors %d", rep.TopCountries, rep.Totals.RangeVisitors)
	}
}

func TestGuestsHaveNoAnalytics(t *testing.T) {
	srv, _, _ := newGuestServer(t)
	tok := newGuest(t, srv.URL)["token"].(string)
	id := publishOne(t, srv.URL, tok, "<p>hello</p>")
	if code, _ := getReport(t, srv.URL+"/api/artifacts/"+id+"/analytics", tok); code != 403 {
		t.Fatalf("guest page report: %d", code)
	}
	if code, _ := getReport(t, srv.URL+"/api/analytics", tok); code != 403 {
		t.Fatalf("guest account report: %d", code)
	}
}
