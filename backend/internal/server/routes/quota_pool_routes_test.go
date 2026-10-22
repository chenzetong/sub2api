package routes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type poolRouteRepository struct {
	service.QuotaPoolRepository
	facts *service.QuotaPoolFacts
}

func (r *poolRouteRepository) Read(context.Context) (*service.QuotaPoolFacts, error) {
	return r.facts, nil
}

type poolRouteUsage struct {
	service.UsageLogRepository
	calls int
}

func (r *poolRouteUsage) GetAccountUsageStats(context.Context, int64, time.Time, time.Time) (*usagestats.AccountUsageStatsResponse, error) {
	r.calls++
	return &usagestats.AccountUsageStatsResponse{}, nil
}

func TestQuotaPoolRoutesAuthorization(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &poolRouteRepository{facts: &service.QuotaPoolFacts{
		Allocation: service.QuotaPoolAllocation{Pools: []service.QuotaPool{{ID: 1, Name: "shared", AccountIDs: []int64{10}, Shares: []service.QuotaPoolShare{{UserID: 7, SharePercent: 10}}}}},
		Accounts:   []*service.Account{{ID: 10, Name: "visible", Platform: "openai", Type: "oauth", Credentials: map[string]any{"access_token": "secret"}, Extra: map[string]any{"private": "secret"}}},
	}}
	usage := &poolRouteUsage{}
	h := &handler.Handlers{Admin: &handler.AdminHandlers{}, QuotaPools: handler.NewQuotaPoolHandler(service.NewQuotaPoolService(repo, usage, nil, nil))}
	router := gin.New()
	jwt := middleware.JWTAuthMiddleware(func(c *gin.Context) {
		if c.GetHeader("Authorization") == "" {
			middleware.AbortWithError(c, 401, "UNAUTHORIZED", "login required")
			return
		}
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7})
		c.Set(string(middleware.ContextKeyUserRole), service.RoleUser)
		c.Next()
	})
	admin := middleware.AdminAuthMiddleware(func(c *gin.Context) {
		status := 401
		if c.GetHeader("Authorization") != "" {
			status = 403
		}
		middleware.AbortWithError(c, status, "FORBIDDEN", "admin required")
	})
	audit := middleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() })
	step := middleware.StepUpAuthMiddleware(func(c *gin.Context) { c.Next() })
	RegisterUserRoutes(router.Group("/api/v1"), h, jwt, audit, nil, nil)
	RegisterAdminRoutes(router.Group("/api/v1"), h, admin, audit, step, nil, nil)
	request := func(method, path string, authenticated bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/api/v1"+path, nil)
		if authenticated {
			req.Header.Set("Authorization", "Bearer member")
		}
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		return out
	}
	for _, path := range []string{"/pool-accounts", "/pool-accounts/10", "/pool-accounts/10/usage", "/pool-accounts/10/stats"} {
		require.Equal(t, 401, request(http.MethodGet, path, false).Code)
		out := request(http.MethodGet, path, true)
		require.Equal(t, 200, out.Code, out.Body.String())
		for _, secret := range []string{"access_token", "credentials", "private", "secret", "request_id"} {
			require.NotContains(t, out.Body.String(), secret)
		}
	}
	require.Equal(t, 1, usage.calls)
	for _, path := range []string{"/pool-accounts/999", "/pool-accounts/999/usage", "/pool-accounts/999/stats", "/pool-accounts/invalid"} {
		require.Equal(t, 404, request(http.MethodGet, path, true).Code)
	}
	require.Equal(t, 1, usage.calls, "forged IDs must never reach statistics")
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		require.Equal(t, 404, request(method, "/pool-accounts/10", true).Code)
	}
	for _, route := range []struct{ method, path string }{{"PUT", ""}, {"POST", "/sync"}, {"PUT", "/settings"}, {"GET", "/preview"}, {"GET", "/runs"}} {
		for _, authenticated := range []bool{false, true} {
			want := 401
			if authenticated {
				want = 403
			}
			require.Equal(t, want, request(route.method, "/admin/quota-pools"+route.path, authenticated).Code)
		}
	}
	repo.facts.Allocation.Pools[0].Shares = nil
	for _, path := range []string{"/pool-accounts/10", "/pool-accounts/10/usage", "/pool-accounts/10/stats"} {
		require.Equal(t, 404, request(http.MethodGet, path, true).Code)
	}
	require.Equal(t, 1, usage.calls, "revocation takes effect on the next request")
	require.Contains(t, request(http.MethodGet, "/pool-accounts", true).Body.String(), `"data":[]`)
}
