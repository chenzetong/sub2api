package service

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func poolTestAccount(id int64, now time.Time, percent float64, offset time.Duration) *Account {
	return &Account{ID: id, Name: "pool account", Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Credentials: map[string]any{"chatgpt_account_id": "identity"}, Extra: map[string]any{"codex_7d_used_percent": percent, "codex_7d_reset_at": now.Add(offset).Format(time.RFC3339Nano), "codex_usage_updated_at": now.Add(-time.Minute).Format(time.RFC3339Nano)}}
}

func TestQuotaPoolEstimateZeroAndInvalidSnapshots(t *testing.T) {
	now := time.Now().UTC()
	a := poolTestAccount(1, now, 25, 24*time.Hour)
	e, history := EstimateQuotaPoolAccount(a, 100, nil, now)
	require.Equal(t, 400.0, *e.TotalCost)
	require.Equal(t, "current", e.Source)
	a.Extra["codex_7d_used_percent"] = 0.0
	a.Extra["codex_7d_reset_at"] = now.Add(6 * 24 * time.Hour).Format(time.RFC3339Nano)
	e, newHistory := EstimateQuotaPoolAccount(a, 1, history, now)
	require.Equal(t, 400.0, *e.TotalCost)
	require.Equal(t, "last_valid", e.Source)
	require.Nil(t, newHistory)
	e, _ = EstimateQuotaPoolAccount(a, 0, nil, now)
	require.Nil(t, e.TotalCost)
	require.Equal(t, "no_previous_prediction", e.Reason)
	a.Credentials["chatgpt_account_id"] = "replacement"
	e, _ = EstimateQuotaPoolAccount(a, 0, history, now)
	require.Nil(t, e.TotalCost)
	for _, tc := range []struct {
		name   string
		mutate func(*Account)
		reason string
	}{
		{"missing", func(a *Account) { delete(a.Extra, "codex_7d_used_percent") }, "missing_snapshot"},
		{"expired", func(a *Account) { a.Extra["codex_7d_reset_at"] = now.Add(-time.Hour).Format(time.RFC3339Nano) }, "expired_snapshot"},
		{"unknown", func(a *Account) { delete(a.Extra, "codex_7d_reset_at") }, "unknown_window"},
		{"NaN", func(a *Account) { a.Extra["codex_7d_used_percent"] = math.NaN() }, "invalid_snapshot"},
		{"future", func(a *Account) { a.Extra["codex_usage_updated_at"] = now.Add(time.Hour).Format(time.RFC3339Nano) }, "missing_observation_time"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := poolTestAccount(1, now, 0, 24*time.Hour)
			tc.mutate(a)
			e, _ := EstimateQuotaPoolAccount(a, 10, history, now)
			require.Nil(t, e.TotalCost)
			require.Equal(t, tc.reason, e.Reason)
		})
	}
}

func TestQuotaPoolSettlementAggregatesBeforeClamping(t *testing.T) {
	now := time.Now().UTC()
	a, b := poolTestAccount(1, now, 50, 24*time.Hour), poolTestAccount(2, now, 25, 72*time.Hour)
	f := &QuotaPoolFacts{Allocation: QuotaPoolAllocation{Revision: 3, Pools: []QuotaPool{{ID: 1, AccountIDs: []int64{1}, Shares: []QuotaPoolShare{{UserID: 7, SharePercent: 50}}}, {ID: 2, AccountIDs: []int64{2}, Shares: []QuotaPoolShare{{UserID: 7, SharePercent: 50}}}}}, Accounts: []*Account{a, b}, Users: []QuotaPoolUser{{ID: 7, Managed: true, FrozenBalance: 5}}, ObservedCosts: map[int64]float64{1: 50, 2: 25}, Costs: map[int64]map[int64]float64{1: {7: 80}, 2: {7: 10}}}
	p, _ := EvaluateQuotaPools(f, now)
	require.Len(t, p.Users, 1)
	require.Equal(t, 100.0, p.Users[0].Entitlement)
	require.Equal(t, 90.0, p.Users[0].Consumed)
	require.Equal(t, 5.0, *p.Users[0].TargetBalance)
	delete(b.Extra, "codex_7d_used_percent")
	p, _ = EvaluateQuotaPools(f, now)
	require.Nil(t, p.Users[0].TargetBalance)
	require.Equal(t, "skipped", p.Users[0].Status)
	f.Allocation.Pools = nil
	p, _ = EvaluateQuotaPools(f, now)
	require.Equal(t, 0.0, *p.Users[0].TargetBalance)
	require.True(t, p.Users[0].Release)
}

