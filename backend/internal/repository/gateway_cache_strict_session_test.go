package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestGatewayCacheStrictSessionBindingNeverMovesAccounts(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	cache := &gatewayCache{rdb: client}
	ctx := context.Background()
	sessionHash := "strict:v1:session-hash"

	require.NoError(t, cache.SetSessionAccountID(ctx, 7, sessionHash, 101, time.Hour))
	require.NoError(t, cache.SetSessionAccountID(ctx, 7, sessionHash, 101, time.Hour))
	err := cache.SetSessionAccountID(ctx, 7, sessionHash, 202, time.Hour)
	require.ErrorIs(t, err, service.ErrStrictSessionAccountConflict)

	accountID, err := cache.GetSessionAccountID(ctx, 7, sessionHash)
	require.NoError(t, err)
	require.Equal(t, int64(101), accountID)

	// Scheduler cleanup is intentionally ignored for hard affinity; expiry is
	// the only automatic release mechanism.
	require.NoError(t, cache.DeleteSessionAccountID(ctx, 7, sessionHash))
	accountID, err = cache.GetSessionAccountID(ctx, 7, sessionHash)
	require.NoError(t, err)
	require.Equal(t, int64(101), accountID)
	require.False(t, errors.Is(err, service.ErrStickySessionNotFound))
}
