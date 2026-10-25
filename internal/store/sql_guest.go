package store

import (
	"context"
	"time"

	"github.com/anup-a/byagent/internal/guest"
	"github.com/anup-a/byagent/internal/ids"
)

// CreateGuest makes a personal tenant on the guest plan and its one key, in
// one transaction. The tenant has no members: the key is the only way in
// until someone claims it.
func (s *sqlStore) CreateGuest(ctx context.Context, hash, prefix string, now time.Time) (Key, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Key{}, err
	}
	defer tx.Rollback()
	tenantID := "t_" + ids.NewArtifactID()
	if _, err = tx.ExecContext(ctx, s.q(`INSERT INTO tenants(id,kind,name,plan,created_at) VALUES (?,'personal','Guest',?,?)`), tenantID, guest.Plan, now); err != nil {
		return Key{}, err
	}
	k, err := s.insertKey(ctx, tx, tenantID, hash, "guest@local", guest.KeyName, prefix, now)
	if err != nil {
		return k, err
	}
	return k, tx.Commit()
}

// ClaimGuest moves a guest workspace's pages into dest and revokes its key.
// The plan check and the move share one transaction, so two claims of the
// same link cannot both win.
func (s *sqlStore) ClaimGuest(ctx context.Context, guestTenantID, dest string, now time.Time) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, s.q(`UPDATE tenants SET plan=? WHERE id=? AND plan=? AND created_at>?`),
		guest.ClaimedPlan, guestTenantID, guest.Plan, now.Add(-guest.TTL))
	if err := affected(res, err); err != nil {
		return 0, err
	}
	res, err = tx.ExecContext(ctx, s.q(`UPDATE artifacts SET tenant_id=?, expires_at=NULL WHERE tenant_id=? AND deleted_at IS NULL AND expires_at>?`), dest, guestTenantID, now)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	// The key is revoked, not moved. A claim link can be sent to someone
	// else, and moving the key would hand whoever holds it write access to
	// the workspace of whoever clicked.
	if _, err := tx.ExecContext(ctx, s.q(`UPDATE keys SET revoked_at=? WHERE tenant_id=? AND revoked_at IS NULL`), now, guestTenantID); err != nil {
		return 0, err
	}
	return int(n), tx.Commit()
}

// ExpireGuests turns expiry into a real deletion: the page is marked deleted
// at its deadline, and an unclaimed guest key past its deadline is revoked,
// which the credential GC removes 30 days later.
func (s *sqlStore) ExpireGuests(ctx context.Context, now time.Time) error {
	if _, err := s.db.ExecContext(ctx, s.q(`UPDATE artifacts SET deleted_at=expires_at, updated_at=expires_at WHERE expires_at<=? AND deleted_at IS NULL`), now); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, s.q(`UPDATE keys SET revoked_at=? WHERE revoked_at IS NULL AND tenant_id IN (SELECT id FROM tenants WHERE plan=? AND created_at<=?)`), now, guest.Plan, now.Add(-guest.TTL))
	return err
}
