package handler

import (
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type QuotaPoolHandler struct{ pools *service.QuotaPoolService }

func NewQuotaPoolHandler(pools *service.QuotaPoolService) *QuotaPoolHandler {
	return &QuotaPoolHandler{pools: pools}
}

func (h *QuotaPoolHandler) Allocation(c *gin.Context) {
	f, err := h.pools.Allocation(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	accounts := []service.PoolAccountView{}
	for _, a := range f.Accounts {
		if !a.IsOpenAIOAuth() || a.OwnerUserID != nil {
			continue
		}
		estimate, _ := service.EstimateQuotaPoolAccount(a, f.ObservedCosts[a.ID], f.Predictions[a.ID], time.Now().UTC())
		accounts = append(accounts, service.PoolAccountView{ID: a.ID, Name: a.Name, Platform: a.Platform, Type: a.Type, Status: a.Status, Estimate: estimate})
	}
	response.Success(c, gin.H{"revision": f.Allocation.Revision, "pools": f.Allocation.Pools, "accounts": accounts, "users": f.Users, "settings": f.Settings})
}

func (h *QuotaPoolHandler) SaveAllocation(c *gin.Context) {
	var req service.QuotaPoolAllocation
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "无效的额度分配")
		return
	}
	if err := h.pools.SaveAllocation(c.Request.Context(), req); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	h.Allocation(c)
}
func (h *QuotaPoolHandler) Settings(c *gin.Context) {
	f, err := h.pools.Allocation(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, f.Settings)
}
func (h *QuotaPoolHandler) UpdateSettings(c *gin.Context) {
	var req service.QuotaPoolSettings
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "无效的同步设置")
		return
	}
	if err := h.pools.UpdateSettings(c.Request.Context(), req); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	h.Settings(c)
}
func (h *QuotaPoolHandler) Preview(c *gin.Context) {
	p, err := h.pools.Preview(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, p)
}
func (h *QuotaPoolHandler) Sync(c *gin.Context) {
	var req struct {
		OperationKey string `json:"operation_key"`
		Revision     int64  `json:"revision"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "缺少同步操作标识")
		return
	}
	p, err := h.pools.Sync(c.Request.Context(), req.OperationKey, "manual", false, req.Revision)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, p)
}
func (h *QuotaPoolHandler) Runs(c *gin.Context) {
	runs, err := h.pools.Runs(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, runs)
}

func poolAccountSubject(c *gin.Context) (int64, bool) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return 0, false
	}
	return subject.UserID, true
}
func poolAccountID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.NotFound(c, "账号不存在或没有查看权限")
		return 0, false
	}
	return id, true
}
func (h *QuotaPoolHandler) Accounts(c *gin.Context) {
	userID, ok := poolAccountSubject(c)
	if !ok {
		return
	}
	accounts, err := h.pools.Accounts(c.Request.Context(), userID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, accounts)
}
func (h *QuotaPoolHandler) Account(c *gin.Context) {
	userID, ok := poolAccountSubject(c)
	if !ok {
		return
	}
	id, ok := poolAccountID(c)
	if !ok {
		return
	}
	account, err := h.pools.Account(c.Request.Context(), userID, id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, account)
}
func (h *QuotaPoolHandler) Stats(c *gin.Context) {
	userID, ok := poolAccountSubject(c)
	if !ok {
		return
	}
	id, ok := poolAccountID(c)
	if !ok {
		return
	}
	stats, err := h.pools.AccountStats(c.Request.Context(), userID, id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, stats)
}
