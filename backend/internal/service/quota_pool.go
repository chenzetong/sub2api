package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/shopspring/decimal"
)

var (
	ErrQuotaPoolConflict        = infraerrors.Conflict("QUOTA_POOL_CONFLICT", "额度分配已变更，请重新加载")
	ErrQuotaPoolAccountNotFound = infraerrors.NotFound("POOL_ACCOUNT_NOT_FOUND", "账号不存在或没有查看权限")
)

type QuotaPoolShare struct {
	UserID       int64   `json:"user_id"`
	SharePercent float64 `json:"share_percent"`
}

type QuotaPool struct {
	ID         int64            `json:"id"`
	Name       string           `json:"name"`
	Revision   int64            `json:"revision"`
	AccountIDs []int64          `json:"account_ids"`
	Shares     []QuotaPoolShare `json:"shares"`
}

type QuotaPoolAllocation struct {
	Revision int64       `json:"revision"`
	Pools    []QuotaPool `json:"pools"`
}

type QuotaPoolSettings struct {
	Enabled         bool       `json:"enabled"`
	IntervalMinutes int        `json:"interval_minutes"`
	NextRunAt       *time.Time `json:"next_run_at"`
}

// QuotaPoolPrediction is a local observation, never an upstream quota override.
type QuotaPoolPrediction struct {
	AccountID   int64     `json:"account_id"`
	Identity    string    `json:"-"`
	Capacity    float64   `json:"capacity"`
	ObservedAt  time.Time `json:"observed_at"`
	WindowStart time.Time `json:"window_start"`
	WindowEnd   time.Time `json:"window_end"`
}

type AccountQuotaEstimate struct {
	TotalCost   *float64   `json:"total_cost"`
	Source      string     `json:"source"`
	Reason      string     `json:"reason,omitempty"`
	ObservedAt  *time.Time `json:"observed_at,omitempty"`
	WindowStart *time.Time `json:"window_start,omitempty"`
	WindowEnd   *time.Time `json:"window_end,omitempty"`
}

type QuotaPoolUser struct {
	ID            int64   `json:"id"`
	Name          string  `json:"name"`
	Balance       float64 `json:"balance"`
	FrozenBalance float64 `json:"frozen_balance"`
	Managed       bool    `json:"-"`
}

// This DTO deliberately contains no credentials, extra maps, notes or proxy data.
type PoolAccountView struct {
	ID           int64                 `json:"id"`
	Name         string                `json:"name"`
	Platform     string                `json:"platform"`
	Type         string                `json:"type"`
	Status       string                `json:"status"`
	PoolID       int64                 `json:"pool_id"`
	PoolName     string                `json:"pool_name"`
	SharePercent float64               `json:"share_percent"`
	FiveHour     *UsageProgress        `json:"five_hour"`
	SevenDay     *UsageProgress        `json:"seven_day"`
	Estimate     *AccountQuotaEstimate `json:"estimate"`
}

type QuotaPoolUserResult struct {
	UserID         int64    `json:"user_id"`
	Name           string   `json:"name"`
	CurrentBalance float64  `json:"current_balance"`
	FrozenBalance  float64  `json:"frozen_balance"`
	Entitlement    float64  `json:"entitlement"`
	Consumed       float64  `json:"consumed"`
	TargetBalance  *float64 `json:"target_balance"`
	Status         string   `json:"status"`
	Reason         string   `json:"reason,omitempty"`
	Release        bool     `json:"release"`
}

type QuotaPoolPreview struct {
	Revision     int64                 `json:"revision"`
	Accounts     []PoolAccountView     `json:"accounts"`
	Users        []QuotaPoolUserResult `json:"users"`
	CreatedAt    time.Time             `json:"created_at"`
	OperationKey string                `json:"operation_key,omitempty"`
	Status       string                `json:"status"`
	Error        string                `json:"error,omitempty"`
}

type QuotaPoolSyncRun struct {
	ID           int64             `json:"id"`
	OperationKey string            `json:"operation_key"`
	Source       string            `json:"source"`
	Status       string            `json:"status"`
	Result       *QuotaPoolPreview `json:"result"`
	CreatedAt    time.Time         `json:"created_at"`
}

