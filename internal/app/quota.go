package app

import "context"

// defaultArtifactsPerTenant matches the API's quota when the config carries
// no limits (tests, or a handler built without config.Load).
const defaultArtifactsPerTenant = 200

// pageQuota is how many live pages a workspace holds against the limit the
// API enforces on publish (LIMIT_ARTIFACTS_PER_TENANT).
type pageQuota struct{ Used, Limit int64 }

func (q pageQuota) Left() int64 { return max(q.Limit-q.Used, 0) }

// Full means the next new page will be refused with QUOTA_EXCEEDED.
func (q pageQuota) Full() bool { return q.Used >= q.Limit }

// Near is 80% of the limit or more, when the dashboard starts warning.
func (q pageQuota) Near() bool { return q.Used*5 >= q.Limit*4 }

func (h *Handler) pageQuota(ctx context.Context, tenantID string) (pageQuota, error) {
	n, err := h.Store.CountArtifacts(ctx, tenantID)
	limit := h.Cfg.Limits.ArtifactsPerTenant
	if limit <= 0 {
		limit = defaultArtifactsPerTenant
	}
	return pageQuota{Used: int64(n), Limit: int64(limit)}, err
}
