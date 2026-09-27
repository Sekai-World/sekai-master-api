package lookups

import (
	"context"

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

// loadMissionRewardItems returns the display metadata of every item type a
// reward can reference. Each entity is decoded once per revision.
func (handler *LookupHandler) loadMissionRewardItems(ctx context.Context, region string) (missionRewardItemIndex, error) {
	index := make(missionRewardItemIndex, len(shared.RewardItemEntities))
	for resourceType, entity := range shared.RewardItemEntities {
		items, err := handler.loadMissionRewardItemEntity(ctx, region, entity, shared.RewardItemCarriesAssetbundleName(resourceType))
		if err != nil {
			return nil, err
		}
		index[resourceType] = items
	}
	return index, nil
}

func (handler *LookupHandler) loadMissionRewardItemEntity(ctx context.Context, region string, entity string, withAssetbundleName bool) (map[int64]missionRewardItem, error) {
	revision, err := handler.masterDataSync.EntityRevision(ctx, region, entity)
	if err != nil {
		return nil, err
	}

	return handler.rewardItemIndexes.load(ctx, region+"\x00"+entity, revision, func(ctx context.Context) (map[int64]missionRewardItem, error) {
		records, err := handler.masterDataSync.ListAll(ctx, region, entity)
		if err != nil {
			return nil, err
		}
		items := make(map[int64]missionRewardItem, len(records))
		for _, record := range records {
			id, ok := lookupInt64(record["id"])
			if !ok {
				continue
			}
			item := missionRewardItem{name: lookupOptionalString(record["name"])}
			if _, isTitle := record["honorRarity"]; isTitle || record["levels"] != nil {
				item.record = map[string]any{"honorRarity": record["honorRarity"], "levels": record["levels"]}
			}
			if withAssetbundleName {
				item.assetbundleName = lookupOptionalString(record["assetbundleName"])
			}
			items[id] = item
		}
		return items, nil
	})
}