// Facts are loaded from one local transaction. Costs use each account's own window.
type QuotaPoolFacts struct {
	Allocation    QuotaPoolAllocation
	Settings      QuotaPoolSettings
	Accounts      []*Account
	Users         []QuotaPoolUser
	Predictions   map[int64]*QuotaPoolPrediction
	ObservedCosts map[int64]float64
	Costs         map[int64]map[int64]float64
	WindowStats   map[int64]map[string]*WindowStats
}

type QuotaPoolProcessOptions struct {
	Apply            bool
	DueOnly          bool
	OperationKey     string
	Source           string
	ExpectedRevision int64
}

type QuotaPoolRepository interface {
	Read(context.Context) (*QuotaPoolFacts, error)
	SaveAllocation(context.Context, QuotaPoolAllocation) error
	UpdateSettings(context.Context, QuotaPoolSettings) error
	Process(context.Context, QuotaPoolProcessOptions, func(*QuotaPoolFacts, time.Time) (*QuotaPoolPreview, []*QuotaPoolPrediction)) (*QuotaPoolPreview, error)
	Prediction(context.Context, int64) (*QuotaPoolPrediction, error)
	SavePrediction(context.Context, *QuotaPoolPrediction) error
	Cost(context.Context, int64, time.Time, time.Time) (float64, error)
	Runs(context.Context) ([]QuotaPoolSyncRun, error)
}

type QuotaPoolService struct {
	repo    QuotaPoolRepository
	usage   UsageLogRepository
	billing *BillingCacheService
	auth    APIKeyAuthCacheInvalidator
	stop    chan struct{}
	once    sync.Once
	wg      sync.WaitGroup
}

func NewQuotaPoolService(repo QuotaPoolRepository, usage UsageLogRepository, billing *BillingCacheService, auth APIKeyAuthCacheInvalidator) *QuotaPoolService {
	return &QuotaPoolService{repo: repo, usage: usage, billing: billing, auth: auth, stop: make(chan struct{})}
}

func (s *QuotaPoolService) Start() {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			_, _ = s.Sync(ctx, "", "scheduled", true, 0)
			cancel()
			select {
			case <-s.stop:
				return
			case <-ticker.C:
			}
		}
	}()
}

func (s *QuotaPoolService) Stop() { s.once.Do(func() { close(s.stop) }); s.wg.Wait() }

func (s *QuotaPoolService) Allocation(ctx context.Context) (*QuotaPoolFacts, error) {
	return s.repo.Read(ctx)
}
func (s *QuotaPoolService) Runs(ctx context.Context) ([]QuotaPoolSyncRun, error) {
	return s.repo.Runs(ctx)
}

func ValidateQuotaPoolAllocation(a QuotaPoolAllocation) error {
	accounts, pools := map[int64]bool{}, map[int64]bool{}
	for _, p := range a.Pools {
		if strings.TrimSpace(p.Name) == "" || len([]rune(p.Name)) > 160 || len(p.AccountIDs) == 0 || p.ID < 0 {
			return infraerrors.BadRequest("INVALID_QUOTA_POOL", "额度池必须有名称和账号")
		}
		if p.ID > 0 && pools[p.ID] {
			return infraerrors.BadRequest("DUPLICATE_QUOTA_POOL", "额度池不能重复")
		}
		pools[p.ID] = true
		for _, id := range p.AccountIDs {
			if id <= 0 || accounts[id] {
				return infraerrors.BadRequest("DUPLICATE_POOL_ACCOUNT", "每个账号只能属于一个额度池")
			}
			accounts[id] = true
		}
		users, total := map[int64]bool{}, decimal.Zero
		for _, share := range p.Shares {
			v := share.SharePercent
			if share.UserID <= 0 || users[share.UserID] || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 100 {
				return infraerrors.BadRequest("INVALID_POOL_SHARE", "份额或用户无效")
			}
			d := decimal.NewFromFloat(v)
			if !d.Equal(d.Round(3)) {
				return infraerrors.BadRequest("INVALID_POOL_SHARE", "份额最多三位小数")
			}
			users[share.UserID] = true
			total = total.Add(d)
		}
		if total.GreaterThan(decimal.NewFromInt(100)) {
			return infraerrors.BadRequest("POOL_SHARE_EXCEEDED", "每池份额合计不能超过 100%")
		}
	}
	return nil
}

