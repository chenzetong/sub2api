package handler

import (
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSubscriptionWithPoolHealthLocalizesGroup(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/api/v1/subscriptions", nil)
	c.Request.Header.Set("Accept-Language", "en")
	group := &service.Group{
		ID: 7, Name: "Default", Description: "Default pool",
		I18n: service.GroupI18n{"en": {Name: "Standard", Description: "Standard pool"}},
	}
	h := &SubscriptionHandler{}

	items := h.withPoolHealth(c, []service.UserSubscription{{ID: 1, GroupID: 7, Group: group}})

	require.Len(t, items, 1)
	require.Equal(t, int64(1), items[0].ID)
	require.Equal(t, "Standard", items[0].Group.Name)
	require.Equal(t, "Standard pool", items[0].Group.Description)
	require.Nil(t, items[0].Group.I18n)
	require.Nil(t, items[0].PoolHealth)
	require.Equal(t, "Default", group.Name)
	require.NotEmpty(t, group.I18n)
}
