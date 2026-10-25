package usage

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anup-a/byagent/internal/store"
)

const safari = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15"
const iphone = "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1"

func pageReq(method, target string, hdr map[string]string) *http.Request {
	r := httptest.NewRequest(method, target, nil)
	r.Header.Set("User-Agent", safari)
	r.Header.Set("Accept", "text/html,application/xhtml+xml,*/*;q=0.8")
	for k, v := range hdr {
		if v == "" {
			r.Header.Del(k)
		} else {
			r.Header.Set(k, v)
		}
	}
	return r
}

func TestCountableKeepsBrowsersOnly(t *testing.T) {
	cases := []struct {
		name   string
		method string
		target string
		hdr    map[string]string
		want   bool
	}{
		{"browser", "GET", "/a/x/", nil, true},
		{"phone", "GET", "/a/x/", map[string]string{"User-Agent": iphone}, true},
		{"head", "HEAD", "/a/x/", nil, false},
		{"dashboard preview", "GET", "/a/x/?" + PreviewParam + "=1", nil, false},
		{"prefetch", "GET", "/a/x/", map[string]string{"Sec-Purpose": "prefetch"}, false},
		{"no accept", "GET", "/a/x/", map[string]string{"Accept": ""}, false},
		{"fetch from a script", "GET", "/a/x/", map[string]string{"Accept": "*/*"}, false},
		{"curl", "GET", "/a/x/", map[string]string{"User-Agent": "curl/8.4.0"}, false},
		{"no user agent", "GET", "/a/x/", map[string]string{"User-Agent": ""}, false},
		{"googlebot", "GET", "/a/x/", map[string]string{"User-Agent": "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)"}, false},
		{"slack unfurl", "GET", "/a/x/", map[string]string{"User-Agent": "Slackbot-LinkExpanding 1.0 (+https://api.slack.com/robots)"}, false},
		{"twitter card", "GET", "/a/x/", map[string]string{"User-Agent": "Mozilla/5.0 (compatible; Twitterbot/1.0)"}, false},
		{"headless chrome", "GET", "/a/x/", map[string]string{"User-Agent": "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) HeadlessChrome/120.0 Safari/537.36"}, false},
		{"gptbot", "GET", "/a/x/", map[string]string{"User-Agent": "Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; GPTBot/1.2; +https://openai.com/gptbot)"}, false},
	}
	for _, c := range cases {
		if got := Countable(pageReq(c.method, c.target, c.hdr)); got != c.want {
			t.Errorf("%s: Countable = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestReferrerIsAHostAndNeverMarkup(t *testing.T) {
	cases := []struct{ referer, target, want string }{
		{"", "http://share.test/a/abc/", Direct},
		{"https://news.ycombinator.com/item?id=1", "http://share.test/a/abc/", "news.ycombinator.com"},
		{"https://www.Google.com/", "http://share.test/a/abc/", "google.com"},
		{"android-app://com.google.android.gm/", "http://share.test/a/abc/", Direct},
		{"not a url at all", "http://share.test/a/abc/", Direct},
		// A link from elsewhere in the same page is navigation, not a source.
		{"http://share.test/a/abc/", "http://share.test/a/abc/b.html", ""},
		// Another page on the same host is a source.
		{"http://share.test/a/other/", "http://share.test/a/abc/", "share.test"},
		// On a page's own host every link is internal.
		{"https://docs.example.com/intro.html", "https://docs.example.com/b.html", ""},
		{"https://evil.test%1b%5b31m/", "http://share.test/a/abc/", Direct},
		{"https://<script>.test/", "http://share.test/a/abc/", store.FacetOther},
		{"https://evil.test\x1b[31m/", "http://share.test/a/abc/", Direct},
		{"https://" + strings.Repeat("a", 300) + ".test/", "http://share.test/a/abc/", store.FacetOther},
		{"https://[::1]:8080/", "http://share.test/a/abc/", store.FacetOther},
	}
	for _, c := range cases {
		r := pageReq("GET", c.target, map[string]string{"Referer": c.referer})
		if got := referrer(r, "abc"); got != c.want {
			t.Errorf("referer %q on %s: got %q, want %q", c.referer, c.target, got, c.want)
		}
	}
}

func TestCountryOnlyFromATrustedProxy(t *testing.T) {
	r := pageReq("GET", "/a/x/", map[string]string{"CF-IPCountry": "DE"})
	if got := country(r, false); got != "" {
		t.Fatalf("untrusted header used: %q", got)
	}
	if got := country(r, true); got != "DE" {
		t.Fatalf("trusted header: %q", got)
	}
	for _, bad := range []string{"XX", "T1", "de", "<b>", "DEU", ""} {
		r.Header.Set("CF-IPCountry", bad)
		if got := country(r, true); got != "" {
			t.Errorf("%q gave %q", bad, got)
		}
	}
}

func TestDeviceClass(t *testing.T) {
	for ua, want := range map[string]string{
		safari: "desktop",
		iphone: "mobile",
		"Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Mobile Safari/537.36": "mobile",
		"Mozilla/5.0 (Linux; Android 13; SM-X700) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36":        "tablet",
		"Mozilla/5.0 (iPad; CPU OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/604.1":  "tablet",
	} {
		if got := device(ua); got != want {
			t.Errorf("%s: %s, want %s", ua, got, want)
		}
	}
}

func TestPagePath(t *testing.T) {
	for in, want := range map[string]string{"index.html": "/", "docs/index.html": "/docs/", "b.html": "/b.html"} {
		if got := pagePath(in); got != want {
			t.Errorf("%s: %s, want %s", in, got, want)
		}
	}
}

func TestRecordCountsVisitorsOncePerPagePerDay(t *testing.T) {
	f := &fakeViews{}
	c := New(f)
	clock := time.Date(2026, 10, 4, 9, 30, 0, 0, time.UTC)
	c.Now = func() time.Time { return clock }
	view := func(id, ip, ua, referer string) {
		r := pageReq("GET", "http://share.test/a/"+id+"/", map[string]string{"User-Agent": ua, "Referer": referer})
		r.RemoteAddr = ip + ":1234"
		c.Record(r, id, "index.html")
	}
	view("a", "10.0.0.1", safari, "https://news.ycombinator.com/")
	view("a", "10.0.0.1", safari, "")                   // same person, reload
	view("a", "10.0.0.2", iphone, "")                   // someone else
	view("b", "10.0.0.1", safari, "")                   // same person, other page
	r := pageReq("HEAD", "http://share.test/a/a/", nil) // not a view
	c.Record(r, "a", "index.html")
	clock = clock.Add(24 * time.Hour) // a new day forgets everyone
	view("a", "10.0.0.1", safari, "")
	if err := c.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	days := map[string]store.ViewCount{}
	for _, v := range f.got {
		days[v.ArtifactID+" "+v.Day] = v
	}
	for key, want := range map[string][2]int64{"a 2026-10-04": {3, 2}, "b 2026-10-04": {1, 1}, "a 2026-10-05": {1, 1}} {
		if got := days[key]; got.Count != want[0] || got.Visitors != want[1] {
			t.Errorf("%s: %+v, want views %d visitors %d", key, got, want[0], want[1])
		}
	}
	hours := map[string]store.ViewHour{}
	for _, h := range f.hours {
		hours[h.ArtifactID+" "+h.Hour] = h
	}
	if h := hours["a 2026-10-04T09"]; h.Views != 3 || h.Visitors != 2 {
		t.Errorf("hour row %+v", h)
	}
	facets := map[string]int64{}
	for _, fc := range f.facets {
		facets[fc.ArtifactID+" "+fc.Day+" "+fc.Kind+" "+fc.Value] = fc.Views
	}
	for key, want := range map[string]int64{
		"a 2026-10-04 referrer news.ycombinator.com": 1,
		"a 2026-10-04 referrer " + Direct:            2,
		"a 2026-10-04 device desktop":                2,
		"a 2026-10-04 device mobile":                 1,
		"a 2026-10-04 path /":                        3,
	} {
		if facets[key] != want {
			t.Errorf("%s: %d, want %d (all: %v)", key, facets[key], want, facets)
		}
	}
	for key := range facets {
		if strings.Contains(key, " country ") {
			t.Errorf("country recorded without a trusted proxy: %s", key)
		}
	}
}

// A visitor making up a fresh referrer on every request fills one "(other)"
// bucket, not memory.
func TestBufferedFacetsAreCapped(t *testing.T) {
	f := &fakeViews{}
	c := New(f)
	now := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	for i := 0; i < 1000; i++ {
		c.Add(Hit{ArtifactID: "a", Referrer: fmt.Sprintf("spam%d.test", i)}, now)
	}
	if err := c.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	var other int64
	distinct := 0
	for _, fc := range f.facets {
		if fc.Kind != store.FacetReferrer {
			continue
		}
		distinct++
		if fc.Value == store.FacetOther {
			other = fc.Views
		}
	}
	if distinct != maxBufferedValues+1 || other != 1000-maxBufferedValues {
		t.Fatalf("distinct %d, other %d", distinct, other)
	}
}

func TestDetailsSurviveAFailedFlush(t *testing.T) {
	f := &fakeViews{fail: true}
	c := New(f)
	now := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	c.Add(Hit{ArtifactID: "a", Referrer: "x.test", Visitor: "v"}, now)
	if err := c.Flush(context.Background()); err == nil {
		t.Fatal("expected error")
	}
	f.fail = false
	c.Add(Hit{ArtifactID: "a", Referrer: "x.test", Visitor: "w"}, now)
	if err := c.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.hours) != 1 || f.hours[0].Views != 2 || f.hours[0].Visitors != 2 {
		t.Fatalf("hours %+v", f.hours)
	}
	if len(f.facets) != 1 || f.facets[0].Views != 2 {
		t.Fatalf("facets %+v", f.facets)
	}
	if len(f.got) != 1 || f.got[0].Count != 2 || f.got[0].Visitors != 2 {
		t.Fatalf("days %+v", f.got)
	}
}

func TestWindows(t *testing.T) {
	now := time.Date(2026, 10, 4, 13, 20, 0, 0, time.UTC)
	w := WindowFor("24h", now)
	q := w.Query("t", "a", 10)
	if !q.Hourly || q.FromHour != "2026-10-03T14" || q.ToHour != "2026-10-04T14" || q.FromDay != "2026-10-03" || q.ToDay != "2026-10-05" {
		t.Fatalf("24h query %+v", q)
	}
	s := w.Series(store.Analytics{Series: []store.AnalyticsPoint{{Bucket: "2026-10-04T13", Views: 4, Visitors: 3}}})
	if len(s) != 24 || s[23] != (Point{Bucket: "2026-10-04T13:00:00Z", Views: 4, Visitors: 3}) || s[0].Bucket != "2026-10-03T14:00:00Z" {
		t.Fatalf("24h series %v", s)
	}
	w = WindowFor("7d", now)
	q = w.Query("t", "", 10)
	if q.Hourly || q.FromDay != "2026-09-28" || q.ToDay != "2026-10-05" {
		t.Fatalf("7d query %+v", q)
	}
	if s := w.Series(store.Analytics{}); len(s) != 7 || s[6].Bucket != "2026-10-04" {
		t.Fatalf("7d series %v", s)
	}
	w = WindowFor("all", now)
	if q := w.Query("t", "a", 10); q.FromDay != "" || q.ToDay != "2026-10-05" {
		t.Fatalf("all query %+v", q)
	}
	if s := w.Series(store.Analytics{FirstDay: "2026-10-01"}); len(s) != 4 || s[0].Bucket != "2026-10-01" || !w.From.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("all series %v %v", s, w.From)
	}
	for _, bad := range []string{"1d", "7D", "all ", "24h;drop"} {
		if _, ok := ParseRange(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
	if r, ok := ParseRange(""); !ok || r != DefaultRange {
		t.Fatal("default range")
	}
}
