package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type quotaPoolRepository struct{ db *sql.DB }

func NewQuotaPoolRepository(_ *dbent.Client, db *sql.DB) service.QuotaPoolRepository {
	return &quotaPoolRepository{db: db}
}

func (r *quotaPoolRepository) Read(ctx context.Context) (*service.QuotaPoolFacts, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	f, err := loadQuotaPoolFacts(ctx, tx, false, true, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return f, nil
}

func loadQuotaPoolFacts(ctx context.Context, q sqlExecutor, lock, candidates bool, now time.Time) (*service.QuotaPoolFacts, error) {
	f := &service.QuotaPoolFacts{Allocation: service.QuotaPoolAllocation{Pools: []service.QuotaPool{}}, Accounts: []*service.Account{}, Users: []service.QuotaPoolUser{}, Predictions: map[int64]*service.QuotaPoolPrediction{}, ObservedCosts: map[int64]float64{}, Costs: map[int64]map[int64]float64{}, WindowStats: map[int64]map[string]*service.WindowStats{}}
	configQuery := "SELECT revision, enabled, interval_minutes, next_run_at FROM quota_pool_config WHERE id=1"
	if lock {
		configQuery += " FOR UPDATE"
	}
	if err := scanSingleRow(ctx, q, configQuery, nil, &f.Allocation.Revision, &f.Settings.Enabled, &f.Settings.IntervalMinutes, &f.Settings.NextRunAt); err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, `SELECT p.id,p.name,p.revision,
		COALESCE((SELECT jsonb_agg(account_id ORDER BY account_id) FROM quota_pool_accounts WHERE pool_id=p.id),'[]'::jsonb),
		COALESCE((SELECT jsonb_agg(jsonb_build_object('user_id',user_id,'share_percent',share_percent) ORDER BY user_id) FROM quota_pool_shares WHERE pool_id=p.id),'[]'::jsonb)
		FROM quota_pools p ORDER BY p.id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var p service.QuotaPool
		var accounts, shares []byte
		if err = rows.Scan(&p.ID, &p.Name, &p.Revision, &accounts, &shares); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err = json.Unmarshal(accounts, &p.AccountIDs); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err = json.Unmarshal(shares, &p.Shares); err != nil {
			_ = rows.Close()
			return nil, err
		}
		f.Allocation.Pools = append(f.Allocation.Pools, p)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	accountQuery := `SELECT id,name,platform,type,status,credentials,extra,owner_user_id FROM accounts
		WHERE deleted_at IS NULL AND (($1 AND platform='openai' AND type='oauth' AND owner_user_id IS NULL) OR id IN (SELECT account_id FROM quota_pool_accounts)) ORDER BY id`
	if lock {
		accountQuery += " FOR SHARE"
	}
	rows, err = q.QueryContext(ctx, accountQuery, candidates)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		a := &service.Account{}
		var credentials, extra []byte
		if err = rows.Scan(&a.ID, &a.Name, &a.Platform, &a.Type, &a.Status, &credentials, &extra, &a.OwnerUserID); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err = json.Unmarshal(credentials, &a.Credentials); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err = json.Unmarshal(extra, &a.Extra); err != nil {
			_ = rows.Close()
			return nil, err
		}
		f.Accounts = append(f.Accounts, a)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	userQuery := `SELECT u.id,COALESCE(NULLIF(u.username,''),u.email),u.balance,u.frozen_balance,
		EXISTS(SELECT 1 FROM quota_pool_managed_users m WHERE m.user_id=u.id AND m.managed)
		FROM users u WHERE u.deleted_at IS NULL AND ($1 OR EXISTS(SELECT 1 FROM quota_pool_managed_users m WHERE m.user_id=u.id AND m.managed)
		OR EXISTS(SELECT 1 FROM quota_pool_shares s WHERE s.user_id=u.id AND s.share_percent>0)) ORDER BY u.id`
	if lock {
		userQuery += " FOR UPDATE OF u"
	}
	rows, err = q.QueryContext(ctx, userQuery, candidates)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var u service.QuotaPoolUser
		if err = rows.Scan(&u.ID, &u.Name, &u.Balance, &u.FrozenBalance, &u.Managed); err != nil {
			_ = rows.Close()
			return nil, err
		}
		f.Users = append(f.Users, u)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	// Capture the cutoff after user locks: all earlier committed billing facts are visible.
	if lock {
		now = time.Now().UTC()
	}
	for _, a := range f.Accounts {
		prediction, readErr := readQuotaPoolPrediction(ctx, q, a.ID)
		if readErr != nil {
			return nil, readErr
		}
		f.Predictions[a.ID] = prediction
		start, _, observed, _, reason := service.QuotaPoolWindow(a, now)
		if reason != "" {
			continue
		}
		rows, err = q.QueryContext(ctx, `SELECT user_id,
			COALESCE(SUM(COALESCE(account_stats_cost,total_cost)*COALESCE(account_rate_multiplier,1)),0),
			COALESCE(SUM(CASE WHEN created_at<=$3 THEN COALESCE(account_stats_cost,total_cost)*COALESCE(account_rate_multiplier,1) ELSE 0 END),0)
			FROM usage_logs WHERE account_id=$1 AND created_at>=$2 AND created_at<=$4 GROUP BY user_id`, a.ID, start, observed, now)
		if err != nil {
			return nil, err
		}
		f.Costs[a.ID] = map[int64]float64{}
		for rows.Next() {
			var userID int64
			var current, sample float64
			if err = rows.Scan(&userID, &current, &sample); err != nil {
				_ = rows.Close()
				return nil, err
			}
			f.Costs[a.ID][userID] = current
			f.ObservedCosts[a.ID] += sample
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return nil, err
		}
		f.WindowStats[a.ID] = map[string]*service.WindowStats{}
		windows := map[string]time.Time{"7d": start}
		if reset, parseErr := time.Parse(time.RFC3339Nano, fmt.Sprint(a.Extra["codex_5h_reset_at"])); parseErr == nil && now.Before(reset) {
			windows["5h"] = reset.Add(-5 * time.Hour)
		}
		for window, since := range windows {
			stats := &service.WindowStats{}
			if err = scanSingleRow(ctx, q, `SELECT COUNT(*),COALESCE(SUM(input_tokens+output_tokens+cache_creation_tokens+cache_read_tokens),0),COALESCE(SUM(COALESCE(account_stats_cost,total_cost)*COALESCE(account_rate_multiplier,1)),0),COALESCE(SUM(total_cost),0),COALESCE(SUM(actual_cost),0) FROM usage_logs WHERE account_id=$1 AND created_at>=$2 AND created_at<=$3`, []any{a.ID, since, now}, &stats.Requests, &stats.Tokens, &stats.Cost, &stats.StandardCost, &stats.UserCost); err != nil {
				return nil, err
			}
			f.WindowStats[a.ID][window] = stats
		}

	}
	return f, nil
}

func (r *quotaPoolRepository) SaveAllocation(ctx context.Context, a service.QuotaPoolAllocation) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var revision int64
	if err = tx.QueryRowContext(ctx, "SELECT revision FROM quota_pool_config WHERE id=1 FOR UPDATE").Scan(&revision); err != nil {
		return err
	}
	if revision != a.Revision {
		return service.ErrQuotaPoolConflict
	}
	// Preserve IDs for unchanged pools so splitting/merging and audit revisions are explicit.
	old := map[int64]bool{}
	rows, err := tx.QueryContext(ctx, "SELECT id FROM quota_pools")
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		old[id] = true
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	for _, p := range a.Pools {
		if p.ID > 0 && !old[p.ID] {
			return service.ErrQuotaPoolConflict
		}
		for _, id := range p.AccountIDs {
			var valid bool
			if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM accounts WHERE id=$1 AND deleted_at IS NULL AND platform='openai' AND type='oauth' AND owner_user_id IS NULL)", id).Scan(&valid); err != nil {
				return err
			}
			if !valid {
				return infraerrors.BadRequest("INVALID_POOL_ACCOUNT", "混池只能包含管理员管理的 OpenAI OAuth 账号")
			}
		}
		for _, share := range p.Shares {
			var valid bool
			if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND deleted_at IS NULL)", share.UserID).Scan(&valid); err != nil {
				return err
			}
			if !valid {
				return infraerrors.BadRequest("INVALID_POOL_USER", "合同包含不存在的用户")
			}
		}
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM quota_pool_accounts"); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM quota_pool_shares"); err != nil {
		return err
	}
	for index, p := range a.Pools {
		id := p.ID
		if id == 0 {
			err = tx.QueryRowContext(ctx, "INSERT INTO quota_pools(name,revision) VALUES($1,$2) RETURNING id", p.Name, revision+1).Scan(&id)
		} else {
			_, err = tx.ExecContext(ctx, "UPDATE quota_pools SET name=$1,revision=$2 WHERE id=$3", p.Name, revision+1, id)
			delete(old, id)
		}
		if err != nil {
			return err
		}
		a.Pools[index].ID = id
		a.Pools[index].Revision = revision + 1
		for _, accountID := range p.AccountIDs {
			if _, err = tx.ExecContext(ctx, "INSERT INTO quota_pool_accounts(pool_id,account_id) VALUES($1,$2)", id, accountID); err != nil {
				return err
			}
		}
		for _, share := range p.Shares {
			if _, err = tx.ExecContext(ctx, "INSERT INTO quota_pool_shares(pool_id,user_id,share_percent) VALUES($1,$2,$3)", id, share.UserID, share.SharePercent); err != nil {
				return err
			}
			if share.SharePercent > 0 {
				if _, err = tx.ExecContext(ctx, "INSERT INTO quota_pool_managed_users(user_id,managed) VALUES($1,TRUE) ON CONFLICT(user_id) DO UPDATE SET managed=TRUE", share.UserID); err != nil {
					return err
				}
			}
		}
	}
	for id := range old {
		if _, err = tx.ExecContext(ctx, "DELETE FROM quota_pools WHERE id=$1", id); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "UPDATE quota_pool_config SET revision=revision+1 WHERE id=1"); err != nil {
		return err
	}
	a.Revision = revision + 1
	audit, err := json.Marshal(a)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO quota_pool_contract_revisions(revision,allocation) VALUES($1,$2)", a.Revision, audit); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *quotaPoolRepository) UpdateSettings(ctx context.Context, s service.QuotaPoolSettings) error {
	_, err := r.db.ExecContext(ctx, `UPDATE quota_pool_config SET enabled=$1,interval_minutes=$2,
		next_run_at=CASE WHEN $1 THEN NOW()+make_interval(mins=>$2) ELSE NULL END WHERE id=1`, s.Enabled, s.IntervalMinutes)
	return err
}

func (r *quotaPoolRepository) Process(ctx context.Context, opts service.QuotaPoolProcessOptions, calculate func(*service.QuotaPoolFacts, time.Time) (*service.QuotaPoolPreview, []*service.QuotaPoolPrediction)) (_ *service.QuotaPoolPreview, err error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
		if err == nil || !opts.Apply || opts.OperationKey == "" {
			return
		}
		reason := "transaction_failed"
		if errors.Is(err, service.ErrQuotaPoolConflict) {
			reason = "contract_changed"
		} else if errors.Is(err, context.DeadlineExceeded) {
			reason = "sync_timeout"
		} else if errors.Is(err, context.Canceled) {
			reason = "sync_cancelled"
		}
		failure := &service.QuotaPoolPreview{Status: "failed", CreatedAt: time.Now().UTC(), OperationKey: opts.OperationKey, Error: reason, Users: []service.QuotaPoolUserResult{}, Accounts: []service.PoolAccountView{}}
		encoded, _ := json.Marshal(failure)
		failureCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, recordErr := r.db.ExecContext(failureCtx, "INSERT INTO quota_pool_sync_runs(operation_key,source,status,result) VALUES($1,$2,'failed',$3) ON CONFLICT(operation_key) DO UPDATE SET status='failed',result=EXCLUDED.result WHERE quota_pool_sync_runs.status='failed'", opts.OperationKey, opts.Source, encoded)
		if recordErr != nil {
			err = errors.Join(err, fmt.Errorf("record failed pool sync: %w", recordErr))
		}
	}()
	// The singleton row is the cross-instance lock shared by edits, manual and scheduled runs.
	var enabled bool
	var interval int
	var next *time.Time
	if err = tx.QueryRowContext(ctx, "SELECT enabled,interval_minutes,next_run_at FROM quota_pool_config WHERE id=1 FOR UPDATE").Scan(&enabled, &interval, &next); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	if opts.DueOnly {
		if !enabled || next == nil || now.Before(*next) {
			return nil, nil
		}
		opts.OperationKey = "scheduled:" + strconv.FormatInt(next.UnixNano(), 10)
	}
	if opts.Apply {
		var previous []byte
		err = tx.QueryRowContext(ctx, "SELECT result FROM quota_pool_sync_runs WHERE operation_key=$1 AND status<>'failed'", opts.OperationKey).Scan(&previous)
		if err == nil {
			var result service.QuotaPoolPreview
			if err = json.Unmarshal(previous, &result); err != nil {
				return nil, err
			}
			return &result, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}
	f, err := loadQuotaPoolFacts(ctx, tx, opts.Apply, false, now)
	if err != nil {
		return nil, err
	}
	if opts.ExpectedRevision > 0 && f.Allocation.Revision != opts.ExpectedRevision {
		return nil, service.ErrQuotaPoolConflict
	}
	now = time.Now().UTC()
	result, predictions := calculate(f, now)
	if !opts.Apply {
		return result, nil
	}
	result.OperationKey = opts.OperationKey
	result.Status = "completed"
	for _, p := range predictions {
		if err = saveQuotaPoolPrediction(ctx, tx, p); err != nil {
			return nil, err
		}
	}
	for i := range result.Users {
		u := &result.Users[i]
		if u.TargetBalance == nil {
			result.Status = "completed_with_skips"
			continue
		}
		if _, err = tx.ExecContext(ctx, "UPDATE users SET balance=$1,updated_at=NOW() WHERE id=$2 AND deleted_at IS NULL", *u.TargetBalance, u.UserID); err != nil {
			return nil, err
		}
		u.Status = "applied"
		if u.Release {
			if _, err = tx.ExecContext(ctx, "UPDATE quota_pool_managed_users SET managed=FALSE WHERE user_id=$1", u.UserID); err != nil {
				return nil, err
			}
		}
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO quota_pool_sync_runs(operation_key,source,status,result) VALUES($1,$2,$3,$4) ON CONFLICT(operation_key) DO UPDATE SET status=EXCLUDED.status,result=EXCLUDED.result,created_at=NOW()", opts.OperationKey, opts.Source, result.Status, encoded); err != nil {
		return nil, err
	}
	if opts.DueOnly {
		// Keep the schedule anchored, skip missed slots instead of issuing catch-up grants.
		step := time.Duration(interval) * time.Minute
		scheduled := next.Add(step)
		if !scheduled.After(now) {
			scheduled = next.Add((now.Sub(*next)/step + 1) * step)
		}
		if _, err = tx.ExecContext(ctx, "UPDATE quota_pool_config SET next_run_at=$1 WHERE id=1", scheduled); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

func readQuotaPoolPrediction(ctx context.Context, q sqlExecutor, id int64) (*service.QuotaPoolPrediction, error) {
	p := &service.QuotaPoolPrediction{AccountID: id}
	err := scanSingleRow(ctx, q, "SELECT identity,capacity,observed_at,window_start,window_end FROM quota_pool_predictions WHERE account_id=$1", []any{id}, &p.Identity, &p.Capacity, &p.ObservedAt, &p.WindowStart, &p.WindowEnd)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return p, err
}

func saveQuotaPoolPrediction(ctx context.Context, q sqlExecutor, p *service.QuotaPoolPrediction) error {
	_, err := q.ExecContext(ctx, `INSERT INTO quota_pool_predictions(account_id,identity,capacity,observed_at,window_start,window_end) VALUES($1,$2,$3,$4,$5,$6)
		ON CONFLICT(account_id) DO UPDATE SET identity=EXCLUDED.identity,capacity=EXCLUDED.capacity,observed_at=EXCLUDED.observed_at,window_start=EXCLUDED.window_start,window_end=EXCLUDED.window_end
		WHERE quota_pool_predictions.identity<>EXCLUDED.identity OR quota_pool_predictions.observed_at<=EXCLUDED.observed_at`, p.AccountID, p.Identity, p.Capacity, p.ObservedAt, p.WindowStart, p.WindowEnd)
	return err
}

func (r *quotaPoolRepository) Prediction(ctx context.Context, id int64) (*service.QuotaPoolPrediction, error) {
	return readQuotaPoolPrediction(ctx, r.db, id)
}
func (r *quotaPoolRepository) SavePrediction(ctx context.Context, p *service.QuotaPoolPrediction) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// An account-page request can finish after credentials have been replaced.
	// Serialize with identity edits so it cannot restore an old prediction.
	a := &service.Account{}
	var credentials []byte
	if err = tx.QueryRowContext(ctx, "SELECT platform,type,owner_user_id,credentials FROM accounts WHERE id=$1 AND deleted_at IS NULL FOR SHARE", p.AccountID).Scan(&a.Platform, &a.Type, &a.OwnerUserID, &credentials); err != nil {
		return err
	}
	if err = json.Unmarshal(credentials, &a.Credentials); err != nil {
		return err
	}
	if !a.IsOpenAIOAuth() || a.OwnerUserID != nil || service.QuotaPoolAccountIdentity(a) != p.Identity {
		return service.ErrQuotaPoolConflict
	}
	if err = saveQuotaPoolPrediction(ctx, tx, p); err != nil {
		return err
	}
	return tx.Commit()
}
func (r *quotaPoolRepository) Cost(ctx context.Context, id int64, start, end time.Time) (float64, error) {
	var cost float64
	err := r.db.QueryRowContext(ctx, "SELECT COALESCE(SUM(COALESCE(account_stats_cost,total_cost)*COALESCE(account_rate_multiplier,1)),0) FROM usage_logs WHERE account_id=$1 AND created_at>=$2 AND created_at<=$3", id, start, end).Scan(&cost)
	return cost, err
}

func (r *quotaPoolRepository) Runs(ctx context.Context) ([]service.QuotaPoolSyncRun, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT id,operation_key,source,status,result,created_at FROM quota_pool_sync_runs ORDER BY id DESC LIMIT 50")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	runs := []service.QuotaPoolSyncRun{}
	for rows.Next() {
		var run service.QuotaPoolSyncRun
		var result []byte
		if err = rows.Scan(&run.ID, &run.OperationKey, &run.Source, &run.Status, &result, &run.CreatedAt); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(result, &run.Result); err != nil {
			return nil, fmt.Errorf("decode quota pool run %d: %w", run.ID, err)
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}
