package store

import (
	"context"
	"time"
)

type Tenant struct {
	ID, Kind, Name, Plan string
	CreatedAt            time.Time
}
type User struct {
	ID, Email, Name string
	CreatedAt       time.Time
}
type Identity struct {
	Provider, Subject, UserID string
	CreatedAt                 time.Time
}

// Session.ID is always a SHA-256 hex digest, never the raw cookie value.
type Session struct {
	ID, UserID, TenantID             string
	CreatedAt, ExpiresAt, LastSeenAt time.Time
}
type MagicLink struct {
	TokenSHA256, Email   string
	CreatedAt, ExpiresAt time.Time
	ConsumedAt           *time.Time
}
type EndUser struct {
	ID, TenantID, ExternalID string
	FirstSeenAt, LastSeenAt  time.Time
	ArtifactCount            int
}
type ArtifactFilter struct {
	Limit                               int
	Before                              time.Time
	BeforeID, EndUser, EndUserID, Query string
	// Project, when set, keeps only artifacts with exactly that project label.
	Project string
}
type ViewCount struct {
	ArtifactID, Day string
	Count           int64
	// Visitors is how many of the views came from a visitor not seen on
	// that page earlier the same day.
	Visitors int64
}

// ViewHour is one hour's views of a page, Hour as "2006-01-02T15" in UTC.
type ViewHour struct {
	ArtifactID, Hour string
	Views, Visitors  int64
}

// Facet kinds: what a view is broken down by.
const (
	FacetReferrer = "referrer"
	FacetCountry  = "country"
	FacetPath     = "path"
	FacetDevice   = "device"
)

// FacetOther collects the views of values past a day's cap.
const FacetOther = "(other)"

// FacetCap is how many distinct values one page keeps per day and kind
// before the rest are folded into FacetOther.
const FacetCap = 50

// ViewFacet counts one day's views of a page with one value of a facet.
type ViewFacet struct {
	ArtifactID, Day, Kind, Value string
	Views                        int64
}

// AnalyticsQuery selects the views a report covers: one page, or every live
// page of a tenant when ArtifactID is empty. Days are "2006-01-02" and hours
// "2006-01-02T15", From inclusive and To exclusive. Hourly reads the series
// and totals from the hour rows; facets always come from day rows.
type AnalyticsQuery struct {
	TenantID, ArtifactID string
	FromDay, ToDay       string
	FromHour, ToHour     string
	Hourly               bool
	// Top caps each ranked list.
	Top int
}

type AnalyticsPoint struct {
	Bucket          string
	Views, Visitors int64
}

type FacetCount struct {
	ArtifactID, Value string
	Views             int64
}

type PageViews struct {
	ArtifactID, Title, Visibility string
	Slug                          string // for the page's address
	Views, Visitors               int64
}

// Analytics is a report. Series holds only buckets with views, in order;
// callers fill the gaps.
type Analytics struct {
	AllTimeViews                         int64
	FirstDay                             string
	Views, Visitors                      int64
	Series                               []AnalyticsPoint
	Referrers, Countries, Paths, Devices []FacetCount
	// Pages ranks a tenant's pages; empty for a one-page report.
	Pages []PageViews
}
type MonthUsage struct {
	Month                                            time.Time
	Publishes, StorageBytes, PageViews, OpenComments int64
}

type TenantStore interface {
	Tenant(context.Context, string) (Tenant, error)
	CreateTenant(context.Context, Tenant, string) error
	RenameTenant(context.Context, string, string) error
	TenantsForUser(context.Context, string) ([]Tenant, error)
	CreateTenantKey(context.Context, string, string, string, string, string, time.Time) (Key, error)
	Keys(context.Context, string, time.Time) ([]Key, error)
	RevokeKey(context.Context, string, int64, time.Time) error
	TouchKey(context.Context, int64, time.Time) error
	UpsertEndUser(context.Context, string, string, time.Time) (EndUser, error)
	EndUser(context.Context, string, string) (EndUser, error)
	EndUserByID(context.Context, string, string) (EndUser, error)
	EndUsers(context.Context, string) ([]EndUser, error)
	AddViews(context.Context, []ViewCount) error
	// AddViewDetails adds hour rows and facet rows in one transaction,
	// folding facet values past FacetCap into FacetOther.
	AddViewDetails(context.Context, []ViewHour, []ViewFacet) error
	// PruneViewHours drops hour rows before the given "2006-01-02T15".
	PruneViewHours(context.Context, string) error
	Analytics(context.Context, AnalyticsQuery) (Analytics, error)
	// ViewTotals sums views per page since a day, for the page list.
	ViewTotals(ctx context.Context, ids []string, fromDay string) (map[string]int64, error)
	Usage(context.Context, string, time.Time) (MonthUsage, error)
	DeleteExpiredCredentials(context.Context, time.Time) error
}

type AuthStore interface {
	// SignIn links a verified identity by normalized email and creates a personal
	// tenant only for a new user. All steps are one transaction.
	SignIn(context.Context, string, string, string, string, time.Time) (User, Tenant, error)
	User(context.Context, string) (User, error)
	Identities(context.Context, string) ([]Identity, error)
	CreateSession(context.Context, Session) error
	Session(context.Context, string) (Session, error)
	RefreshSession(context.Context, string, time.Time, time.Time) error
	SwitchTenant(context.Context, string, string, string) error
	DeleteSession(context.Context, string) error
	CreateMagicLink(context.Context, MagicLink) error
	ConsumeMagicLink(context.Context, string, time.Time) (MagicLink, error)
	CreateOAuthState(context.Context, string, time.Time, time.Time) error
	ConsumeOAuthState(context.Context, string, time.Time) error
}