func (s *QuotaPoolService) SaveAllocation(ctx context.Context, a QuotaPoolAllocation) error {
	if err := ValidateQuotaPoolAllocation(a); err != nil {
		return err
	}
	return s.repo.SaveAllocation(ctx, a)
}

func (s *QuotaPoolService) UpdateSettings(ctx context.Context, settings QuotaPoolSettings) error {
	if settings.IntervalMinutes < 5 || settings.IntervalMinutes > 1440 {
		return infraerrors.BadRequest("INVALID_SYNC_INTERVAL", "同步间隔必须在 5～1440 分钟之间")
	}
	return s.repo.UpdateSettings(ctx, settings)
}

func (s *QuotaPoolService) Preview(ctx context.Context) (*QuotaPoolPreview, error) {
	return s.repo.Process(ctx, QuotaPoolProcessOptions{}, EvaluateQuotaPools)
}

func (s *QuotaPoolService) Sync(ctx context.Context, key, source string, dueOnly bool, revision int64) (*QuotaPoolPreview, error) {
	if !dueOnly && (strings.TrimSpace(key) == "" || len(key) > 120) {
		return nil, infraerrors.BadRequest("SYNC_KEY_REQUIRED", "同步操作需要有效的幂等标识")
	}
	if !dueOnly && revision <= 0 {
		return nil, ErrQuotaPoolConflict
	}
	p, err := s.repo.Process(ctx, QuotaPoolProcessOptions{Apply: true, DueOnly: dueOnly, OperationKey: key, Source: source, ExpectedRevision: revision}, EvaluateQuotaPools)
	if err != nil {
		logger.LegacyPrintf("service.quota_pool", "sync failed: source=%s err=%v", source, err)
	}
	if err != nil || p == nil {
		return p, err
	}
	invalidateCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	for _, u := range p.Users {
		if u.Status != "applied" {
			continue
		}
		if s.auth != nil {
			s.auth.InvalidateAuthCacheByUserID(invalidateCtx, u.UserID)
		}
		if s.billing != nil {
			if err := s.billing.InvalidateUserBalance(invalidateCtx, u.UserID); err != nil {
				logger.LegacyPrintf("service.quota_pool", "invalidate balance cache failed: user_id=%d err=%v", u.UserID, err)
			}
		}
	}
	return p, nil
}

func QuotaPoolAccountIdentity(a *Account) string {
	// Stable across access/refresh token rotation; replacement identities cannot reuse history.
	identity := a.GetCredential("chatgpt_account_id") + "|" + a.GetChatGPTUserID() + "|" + a.GetCredential("email") + "|" + a.GetOpenAIOrganizationID()
	if identity == "|||" {
		identity = a.GetCredential("refresh_token")
		if identity == "" {
			identity = a.GetCredential("access_token")
		}
	}
	digest := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(digest[:])
}

// QuotaPoolWindow does not turn expired/missing snapshots into real zero observations.
func QuotaPoolWindow(a *Account, now time.Time) (start, end, observed time.Time, percent float64, reason string) {
	if a == nil || !a.IsOpenAIOAuth() || a.OwnerUserID != nil {
		reason = "unsupported_account"
		return
	}
	raw, ok := a.Extra["codex_7d_used_percent"]
	if !ok {
		reason = "missing_snapshot"
		return
	}
	var err error
	percent, err = decimalFromQuotaValue(raw)
	if err != nil || !finiteQuota(percent) || percent < 0 || percent > 100 {
		reason = "invalid_snapshot"
		return
	}
	observed, err = parseTime(fmt.Sprint(a.Extra["codex_usage_updated_at"]))
	if err != nil || observed.After(now) {
		reason = "missing_observation_time"
		return
	}
	progress := buildCodexUsageProgressFromExtra(a.Extra, "7d", now)
	if progress == nil || progress.ResetsAt == nil {
		reason = "unknown_window"
		return
	}
	end = *progress.ResetsAt
	start = end.Add(-7 * 24 * time.Hour)
	if !now.Before(end) || observed.Before(start) || !observed.Before(end) {
		reason = "expired_snapshot"
		return
	}
	return
}

