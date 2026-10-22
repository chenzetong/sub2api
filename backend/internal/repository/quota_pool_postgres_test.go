package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// Runs against a disposable PostgreSQL database without requiring Docker. Never
// point SUB2API_QUOTA_POOL_TEST_DSN at a production database: tests create an
// isolated database and apply the application's migrations inside it.
func poolPostgresDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("SUB2API_QUOTA_POOL_TEST_DSN")
	if dsn == "" {
		t.Skip("set SUB2API_QUOTA_POOL_TEST_DSN to run real PostgreSQL pool tests")
	}
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	database := "pool_test_" + fmt.Sprint(time.Now().UnixNano())
	_, err = admin.Exec("CREATE DATABASE " + database)
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn+" dbname="+database)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = db.Close()
		_, _ = admin.Exec("DROP DATABASE " + database + " WITH (FORCE)")
		_ = admin.Close()
	})
	require.NoError(t, ApplyMigrations(context.Background(), db))
	return db
}

type poolPGFixture struct {
	db                        *sql.DB
	repo                      service.QuotaPoolRepository
	user, other, account, key int64
	observed                  time.Time
}

func poolPostgresFixture(t *testing.T, db *sql.DB) *poolPGFixture {
	t.Helper()
	f := &poolPGFixture{db: db, repo: NewQuotaPoolRepository(nil, db), observed: time.Now().UTC().Add(-time.Minute)}
	require.NoError(t, db.QueryRow("INSERT INTO users(email,password_hash,balance,frozen_balance) VALUES($1,'hash',80,5) RETURNING id", uuid.NewString()+"@example.test").Scan(&f.user))
	require.NoError(t, db.QueryRow("INSERT INTO users(email,password_hash) VALUES($1,'hash') RETURNING id", uuid.NewString()+"@example.test").Scan(&f.other))
	extra, _ := json.Marshal(map[string]any{"codex_7d_used_percent": 50, "codex_7d_reset_at": time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339Nano), "codex_5h_used_percent": 20, "codex_5h_reset_at": time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano), "codex_usage_updated_at": f.observed.Format(time.RFC3339Nano)})
	require.NoError(t, db.QueryRow("INSERT INTO accounts(name,platform,type,credentials,extra) VALUES('pool account','openai','oauth',$1,$2) RETURNING id", `{"chatgpt_account_id":"pool-identity"}`, extra).Scan(&f.account))
	require.NoError(t, db.QueryRow("INSERT INTO api_keys(user_id,key,name) VALUES($1,$2,'test') RETURNING id", f.user, "sk-"+uuid.NewString()).Scan(&f.key))
	for _, row := range []struct {
		user int64
		cost float64
	}{{f.user, 20}, {f.other, 80}} {
		_, err := db.Exec("INSERT INTO usage_logs(user_id,api_key_id,account_id,request_id,model,total_cost,actual_cost,account_stats_cost,created_at) VALUES($1,$2,$3,$4,'gpt-test',$5,$5,$5,$6)", row.user, f.key, f.account, uuid.NewString(), row.cost, f.observed.Add(-time.Hour))
		require.NoError(t, err)
	}
	state, err := f.repo.Read(context.Background())
	require.NoError(t, err)
	require.NoError(t, f.repo.SaveAllocation(context.Background(), service.QuotaPoolAllocation{Revision: state.Allocation.Revision, Pools: []service.QuotaPool{{Name: "pool", AccountIDs: []int64{f.account}, Shares: []service.QuotaPoolShare{{UserID: f.user, SharePercent: 50}}}}}))
	return f
}
func (f *poolPGFixture) sync(key string) (*service.QuotaPoolPreview, error) {
	return f.repo.Process(context.Background(), service.QuotaPoolProcessOptions{Apply: true, OperationKey: key, Source: "manual"}, service.EvaluateQuotaPools)
}
func (f *poolPGFixture) balance(t *testing.T) float64 {
	t.Helper()
	var balance float64
	require.NoError(t, f.db.QueryRow("SELECT balance FROM users WHERE id=$1", f.user).Scan(&balance))
	return balance
}
func (f *poolPGFixture) command() *service.UsageBillingCommand {
	log := &service.UsageLog{UserID: f.user, APIKeyID: f.key, AccountID: f.account, RequestID: uuid.NewString(), Model: "gpt-test", TotalCost: 10, ActualCost: 10, RateMultiplier: 1, CreatedAt: time.Now().UTC()}
	return &service.UsageBillingCommand{RequestID: log.RequestID, APIKeyID: f.key, UserID: f.user, AccountID: f.account, AccountType: service.AccountTypeOAuth, BalanceCost: 10, AtomicUsageLog: log}
}

