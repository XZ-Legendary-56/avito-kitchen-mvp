package kitchen

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"venue-pasta-roma/internal/generated/partnerclient"
)

func TestLoadMenu_PopulatesStateFromSyncResponse(t *testing.T) {
	platform := newFakePlatform(t)
	carbonaraID := uuid.New()
	externalID := "pr-carbonara"
	stockQty := 10
	platform.menuResponse = partnerclient.PartnerMenu{
		MenuVersion: 1,
		Categories: []partnerclient.PartnerMenuCategory{
			{
				Id:   uuid.New(),
				Name: "Pasta",
				Items: []partnerclient.PartnerMenuItem{
					{Id: carbonaraID, Name: "Carbonara", ExternalId: &externalID, StockQty: &stockQty},
				},
			},
		},
	}

	state := NewState()
	err := LoadMenu(context.Background(), platform.client(t), state)
	require.NoError(t, err)

	platformID, ok := state.SetStockByExternalID(externalID, 10)
	require.True(t, ok, "LoadMenu must have recorded pr-carbonara under its external id")
	assert.Equal(t, carbonaraID, platformID)
}

func TestLoadMenu_ItemWithoutExternalID_IsSkipped(t *testing.T) {
	platform := newFakePlatform(t)
	platform.menuResponse = partnerclient.PartnerMenu{
		Categories: []partnerclient.PartnerMenuCategory{
			{Id: uuid.New(), Name: "Pasta", Items: []partnerclient.PartnerMenuItem{
				{Id: uuid.New(), Name: "No External Id"},
			}},
		},
	}

	state := NewState()
	require.NoError(t, LoadMenu(context.Background(), platform.client(t), state))

	assert.Empty(t, state.StockSnapshot())
}
