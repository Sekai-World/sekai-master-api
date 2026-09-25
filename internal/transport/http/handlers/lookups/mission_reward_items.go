package lookups

import "context"

// missionRewardItemEntities maps reward resource types to the master-data
// entity that names them. Only item types with an ID-addressed master record
// are listed; common currencies such as jewel or coin need no lookup.
var missionRewardItemEntities = map[string]string{
	"gacha_ticket":          "gachatickets",
	"material":              "materials",
	"skill_practice_ticket": "skillpracticetickets",
	"boost_item":            "boostitems",
}

// missionRewardItem is the display metadata of one rewarded item.
type missionRewardItem struct {
	name            *string
	assetbundleName *string
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
	index := make(missionRewardItemIndex, len(missionRewardItemEntities))
	for resourceType, entity := range missionRewardItemEntities {
		items, err := handler.loadMissionRewardItemEntity(ctx, region, entity)
		if err != nil {
			return nil, err
		}
		index[resourceType] = items
	}
	return index, nil
}

func (handler *LookupHandler) loadMissionRewardItemEntity(ctx context.Context, region string, entity string) (map[int64]missionRewardItem, error) {
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
			items[id] = missionRewardItem{
				name:            lookupOptionalString(record["name"]),
				assetbundleName: lookupOptionalString(record["assetbundleName"]),
			}
		}
		return items, nil
	})
}
