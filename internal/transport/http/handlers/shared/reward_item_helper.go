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
}

// RewardItemCarriesAssetbundleName reports whether a reward type's icon path
// depends on its record's assetbundleName. Titles have one too, but their
// icon comes from their rarity.
func RewardItemCarriesAssetbundleName(resourceType string) bool {
	return resourceType == "gacha_ticket"
}

// RewardItemRecordLookup reads one master-data record by ID.
type RewardItemRecordLookup func(ctx context.Context, region string, entity string, id string) (map[string]any, bool, error)

// RewardItemFields returns the display fields of a reward detail's item:
// `resourceName`, `resourceAssetbundleName` for gacha tickets (their icon path
// uses it), and `resourceRarity` for titles. It returns nil when the item type
// needs no lookup or its record is missing.
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
	if bundle, ok := record["assetbundleName"].(string); ok && strings.TrimSpace(bundle) != "" && RewardItemCarriesAssetbundleName(resourceType) {
		fields["resourceAssetbundleName"] = bundle
	}
	if rarity, ok := record["honorRarity"].(string); ok && strings.TrimSpace(rarity) != "" {
		fields["resourceRarity"] = rarity
	}
	if len(fields) == 0 {
		return nil
	}
	return fields
}
