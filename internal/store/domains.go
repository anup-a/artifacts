package store

import (
	"context"
	"errors"
	"time"
)

// ErrDomainTaken is returned by CreateDomain when the hostname is already
// connected, by this workspace or another.
var ErrDomainTaken = errors.New("domain taken")

// ErrDomainQuota is returned by CreateDomain when the workspace already has
// as many domains as it may.
var ErrDomainQuota = errors.New("domain quota")

// Domain is a hostname a workspace has connected.
type Domain struct {
	Hostname string
	TenantID string
	// Token is the value of the TXT record at _byagent.<Hostname> that proves
	// the workspace controls the hostname's DNS.
	Token string
	// Status is pending, active or error. SSLStatus is the edge's own word
	// for the certificate; Message explains the last check.
	Status, SSLStatus, Message string
	// Provider and ProviderRef name the edge that terminates TLS for the
	// hostname and its id there.
	Provider, ProviderRef string
	// VerifiedAt is when the TXT record was first seen. Nothing is served on
	// the hostname before it.
	VerifiedAt *time.Time
	CheckedAt  *time.Time
	CreatedAt  time.Time
}

// Mount serves one artifact at a path on a domain. Path is "" for the root,
// else slash-separated segments with no leading or trailing slash.
type Mount struct {
	Hostname   string
	Path       string
	ArtifactID string
	// Primary marks the one mount the artifact's share-origin address
	// redirects to.
	Primary   bool
	CreatedAt time.Time
}

type DomainStore interface {
	// CreateDomain inserts a domain unless the hostname is taken
	// (ErrDomainTaken, with the existing row) or the workspace already holds
	// max domains (ErrDomainQuota).
	CreateDomain(ctx context.Context, d Domain, max int) (Domain, error)
	Domain(ctx context.Context, hostname string) (Domain, error)
	// Domains lists a workspace's domains, oldest first; "" lists every one.
	Domains(ctx context.Context, tenantID string) ([]Domain, error)
	// UpdateDomainState writes what a check found: status, SSL status,
	// message, provider and its ref, verified_at and checked_at.
	UpdateDomainState(ctx context.Context, d Domain) error
	// DeleteDomain removes a domain and every mount on it.
	DeleteDomain(ctx context.Context, hostname string) error
	// Mounts lists the mounts on one domain, or on every domain for "".
	Mounts(ctx context.Context, hostname string) ([]Mount, error)
	// ArtifactMounts lists every mount serving one artifact.
	ArtifactMounts(ctx context.Context, artifactID string) ([]Mount, error)
	// PutMount creates a mount or points an existing one at another
	// artifact. Re-pointing drops the primary mark.
	PutMount(ctx context.Context, m Mount) error
	DeleteMount(ctx context.Context, hostname, path string) error
	// SetPrimaryMount marks one of an artifact's mounts primary and clears
	// the rest; hostname "" clears them all.
	SetPrimaryMount(ctx context.Context, artifactID, hostname, path string) error
}
