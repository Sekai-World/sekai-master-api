package lookups

import (
	"context"
	"slices"
	"strconv"

	"sekai-master-api/internal/transport/http/handlers/shared"
)

// missionRewardItem is the display metadata of one rewarded item.
type missionRewardItem struct {
	name            *string
	assetbundleName *string
	// record is kept for titles, whose rarity can depend on the rewarded level.
	record map[string]any
}

func (item missionRewardItem) rarity(level *int64) *string {
	if item.record == nil {
		return nil
	}
	var rewardedLevel any
	if level != nil {
		rewardedLevel = *level
	}
	rarity := shared.RewardTitleRarity(item.record, rewardedLevel)
	if rarity == "" {
		return nil
	}
	return &rarity
}

// missionRewardItemIndex holds reward items by resource type, then by ID.
type missionRewardItemIndex map[string]map[int64]missionRewardItem

func (index missionRewardItemIndex) lookup(resourceType *string, resourceID *int64) (missionRewardItem, bool) {
	if resourceType == nil || resourceID == nil {
		return missionRewardItem{}, false
	}
	item, ok := index[*resourceType][*resourceID]
	return item, ok
}

// loadMissionRewardItems reads the display metadata of the rewarded items, by
// resource type, with one batched read per item entity.
func (handler *LookupHandler) loadMissionRewardItems(ctx context.Context, region string, idsByType map[string][]int64) (missionRewardItemIndex, error) {
	index := make(missionRewardItemIndex, len(idsByType))
	for resourceType, ids := range idsByType {
		entity, ok := shared.RewardItemEntities[resourceType]
		if !ok {
			continue
		}
		ids = slices.Compact(slices.Sorted(slices.Values(ids)))
		keys := make([]string, 0, len(ids))
		for _, id := range ids {
			keys = append(keys, strconv.FormatInt(id, 10))
		}
		records, err := handler.masterDataSync.GetByIDs(ctx, region, entity, keys)
		if err != nil {
			return nil, err
		}

		withAssetbundleName := shared.RewardItemCarriesAssetbundleName(resourceType)
		items := make(map[int64]missionRewardItem, len(records))
		for position, record := range records {
			if record == nil {
				continue
			}
			item := missionRewardItem{name: lookupOptionalString(record["name"])}
			if _, isTitle := record["honorRarity"]; isTitle || record["levels"] != nil {
				item.record = map[string]any{"honorRarity": record["honorRarity"], "levels": record["levels"]}
			}
			if withAssetbundleName {
				item.assetbundleName = lookupOptionalString(record["assetbundleName"])
			}
			items[ids[position]] = item
		}
		index[resourceType] = items
	}
	return index, nil
}