func TestQuotaPoolPostgres(t *testing.T) {
	db := poolPostgresDB(t)
	t.Run("snapshot cutoff, zero reuse, idempotency, revocation", func(t *testing.T) {
		f := poolPostgresFixture(t, db)
		p, err := f.sync("first")
		require.NoError(t, err)
		require.Equal(t, 75.0, f.balance(t))
		require.Equal(t, 200.0, *p.Accounts[0].Estimate.TotalCost)
		_, err = db.Exec("UPDATE users SET balance=123 WHERE id=$1", f.user)
		require.NoError(t, err)
		_, err = f.sync("first")
		require.NoError(t, err)
		require.Equal(t, 123.0, f.balance(t))
		cmd := f.command()
		_, err = NewUsageBillingRepository(nil, db).Apply(context.Background(), cmd)
		require.NoError(t, err)
		require.True(t, cmd.AtomicUsageLog.PersistedWithBilling)
		p, err = f.sync("second")
		require.NoError(t, err)
		require.Equal(t, 65.0, f.balance(t))
		require.Equal(t, 200.0, *p.Accounts[0].Estimate.TotalCost, "post-observation usage must not inflate capacity")
		_, err = db.Exec("UPDATE accounts SET extra=jsonb_set(extra,'{codex_7d_used_percent}','0') WHERE id=$1", f.account)
		require.NoError(t, err)
		p, err = f.sync("zero")
		require.NoError(t, err)
		require.Equal(t, "last_valid", p.Accounts[0].Estimate.Source)
		require.Equal(t, 65.0, f.balance(t))
		state, err := f.repo.Read(context.Background())
		require.NoError(t, err)
		require.NoError(t, f.repo.SaveAllocation(context.Background(), service.QuotaPoolAllocation{Revision: state.Allocation.Revision, Pools: []service.QuotaPool{}}))
		p, err = f.sync("release")
		require.NoError(t, err)
		require.Equal(t, 0.0, f.balance(t))
		require.True(t, p.Users[0].Release)
		_, err = db.Exec("UPDATE users SET balance=42 WHERE id=$1", f.user)
		require.NoError(t, err)
		_, err = f.sync("after-release")
		require.NoError(t, err)
		require.Equal(t, 42.0, f.balance(t))
		var audits int
		require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM quota_pool_contract_revisions").Scan(&audits))
		require.Equal(t, 2, audits)
	})
	t.Run("atomic usage insertion rolls back billing", func(t *testing.T) {
		f := poolPostgresFixture(t, db)
		_, err := db.Exec(`CREATE FUNCTION reject_pool_usage() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected log failure'; END $$;
			CREATE TRIGGER reject_pool_usage BEFORE INSERT ON usage_logs FOR EACH ROW EXECUTE FUNCTION reject_pool_usage()`)
		require.NoError(t, err)
		cmd := f.command()
		_, err = NewUsageBillingRepository(nil, db).Apply(context.Background(), cmd)
		require.Error(t, err)
		require.Equal(t, 80.0, f.balance(t))
		require.False(t, cmd.AtomicUsageLog.PersistedWithBilling)
		var count int
		require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM usage_billing_dedup WHERE request_id=$1", cmd.RequestID).Scan(&count))
		require.Zero(t, count)
		_, err = db.Exec("DROP TRIGGER reject_pool_usage ON usage_logs; DROP FUNCTION reject_pool_usage()")
		require.NoError(t, err)
		_, err = NewUsageBillingRepository(nil, db).Apply(context.Background(), cmd)
		require.NoError(t, err)
		require.Equal(t, 70.0, f.balance(t))
	})
	t.Run("sync waits for debit and its usage fact", func(t *testing.T) {
		f := poolPostgresFixture(t, db)
		_, err := f.sync("concurrent-initial")
		require.NoError(t, err)
		gate, err := db.Conn(context.Background())
		require.NoError(t, err)
		defer func() { require.NoError(t, gate.Close()) }()
		_, err = gate.ExecContext(context.Background(), "SELECT pg_advisory_lock(987654321)")
		require.NoError(t, err)
		_, err = db.Exec(`CREATE FUNCTION hold_pool_usage() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(987654321); RETURN NEW; END $$;
			CREATE TRIGGER hold_pool_usage BEFORE INSERT ON usage_logs FOR EACH ROW EXECUTE FUNCTION hold_pool_usage()`)
		require.NoError(t, err)
		defer func() {
			_, _ = gate.ExecContext(context.Background(), "SELECT pg_advisory_unlock(987654321)")
			_, _ = db.Exec("DROP TRIGGER hold_pool_usage ON usage_logs; DROP FUNCTION hold_pool_usage()")
		}()
		billed := make(chan error, 1)
		go func() {
			_, e := NewUsageBillingRepository(nil, db).Apply(context.Background(), f.command())
			billed <- e
		}()
		require.Eventually(t, func() bool {
			var waiting bool
			e := db.QueryRow("SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND objid=987654321 AND NOT granted)").Scan(&waiting)
			return e == nil && waiting
		}, 5*time.Second, 10*time.Millisecond)
		synced := make(chan error, 1)
		go func() { _, e := f.sync("concurrent-final"); synced <- e }()
		select {
		case err := <-synced:
			t.Fatalf("sync did not wait for uncommitted debit: %v", err)
		case <-time.After(100 * time.Millisecond):
		}
		_, err = gate.ExecContext(context.Background(), "SELECT pg_advisory_unlock(987654321)")
		require.NoError(t, err)
		require.NoError(t, <-billed)
		require.NoError(t, <-synced)
		require.Equal(t, 65.0, f.balance(t))
	})
	t.Run("duplicate runs and failed transactions", func(t *testing.T) {
		f := poolPostgresFixture(t, db)
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); _, e := f.sync("same-operation"); errs <- e }()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			require.NoError(t, err)
		}
		var count int
		require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM quota_pool_sync_runs WHERE operation_key='same-operation'").Scan(&count))
		require.Equal(t, 1, count)
		_, err := db.Exec(`CREATE FUNCTION reject_pool_run() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected run failure'; END $$;
			CREATE TRIGGER reject_pool_run BEFORE INSERT ON quota_pool_sync_runs FOR EACH ROW WHEN (NEW.status<>'failed') EXECUTE FUNCTION reject_pool_run()`)
		require.NoError(t, err)
		_, err = db.Exec("UPDATE users SET balance=123 WHERE id=$1", f.user)
		require.NoError(t, err)
		_, err = f.sync("retry-operation")
		require.Error(t, err)
		require.Equal(t, 123.0, f.balance(t))
		var status string
		require.NoError(t, db.QueryRow("SELECT status FROM quota_pool_sync_runs WHERE operation_key='retry-operation'").Scan(&status))
		require.Equal(t, "failed", status)
		_, err = db.Exec("DROP TRIGGER reject_pool_run ON quota_pool_sync_runs; DROP FUNCTION reject_pool_run()")
		require.NoError(t, err)
		_, err = f.sync("retry-operation")
		require.NoError(t, err)
		require.Equal(t, 75.0, f.balance(t))
	})
	t.Run("identity replacement clears history and rejects stale writers", func(t *testing.T) {
		f := poolPostgresFixture(t, db)
		_, err := f.sync("identity-initial")
		require.NoError(t, err)
		history, err := f.repo.Prediction(context.Background(), f.account)
		require.NoError(t, err)
		require.NotNil(t, history)
		_, err = db.Exec("UPDATE accounts SET credentials=credentials || '{\"access_token\":\"rotated\"}'::jsonb WHERE id=$1", f.account)
		require.NoError(t, err)
		preserved, err := f.repo.Prediction(context.Background(), f.account)
		require.NoError(t, err)
		require.NotNil(t, preserved, "token rotation must preserve stable-identity history")
		_, err = db.Exec("UPDATE accounts SET credentials=credentials || '{\"chatgpt_account_id\":\"replacement\"}'::jsonb WHERE id=$1", f.account)
		require.NoError(t, err)
		cleared, err := f.repo.Prediction(context.Background(), f.account)
		require.NoError(t, err)
		require.Nil(t, cleared)
		require.ErrorIs(t, f.repo.SavePrediction(context.Background(), history), service.ErrQuotaPoolConflict)
		p, err := f.sync("identity-replaced")
		require.NoError(t, err)
		require.Equal(t, "missing_snapshot", p.Accounts[0].Estimate.Reason)
		require.Equal(t, "skipped", p.Users[0].Status)
		require.Equal(t, 75.0, f.balance(t))
	})
	t.Run("contract versions and scheduled multi-instance claims", func(t *testing.T) {
		f := poolPostgresFixture(t, db)
		state, err := f.repo.Read(context.Background())
		require.NoError(t, err)
		_, err = f.repo.Process(context.Background(), service.QuotaPoolProcessOptions{Apply: true, OperationKey: "stale-preview", Source: "manual", ExpectedRevision: state.Allocation.Revision - 1}, service.EvaluateQuotaPools)
		require.ErrorIs(t, err, service.ErrQuotaPoolConflict)
		require.Equal(t, 80.0, f.balance(t))
		p, err := f.repo.Process(context.Background(), service.QuotaPoolProcessOptions{Apply: true, DueOnly: true, Source: "scheduled"}, service.EvaluateQuotaPools)
		require.NoError(t, err)
		require.Nil(t, p, "automatic sync defaults to disabled")
		require.NoError(t, f.repo.UpdateSettings(context.Background(), service.QuotaPoolSettings{Enabled: true, IntervalMinutes: 5}))
		_, err = db.Exec("UPDATE quota_pool_config SET next_run_at=NOW()-INTERVAL '16 minutes' WHERE id=1")
		require.NoError(t, err)
		results := make(chan *service.QuotaPoolPreview, 2)
		errs := make(chan error, 2)
		for i := 0; i < 2; i++ {
			// Separate instances compete on the persistent configuration lock.
			go func() {
				p, err := NewQuotaPoolRepository(nil, db).Process(context.Background(), service.QuotaPoolProcessOptions{Apply: true, DueOnly: true, Source: "scheduled"}, service.EvaluateQuotaPools)
				results <- p
				errs <- err
			}()
		}
		applied := 0
		for i := 0; i < 2; i++ {
			require.NoError(t, <-errs)
			if <-results != nil {
				applied++
			}
		}
		require.Equal(t, 1, applied)
		require.Equal(t, 75.0, f.balance(t))
		var next time.Time
		require.NoError(t, db.QueryRow("SELECT next_run_at FROM quota_pool_config WHERE id=1").Scan(&next))
		require.WithinRange(t, next, time.Now(), time.Now().Add(5*time.Minute))
	})
	t.Run("independent windows, overdraw, precision and zero rollover", func(t *testing.T) {
		f := poolPostgresFixture(t, db)
		now := time.Now().UTC()
		_, err := db.Exec("UPDATE usage_logs SET account_stats_cost=CASE WHEN user_id=$1 THEN 90 ELSE 10 END WHERE account_id=$2", f.user, f.account)
		require.NoError(t, err)
		extra, _ := json.Marshal(map[string]any{"codex_7d_used_percent": 25, "codex_7d_reset_at": now.Add(6 * 24 * time.Hour).Format(time.RFC3339Nano), "codex_usage_updated_at": f.observed.Format(time.RFC3339Nano)})
		var second int64
		require.NoError(t, db.QueryRow("INSERT INTO accounts(name,platform,type,credentials,extra) VALUES('second','openai','oauth','{\"chatgpt_account_id\":\"second\"}',$1) RETURNING id", extra).Scan(&second))
		for _, row := range []struct {
			user int64
			cost float64
			at   time.Time
		}{{f.user, 10, now.Add(-time.Hour)}, {f.other, 40, now.Add(-time.Hour)}, {f.user, 999, now.Add(-48 * time.Hour)}} {
			_, err = db.Exec("INSERT INTO usage_logs(user_id,api_key_id,account_id,request_id,model,total_cost,actual_cost,account_stats_cost,created_at) VALUES($1,$2,$3,$4,'test',$5,$5,$5,$6)", row.user, f.key, second, uuid.NewString(), row.cost, row.at)
			require.NoError(t, err)
		}
		state, err := f.repo.Read(context.Background())
		require.NoError(t, err)
		pools := []service.QuotaPool{{Name: "first", AccountIDs: []int64{f.account}, Shares: []service.QuotaPoolShare{{UserID: f.user, SharePercent: 33.333}}}, {Name: "second", AccountIDs: []int64{second}, Shares: []service.QuotaPoolShare{{UserID: f.user, SharePercent: 33.333}}}}
		require.NoError(t, f.repo.SaveAllocation(context.Background(), service.QuotaPoolAllocation{Revision: state.Allocation.Revision, Pools: pools}))
		_, err = f.sync("window-initial")
		require.NoError(t, err)
		require.Equal(t, 28.332, f.balance(t), "offset negative rights before clamping and use each account's own window")
		rollover, _ := json.Marshal(map[string]any{"codex_7d_used_percent": 0, "codex_7d_reset_at": now.Add(7*24*time.Hour - 30*time.Second).Format(time.RFC3339Nano), "codex_usage_updated_at": now.Format(time.RFC3339Nano)})
		_, err = db.Exec("UPDATE accounts SET extra=extra || $1::jsonb WHERE id=$2", rollover, f.account)
		require.NoError(t, err)
		_, err = db.Exec("INSERT INTO usage_logs(user_id,api_key_id,account_id,request_id,model,total_cost,actual_cost,created_at) VALUES($1,$2,$3,$4,'test',2,2,$5)", f.user, f.key, f.account, uuid.NewString(), now.Add(-10*time.Second))
		require.NoError(t, err)
		p, err := f.sync("window-rollover")
		require.NoError(t, err)
		require.Equal(t, "last_valid", p.Accounts[0].Estimate.Source)
		require.Equal(t, 116.332, f.balance(t), "reuse capacity across cycles while subtracting the new cycle's cost")
		_, err = db.Exec("UPDATE accounts SET extra=extra - 'codex_7d_reset_at' WHERE id=$1", second)
		require.NoError(t, err)
		_, err = f.sync("window-incomplete")
		require.NoError(t, err)
		require.Equal(t, 116.332, f.balance(t), "an unknown second window must skip the user's complete balance")
	})
}
