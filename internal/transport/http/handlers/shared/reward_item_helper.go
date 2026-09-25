package shared

import (
	"context"
	"strings"
)

// RewardItemEntities maps reward resource types to the master-data entity that
// names them. Common currencies such as jewel or coin need no lookup.
var RewardItemEntities = map[string]string{
	"gacha_ticket":          "gachatickets",
	"material":              "materials",
	"skill_practice_ticket": "skillpracticetickets",
	"boost_item":            "boostitems",
}

// RewardItemRecordLookup reads one master-data record by ID.
type RewardItemRecordLookup func(ctx context.Context, region string, entity string, id string) (map[string]any, bool, error)

// RewardItemFields returns the display fields of a reward detail's item:
// `resourceName`, plus `resourceAssetbundleName` for items that carry one
// (gacha tickets, whose icon path uses it). It returns nil when the item type
// needs no lookup or its record is missing.
func RewardItemFields(ctx context.Context, lookup RewardItemRecordLookup, region string, detail map[string]any) map[string]any {
	entity, ok := RewardItemEntities[NormalizeComparableText(detail["resourceType"])]
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
	if bundle, ok := record["assetbundleName"].(string); ok && strings.TrimSpace(bundle) != "" {
		fields["resourceAssetbundleName"] = bundle
	}
	if len(fields) == 0 {
		return nil
	}
	return fields
}