func TestQuotaPoolContractValidation(t *testing.T) {
	valid := func() QuotaPoolAllocation {
		return QuotaPoolAllocation{Pools: []QuotaPool{{Name: "pool", AccountIDs: []int64{1, 2}, Shares: []QuotaPoolShare{{UserID: 1, SharePercent: 33.333}, {UserID: 2, SharePercent: 66.667}}}}}
	}
	require.NoError(t, ValidateQuotaPoolAllocation(valid()))
	for _, mutate := range []func(*QuotaPoolAllocation){
		func(a *QuotaPoolAllocation) { a.Pools[0].Shares[0].SharePercent = 33.334 },
		func(a *QuotaPoolAllocation) { a.Pools[0].Shares[0].SharePercent = 0.0001 },
		func(a *QuotaPoolAllocation) { a.Pools[0].Shares[0].SharePercent = math.Inf(1) },
		func(a *QuotaPoolAllocation) { a.Pools[0].AccountIDs = []int64{1, 1} },
		func(a *QuotaPoolAllocation) { a.Pools[0].Shares[1].UserID = 1 },
	} {
		a := valid()
		mutate(&a)
		require.Error(t, ValidateQuotaPoolAllocation(a))
	}
}

type poolReadRepo struct {
	QuotaPoolRepository
	facts *QuotaPoolFacts
}

func (r poolReadRepo) Read(context.Context) (*QuotaPoolFacts, error) { return r.facts, nil }

func TestQuotaPoolVisibilityAndDTO(t *testing.T) {
	now := time.Now().UTC()
	a := poolTestAccount(1, now, 20, 24*time.Hour)
	a.Credentials["access_token"] = "secret"
	a.Extra["private"] = "private"
	f := &QuotaPoolFacts{Allocation: QuotaPoolAllocation{Pools: []QuotaPool{{ID: 5, Name: "visible", AccountIDs: []int64{1}, Shares: []QuotaPoolShare{{UserID: 7, SharePercent: 10}}}}}, Accounts: []*Account{a}, ObservedCosts: map[int64]float64{1: 20}}
	s := NewQuotaPoolService(poolReadRepo{facts: f}, nil, nil, nil)
	views, err := s.Accounts(context.Background(), 7)
	require.NoError(t, err)
	require.Len(t, views, 1)
	require.Equal(t, 10.0, views[0].SharePercent)
	encoded, err := json.Marshal(views)
	require.NoError(t, err)
	for _, secret := range []string{"credentials", "access_token", "secret", "private", "proxy", "notes"} {
		require.NotContains(t, string(encoded), secret)
	}
	_, err = s.Account(context.Background(), 8, 1)
	require.ErrorIs(t, err, ErrQuotaPoolAccountNotFound)
	a.OwnerUserID = new(int64)
	_, err = s.Account(context.Background(), 7, 1)
	require.ErrorIs(t, err, ErrQuotaPoolAccountNotFound, "a pool link cannot expose a privately owned account")
	a.OwnerUserID = nil
	f.Allocation.Pools[0].Shares[0].SharePercent = 0
	_, err = s.Account(context.Background(), 7, 1)
	require.ErrorIs(t, err, ErrQuotaPoolAccountNotFound)
}

func TestQuotaPoolMoneyPrecisionAndUnallocatedShares(t *testing.T) {
	now := time.Now().UTC()
	f := &QuotaPoolFacts{
		Allocation:    QuotaPoolAllocation{Pools: []QuotaPool{{ID: 1, AccountIDs: []int64{1}, Shares: []QuotaPoolShare{{UserID: 7, SharePercent: 33.333}}}}},
		Accounts:      []*Account{poolTestAccount(1, now, 30, 24*time.Hour)},
		Users:         []QuotaPoolUser{{ID: 7, Managed: true, FrozenBalance: 0.1}},
		ObservedCosts: map[int64]float64{1: 10}, Costs: map[int64]map[int64]float64{1: {7: 0.2}},
	}
	p, _ := EvaluateQuotaPools(f, now)
	require.Equal(t, 11.111, p.Users[0].Entitlement)
	require.Equal(t, 10.811, *p.Users[0].TargetBalance, "unallocated rights must not be redistributed")
	f.Costs[1][7] = 20
	p, _ = EvaluateQuotaPools(f, now)
	require.Equal(t, 0.0, *p.Users[0].TargetBalance)
}

func TestQuotaPoolUsageBillingDoesNotWriteAfterFailureOrDeduplication(t *testing.T) {
	for _, failure := range []bool{true, false} {
		log := &UsageLog{UserID: 7, AccountID: 1, APIKeyID: 2}
		logs := &openAIRecordUsageLogRepoStub{}
		repo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: false}}
		if failure {
			repo.err = errors.New("transaction rolled back")
		}
		_, err := applyUsageBilling(context.Background(), "atomic", log, &postUsageBillingParams{
			Cost: &CostBreakdown{ActualCost: 1}, User: &User{ID: 7}, APIKey: &APIKey{ID: 2},
			Account: &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth},
		}, &billingDeps{deferredService: &DeferredService{}}, repo)
		if failure {
			require.Error(t, err)
		} else {
			require.NoError(t, err)
		}
		require.Same(t, log, repo.lastCmd.AtomicUsageLog)
		writeUsageLogBestEffort(context.Background(), logs, log, "test")
		require.Zero(t, logs.calls, "the billing transaction exclusively owns this usage fact")
	}
}
