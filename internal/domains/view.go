package domains

import (
	"net"
	"time"

	"github.com/anup-a/artifacts/internal/store"
)

// Record is one DNS record the owner adds at their DNS provider. Host is
// relative to the zone, as most providers' forms want it ("docs", "@");
// Name is the full name.
type Record struct {
	Type    string `json:"type"`
	Host    string `json:"host"`
	Name    string `json:"name"`
	Value   string `json:"value"`
	Purpose string `json:"purpose"`
}

// Verification says whether a pending domain needs its owner to act.
type Verification struct {
	State   string `json:"state"` // verifying | action_required
	Message string `json:"message"`
}

// MountView is one mount as the API returns it.
type MountView struct {
	Domain     string    `json:"domain"`
	Path       string    `json:"path"`
	ArtifactID string    `json:"artifact_id"`
	URL        string    `json:"url"`
	Primary    bool      `json:"primary"`
	CreatedAt  time.Time `json:"created_at"`
}

// DomainView is one domain as the API returns it.
type DomainView struct {
	Domain          string        `json:"domain"`
	Status          string        `json:"status"`
	Verified        bool          `json:"verified"`
	SSLStatus       string        `json:"ssl_status"`
	IsApex          bool          `json:"is_apex"`
	URL             string        `json:"url"`
	DNSInstructions []Record      `json:"dns_instructions"`
	Verification    *Verification `json:"verification,omitempty"`
	Mounts          []MountView   `json:"mounts"`
	CreatedAt       time.Time     `json:"created_at"`
	VerifiedAt      *time.Time    `json:"verified_at"`
	CheckedAt       *time.Time    `json:"checked_at"`
}

// Records are the DNS records that connect hostname: one that routes it to
// byagent and one that proves the workspace controls it.
func (s *Service) Records(d store.Domain) []Record {
	zone := Registrable(d.Hostname)
	route := "routes the domain to byagent"
	var out []Record
	if d.Hostname != zone {
		out = append(out, Record{Type: "CNAME", Host: relativeName(d.Hostname, zone), Name: d.Hostname, Value: s.Target, Purpose: route})
	} else if len(s.ApexIPs) > 0 {
		for _, ip := range s.ApexIPs {
			t := "A"
			if p := net.ParseIP(ip); p != nil && p.To4() == nil {
				t = "AAAA"
			}
			out = append(out, Record{Type: t, Host: "@", Name: d.Hostname, Value: ip, Purpose: route})
		}
	} else {
		out = append(out, Record{Type: "ALIAS", Host: "@", Name: d.Hostname, Value: s.Target,
			Purpose: route + "; an apex cannot hold a CNAME, so use your DNS provider's ALIAS, ANAME or CNAME flattening"})
	}
	txt := TXTName(d.Hostname)
	out = append(out, Record{Type: "TXT", Host: relativeName(txt, zone), Name: txt, Value: TXTValue(d.Token),
		Purpose: "proves this workspace controls the domain; keep it in place"})
	return out
}

// View renders a domain and its mounts.
func (s *Service) View(d store.Domain, mounts []store.Mount, now time.Time) DomainView {
	v := DomainView{
		Domain: d.Hostname, Status: d.Status, Verified: d.VerifiedAt != nil, SSLStatus: d.SSLStatus,
		IsApex: IsApex(d.Hostname), URL: s.URL(d.Hostname, ""), DNSInstructions: s.Records(d),
		Mounts: []MountView{}, CreatedAt: d.CreatedAt, VerifiedAt: d.VerifiedAt, CheckedAt: d.CheckedAt,
	}
	if d.Status != "active" {
		state := "verifying"
		msg := "Add the records in dns_instructions. byagent checks every few minutes; most domains go active within 20 minutes of the records appearing."
		if d.Status == "error" || now.Sub(d.CreatedAt) > actionAfter {
			state = "action_required"
			msg = "The domain is not active yet. Compare your DNS records with dns_instructions and fix any that differ."
		}
		if d.Message != "" {
			msg += " Last check: " + d.Message + "."
		}
		v.Verification = &Verification{State: state, Message: msg}
	}
	for _, m := range mounts {
		v.Mounts = append(v.Mounts, s.MountView(m))
	}
	return v
}

func (s *Service) MountView(m store.Mount) MountView {
	return MountView{Domain: m.Hostname, Path: DisplayPath(m.Path), ArtifactID: m.ArtifactID, URL: s.URL(m.Hostname, m.Path), Primary: m.Primary, CreatedAt: m.CreatedAt}
}
