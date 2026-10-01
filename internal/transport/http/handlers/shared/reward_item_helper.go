package shared

import (
	"context"
	"strings"
)

// RewardItemEntities maps reward resource types to the master-data entity that
// names them. Common currencies such as jewel or coin need no lookup. Titles
// (honors and Kizuna titles) also carry a rarity, which picks their icon.
var RewardItemEntities = map[string]string{
	"gacha_ticket":          "gachatickets",
	"material":              "materials",
	"skill_practice_ticket": "skillpracticetickets",
	"boost_item":            "boostitems",
	"honor":                 "honors",
	"bonds_honor":           "bondshonors",
	"mysekai_material":      "mysekaimaterials",
	"mysekai_tool":          "mysekaitools",
	"stamp":                 "stamps",
	// Virtual message lives give these archive (memorial record) items.
	"virtual_live_transition_item": "virtuallivetransitionitems",
}

// rewardItemAssetbundleFields names, per reward type, the record field its
// icon path depends on. Titles have an assetbundleName too, but their icon
// comes from their rarity.
var rewardItemAssetbundleFields = map[string]string{
	"gacha_ticket":     "assetbundleName",
	"mysekai_material": "iconAssetbundleName",
	"mysekai_tool":     "assetbundleName",
	// Stamp bundles do not follow their IDs (JP stamp 33 is stamp0038).
	"stamp":                        "assetbundleName",
	"virtual_live_transition_item": "assetbundleName",
}

// RewardItemAssetbundleField returns the record field that holds a reward
// type's icon asset bundle name, or "" when its icon path does not depend on
// the record.
func RewardItemAssetbundleField(resourceType string) string {
	return rewardItemAssetbundleFields[resourceType]
}

// RewardItemRecordLookup reads one master-data record by ID.
type RewardItemRecordLookup func(ctx context.Context, region string, entity string, id string) (map[string]any, bool, error)

// RewardTitleRarity returns a title record's rarity. Live Master titles leave
// the title's honorRarity empty and set it per level, so those fall back to the
// rewarded level's rarity, then to the first level that has one.
func RewardTitleRarity(record map[string]any, level any) string {
	if rarity, ok := record["honorRarity"].(string); ok && strings.TrimSpace(rarity) != "" {
		return rarity
	}
	levels, _ := record["levels"].([]any)
	wanted := NormalizeAnyID(level)
	first := ""
	for _, item := range levels {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		rarity, _ := entry["honorRarity"].(string)
		if strings.TrimSpace(rarity) == "" {
			continue
		}
		if wanted != "" && NormalizeAnyID(entry["level"]) == wanted {
			return rarity
		}
		if first == "" {
			first = rarity
		}
	}
	return first
}

// RewardItemFields returns the display fields of a reward detail's item:
// `resourceName`, `resourceAssetbundleName` for gacha tickets, MySekai
// materials and tools, stamps, and virtual live archive items (their icon path
// uses it), and `resourceRarity` for titles. It returns nil when the item type needs no lookup or its record is
// missing.
func RewardItemFields(ctx context.Context, lookup RewardItemRecordLookup, region string, detail map[string]any) map[string]any {
	resourceType := NormalizeComparableText(detail["resourceType"])
	entity, ok := RewardItemEntities[resourceType]
	if !ok || lookup == nil {
		return nil
	}
	id := NormalizeAnyID(detail["resourceId"])
	if id == "" {
		return nil
	}
	record, found, err := lookup(ctx, region, entity, id)
	if err != nil || !found {
		return nil
	}

	fields := map[string]any{}
	if name, ok := record["name"].(string); ok && strings.TrimSpace(name) != "" {
		fields["resourceName"] = name
	}
	if field := RewardItemAssetbundleField(resourceType); field != "" {
		if bundle, ok := record[field].(string); ok && strings.TrimSpace(bundle) != "" {
			fields["resourceAssetbundleName"] = bundle
		}
	}
	if rarity := RewardTitleRarity(record, detail["resourceLevel"]); rarity != "" {
		fields["resourceRarity"] = rarity
	}
	if len(fields) == 0 {
		return nil
	}
	return fields
}

// RewardItemIDsByEntity collects, by entity, the IDs RewardItemFields looks up
// for details, so they can be prefetched in one read per entity.
func RewardItemIDsByEntity(details []map[string]any) map[string][]string {
	ids := make(map[string][]string)
	for _, detail := range details {
		entity, ok := RewardItemEntities[NormalizeComparableText(detail["resourceType"])]
		if !ok {
			continue
		}
		if id := NormalizeAnyID(detail["resourceId"]); id != "" {
			ids[entity] = append(ids[entity], id)
		}
	}
	return ids
}
