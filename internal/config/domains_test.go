package config

import "testing"

func TestDomainsConfig(t *testing.T) {
	base := func(extra map[string]string) map[string]string {
		m := map[string]string{"ADMIN_TOKEN": "x"}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	off, err := FromEnv(env(base(nil)))
	if err != nil || off.Domains.Provider != "" || off.Limits.DomainsPerTenant != 5 || off.Domains.Resolver != "1.1.1.1:53" {
		t.Fatalf("defaults: %v %+v", err, off.Domains)
	}
	for _, bad := range []map[string]string{
		{"DOMAINS_PROVIDER": "cloudflare", "DOMAINS_CNAME_TARGET": "domains.byagent.dev"},
		{"DOMAINS_PROVIDER": "cloudflare", "CLOUDFLARE_API_TOKEN": "t", "CLOUDFLARE_ZONE_ID": "z"},
		{"DOMAINS_PROVIDER": "fly", "FLY_API_TOKEN": "t"},
		{"DOMAINS_PROVIDER": "netlify", "DOMAINS_CNAME_TARGET": "x.example.com"},
		{"DOMAINS_PROVIDER": "none", "DOMAINS_CNAME_TARGET": "x.example.com", "DOMAINS_APEX_IPS": "1.2.3.4,nope"},
		{"LIMIT_DOMAINS_PER_TENANT": "0"},
	} {
		if _, err := FromEnv(env(base(bad))); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
	cf, err := FromEnv(env(base(map[string]string{"DOMAINS_PROVIDER": "cloudflare", "CLOUDFLARE_API_TOKEN": "t", "CLOUDFLARE_ZONE_ID": "z",
		"DOMAINS_CNAME_TARGET": "Domains.Byagent.dev.", "DOMAINS_APEX_IPS": " 1.2.3.4 , 2a09::1 ", "LIMIT_DOMAINS_PER_TENANT": "9"})))
	if err != nil || cf.Domains.Target != "domains.byagent.dev" || len(cf.Domains.ApexIPs) != 2 || cf.Limits.DomainsPerTenant != 9 {
		t.Fatalf("cloudflare: %v %+v", err, cf.Domains)
	}
	fly, err := FromEnv(env(base(map[string]string{"DOMAINS_PROVIDER": "fly", "FLY_API_TOKEN": "t", "FLY_APP_NAME": "artifacts-share"})))
	if err != nil || fly.Domains.Target != "artifacts-share.fly.dev" {
		t.Fatalf("fly target default: %v %+v", err, fly.Domains)
	}
}