func decimalFromQuotaValue(v any) (float64, error) {
	d, err := decimal.NewFromString(fmt.Sprint(v))
	if err != nil {
		return 0, err
	}
	f, _ := d.Float64()
	return f, nil
}
func finiteQuota(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func EstimateQuotaPoolAccount(a *Account, cost float64, history *QuotaPoolPrediction, now time.Time) (*AccountQuotaEstimate, *QuotaPoolPrediction) {
	start, end, observed, p, reason := QuotaPoolWindow(a, now)
	e := &AccountQuotaEstimate{Source: "unavailable", Reason: reason}
	if reason != "" {
		return e, nil
	}
	e.WindowStart = &start
	e.WindowEnd = &end
	e.ObservedAt = &observed
	identity := QuotaPoolAccountIdentity(a)
	if p == 0 {
		if history == nil || history.Identity != identity || !finiteQuota(history.Capacity) || history.Capacity <= 0 || history.ObservedAt.After(now) {
			e.Reason = "no_previous_prediction"
			return e, nil
		}
		capacity := history.Capacity
		e.TotalCost = &capacity
		e.Source = "last_valid"
		e.ObservedAt = &history.ObservedAt
		return e, nil
	}
	if !finiteQuota(cost) || cost <= 0 {
		e.Reason = "invalid_cost"
		return e, nil
	}
	capacity, _ := decimal.NewFromFloat(cost).Mul(decimal.NewFromInt(100)).Div(decimal.NewFromFloat(p)).Round(8).Float64()
	if !finiteQuota(capacity) || capacity <= 0 || capacity >= 1e12 {
		e.Reason = "invalid_prediction"
		return e, nil
	}
	e.TotalCost = &capacity
	e.Source = "current"
	return e, &QuotaPoolPrediction{AccountID: a.ID, Identity: identity, Capacity: capacity, ObservedAt: observed, WindowStart: start, WindowEnd: end}
}

func (s *QuotaPoolService) Estimate(ctx context.Context, a *Account, now time.Time) (*AccountQuotaEstimate, error) {
	start, _, observed, _, reason := QuotaPoolWindow(a, now)
	if reason != "" {
		return &AccountQuotaEstimate{Source: "unavailable", Reason: reason}, nil
	}
	history, err := s.repo.Prediction(ctx, a.ID)
	if err != nil {
		return nil, err
	}
	cost, err := s.repo.Cost(ctx, a.ID, start, observed)
	if err != nil {
		return nil, err
	}
	estimate, prediction := EstimateQuotaPoolAccount(a, cost, history, now)
	if prediction != nil {
		if err := s.repo.SavePrediction(ctx, prediction); err != nil {
			return nil, err
		}
	}
	return estimate, nil
}

func EvaluateQuotaPools(f *QuotaPoolFacts, now time.Time) (*QuotaPoolPreview, []*QuotaPoolPrediction) {
	result := &QuotaPoolPreview{Revision: f.Allocation.Revision, Accounts: []PoolAccountView{}, Users: []QuotaPoolUserResult{}, CreatedAt: now, Status: "preview"}
	predictions := []*QuotaPoolPrediction{}
	views := map[int64]PoolAccountView{}
	for _, a := range f.Accounts {
		e, prediction := EstimateQuotaPoolAccount(a, f.ObservedCosts[a.ID], f.Predictions[a.ID], now)
		if prediction != nil {
			predictions = append(predictions, prediction)
		}
		view := PoolAccountView{ID: a.ID, Name: a.Name, Platform: a.Platform, Type: a.Type, Status: a.Status, Estimate: e, FiveHour: buildCodexUsageProgressFromExtra(a.Extra, "5h", now), SevenDay: buildCodexUsageProgressFromExtra(a.Extra, "7d", now)}
		if view.SevenDay != nil {
			var cost decimal.Decimal
			for _, v := range f.Costs[a.ID] {
				cost = cost.Add(decimal.NewFromFloat(v))
			}
			c, _ := cost.Float64()
			view.SevenDay.WindowStats = &WindowStats{Cost: c}
		}
		if stats := f.WindowStats[a.ID]; stats != nil {
			if view.FiveHour != nil {
				view.FiveHour.WindowStats = stats["5h"]
			}
			if view.SevenDay != nil {
				view.SevenDay.WindowStats = stats["7d"]
			}
		}
		views[a.ID] = view
	}
	for _, p := range f.Allocation.Pools {
		for _, id := range p.AccountIDs {
			v, ok := views[id]
			if !ok {
				v = PoolAccountView{ID: id, Estimate: &AccountQuotaEstimate{Source: "unavailable", Reason: "missing_account"}}
			}
			v.PoolID = p.ID
			v.PoolName = p.Name
			views[id] = v
			result.Accounts = append(result.Accounts, v)
		}
	}
	for _, u := range f.Users {
		entitlement, consumed := decimal.Zero, decimal.Zero
		r := QuotaPoolUserResult{UserID: u.ID, Name: u.Name, CurrentBalance: u.Balance, FrozenBalance: u.FrozenBalance, Status: "ready", Release: true}
		for _, p := range f.Allocation.Pools {
			for _, share := range p.Shares {
				if share.UserID != u.ID || share.SharePercent <= 0 {
					continue
				}
				r.Release = false
				for _, id := range p.AccountIDs {
					v := views[id]
					if v.Estimate == nil || v.Estimate.TotalCost == nil {
						r.Status = "skipped"
						r.Reason = "incomplete_prediction"
						continue
					}
					entitlement = entitlement.Add(decimal.NewFromFloat(*v.Estimate.TotalCost).Mul(decimal.NewFromFloat(share.SharePercent)).Div(decimal.NewFromInt(100)))
					consumed = consumed.Add(decimal.NewFromFloat(f.Costs[id][u.ID]))
				}
			}
		}
		if r.Release && !u.Managed {
			continue
		}
		r.Entitlement, _ = entitlement.Round(8).Float64()
		r.Consumed, _ = consumed.Round(8).Float64()
		if r.Status == "ready" {
			target := decimal.Max(decimal.Zero, entitlement.Sub(consumed).Sub(decimal.NewFromFloat(u.FrozenBalance)))
			v, _ := target.Round(8).Float64()
			r.TargetBalance = &v
		}
		result.Users = append(result.Users, r)
	}
	return result, predictions
}

func (s *QuotaPoolService) Accounts(ctx context.Context, userID int64) ([]PoolAccountView, error) {
	f, err := s.repo.Read(ctx)
	if err != nil {
		return nil, err
	}
	p, _ := EvaluateQuotaPools(f, time.Now().UTC())
	allowed := map[int64]float64{}
	eligible := map[int64]bool{}
	for _, a := range f.Accounts {
		eligible[a.ID] = a.IsOpenAIOAuth() && a.OwnerUserID == nil
	}
	for _, pool := range f.Allocation.Pools {
		for _, share := range pool.Shares {
			if share.UserID == userID && share.SharePercent > 0 {
				allowed[pool.ID] = share.SharePercent
			}
		}
	}
	views := []PoolAccountView{}
	for _, v := range p.Accounts {
		if share, ok := allowed[v.PoolID]; ok && eligible[v.ID] {
			v.SharePercent = share
			views = append(views, v)
		}
	}
	sort.Slice(views, func(i, j int) bool { return views[i].ID < views[j].ID })
	return views, nil
}

func (s *QuotaPoolService) Account(ctx context.Context, userID, id int64) (*PoolAccountView, error) {
	views, err := s.Accounts(ctx, userID)
	if err != nil {
		return nil, err
	}
	for _, v := range views {
		if v.ID == id {
			return &v, nil
		}
	}
	return nil, ErrQuotaPoolAccountNotFound
}

func (s *QuotaPoolService) AccountStats(ctx context.Context, userID, id int64) (*usagestats.AccountUsageStatsResponse, error) {
	if _, err := s.Account(ctx, userID, id); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	return s.usage.GetAccountUsageStats(ctx, id, now.AddDate(0, 0, -30), now)
}
