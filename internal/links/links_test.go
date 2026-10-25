package links

import "testing"

func TestNormalize(t *testing.T) {
	own := []string{"byagent.dev", "byagent.page"}
	for _, c := range []struct{ in, url, host string }{
		{"https://my-app.vercel.app", "https://my-app.vercel.app/", "my-app.vercel.app"},
		{"  HTTPS://User:pw@Docs.Example.COM:8443/a?b=1#top ", "https://docs.example.com:8443/a?b=1", "docs.example.com"},
		{"http://anup-a.github.io/site/", "http://anup-a.github.io/site/", "anup-a.github.io"},
	} {
		got, host, err := Normalize(c.in, own)
		if err != nil || got != c.url || host != c.host {
			t.Errorf("Normalize(%q) = %q %q %v, want %q %q", c.in, got, host, err, c.url, c.host)
		}
	}
	for _, bad := range []string{"", "ftp://x.com/", "/relative", "https://localhost:3000/", "http://127.0.0.1/", "http://intranet/", "http://box.local/",
		"https://abc.byagent.dev/", "https://byagent.page/x", "https://x.com/" + string(make([]byte, MaxURL))} {
		if _, _, err := Normalize(bad, own); err == nil {
			t.Errorf("Normalize(%q) accepted", bad)
		}
	}
}

func TestProvider(t *testing.T) {
	for host, want := range map[string]string{
		"my-app-git-main.vercel.app": "Vercel", "vercel.app": "Vercel", "x.pages.dev": "Cloudflare Pages",
		"anup-a.github.io": "GitHub Pages", "www.example.com": "example.com", "notvercel.app": "notvercel.app",
	} {
		if got := Provider(host); got != want {
			t.Errorf("Provider(%q) = %q, want %q", host, got, want)
		}
	}
}
