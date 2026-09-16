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
