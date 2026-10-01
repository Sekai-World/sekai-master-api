package virtuallives

import (
	"context"

	"sekai-master-api/internal/transport/http/handlers/shared"
)

// Resource box purposes of the virtual live reward kinds. Solo virtual lives
// reward the Virtual Cheer Coins spent in total: one box per threshold, and
// one surplus box per basePoint coins spent past the thresholds.
const (
	virtualLiveRewardPurpose                       = "virtual_live_reward"
	virtualLiveTotalCheerPointRewardPurpose        = "virtual_live_total_cheer_point_reward"
	virtualLiveTotalCheerPointSurplusRewardPurpose = "virtual_live_total_cheer_point_surplus_reward"
)

// virtualLiveResourceBoxes holds the resource boxes a virtual live's rewards
// reference, by purpose, then ID.
type virtualLiveResourceBoxes map[string]map[string]map[string]any

// virtualLiveRewardRecords returns a virtual live's reward records by the
// resource box purpose they reference.
func virtualLiveRewardRecords(record map[string]any) map[string][]map[string]any {
	rewards := map[string][]map[string]any{}
	for field, purpose := range map[string]string{
		"virtualLiveRewards":                virtualLiveRewardPurpose,
		"virtualLiveTotalCheerPointRewards": virtualLiveTotalCheerPointRewardPurpose,
	} {
		items, _ := record[field].([]any)
		for _, item := range items {
			if reward, ok := item.(map[string]any); ok {
				rewards[purpose] = append(rewards[purpose], reward)
			}
		}
	}
	if surplus, ok := record["virtualLiveTotalCheerPointSurplusReward"].(map[string]any); ok {
		rewards[virtualLiveTotalCheerPointSurplusRewardPurpose] = []map[string]any{surplus}
	}
	return rewards
}

// details returns the details of every box.
func (resourceBoxes virtualLiveResourceBoxes) details() []map[string]any {
	details := []map[string]any{}
	for _, boxes := range resourceBoxes {
		for _, box := range boxes {
			items, _ := box["details"].([]any)
			for _, item := range items {
				if detail, ok := item.(map[string]any); ok {
					details = append(details, detail)
				}
			}
		}
	}
	return details
}

// loadRewardResourceBoxes reads the boxes the rewards reference, by purpose
// and ID, in one composite-key read. resourceboxes IDs are not unique across
// resourceBoxPurpose, so a bare-ID lookup cannot select them.
func (handler *VirtualLiveHandler) loadRewardResourceBoxes(ctx context.Context, region string, rewardsByPurpose map[string][]map[string]any) virtualLiveResourceBoxes {
	if handler == nil || handler.masterDataSync == nil {
		return nil
	}

	type boxKey struct{ purpose, id string }
	ids := []boxKey{}
	keys := []map[string]any{}
	for purpose, rewards := range rewardsByPurpose {
		for _, reward := range rewards {
			if id := shared.NormalizeAnyID(reward["resourceBoxId"]); id != "" {
				ids = append(ids, boxKey{purpose: purpose, id: id})
				keys = append(keys, map[string]any{"id": id, "resourceBoxPurpose": purpose})
			}
		}
	}
	if len(keys) == 0 {
		return nil
	}

	boxes, err := handler.masterDataSync.GetByCompositeKeys(ctx, region, "resourceboxes", keys)
	if err != nil {
		return nil
	}
	resourceBoxes := virtualLiveResourceBoxes{}
	for index, box := range boxes {
		if box == nil {
			continue
		}
		key := ids[index]
		if resourceBoxes[key.purpose] == nil {
			resourceBoxes[key.purpose] = map[string]map[string]any{}
		}
		resourceBoxes[key.purpose][key.id] = box
	}
	return resourceBoxes
}

// prefetchRewardDetailRecords reads the items and titles the boxes' details
// name, then the titles' groups. A failed read leaves those lookups to GetByID.
func (handler *VirtualLiveHandler) prefetchRewardDetailRecords(ctx context.Context, region string, resourceBoxes virtualLiveResourceBoxes) shared.RecordLookup {
	details := resourceBoxes.details()
	idsByEntity := shared.RewardItemIDsByEntity(details)
	for _, detail := range details {
		if shared.NormalizeComparableText(detail["resourceType"]) == "honor" {
			idsByEntity["honors"] = append(idsByEntity["honors"], shared.NormalizeAnyID(detail["resourceId"]))
		}
	}
	records, err := shared.PrefetchRecords(ctx, handler.masterDataSync, region, idsByEntity)
	if err != nil {
		return handler.masterDataSync.GetByID
	}
	groupIDs := []string{}
	for _, id := range idsByEntity["honors"] {
		if honor, ok := records.Record("honors", id); ok {
			groupIDs = append(groupIDs, shared.NormalizeAnyID(honor["groupId"]))
		}
	}
	_ = records.Add(ctx, map[string][]string{"honorgroups": groupIDs})
	return records.Lookup
}

// buildVirtualLiveRewards copies the rewards and expands each one's resource
// box of the given purpose.
func buildVirtualLiveRewards(ctx context.Context, lookup shared.RecordLookup, region string, rawRewards any, purpose string, resourceBoxes virtualLiveResourceBoxes) []map[string]any {
	items, ok := rawRewards.([]any)
	if !ok {
		return []map[string]any{}
	}

	rewards := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if rewardRecord, ok := item.(map[string]any); ok {
			rewards = append(rewards, buildVirtualLiveReward(ctx, lookup, region, rewardRecord, purpose, resourceBoxes))
		}
	}
	return rewards
}

func buildVirtualLiveReward(ctx context.Context, lookup shared.RecordLookup, region string, rewardRecord map[string]any, purpose string, resourceBoxes virtualLiveResourceBoxes) map[string]any {
	reward := make(map[string]any, len(rewardRecord)+1)
	for key, value := range rewardRecord {
		reward[key] = value
	}
	if resourceBox := resolveVirtualLiveRewardResourceBox(ctx, lookup, region, rewardRecord, purpose, resourceBoxes); resourceBox != nil {
		reward["resourceBox"] = resourceBox
	}
	return reward
}

// resolveVirtualLiveRewardResourceBox returns the reward's resource box of the
// given purpose. The lookup is strict to the current region. There is no JP
// fallback: a missing or non-matching box keeps the reward present and the
// resourceBox absent (nil).
func resolveVirtualLiveRewardResourceBox(ctx context.Context, lookup shared.RecordLookup, region string, reward map[string]any, purpose string, resourceBoxes virtualLiveResourceBoxes) map[string]any {
	resourceBoxID := shared.NormalizeAnyID(reward["resourceBoxId"])
	if resourceBoxID == "" {
		return nil
	}

	resourceBox := resourceBoxes[purpose][resourceBoxID]
	if resourceBox == nil || !isUsableVirtualLiveRewardResourceBox(resourceBox, purpose) {
		return nil
	}

	result := pickVirtualLiveFields(resourceBox, []string{"id", "resourceBoxPurpose", "resourceBoxType", "details"})
	details, _ := resourceBox["details"].([]any)
	result["details"] = enrichVirtualLiveRewardResourceBoxDetails(ctx, lookup, region, details)
	return result
}

func isUsableVirtualLiveRewardResourceBox(resourceBox map[string]any, purpose string) bool {
	if shared.NormalizeComparableText(resourceBox["resourceBoxPurpose"]) != purpose {
		return false
	}
	details, ok := resourceBox["details"].([]any)
	return ok && len(details) > 0
}

func enrichVirtualLiveRewardResourceBoxDetails(ctx context.Context, lookup shared.RecordLookup, region string, details []any) []any {
	items := make([]any, 0, len(details))
	for _, item := range details {
		detailRecord, ok := item.(map[string]any)
		if !ok {
			items = append(items, item)
			continue
		}

		detail := pickVirtualLiveFields(detailRecord, []string{
			"resourceType", "resourceId", "resourceLevel", "resourceQuantity", "seq",
		})
		if lookup != nil {
			if honor := resolveVirtualLiveRewardHonor(ctx, lookup, region, detailRecord); honor != nil {
				detail["honor"] = honor
			}
			for key, value := range shared.RewardItemFields(ctx, shared.RewardItemRecordLookup(lookup), region, detailRecord) {
				detail[key] = value
			}
		}
		items = append(items, detail)
	}

	return items
}

func resolveVirtualLiveRewardHonor(ctx context.Context, lookup shared.RecordLookup, region string, detail map[string]any) map[string]any {
	if shared.NormalizeComparableText(detail["resourceType"]) != "honor" {
		return nil
	}

	honorID := shared.NormalizeAnyID(detail["resourceId"])
	if honorID == "" {
		return nil
	}

	honor, found, err := lookup(ctx, region, "honors", honorID)
	if err != nil || !found {
		return nil
	}

	result := pickVirtualLiveFields(honor, []string{
		"id", "groupId", "honorRarity", "honorMissionType", "honorType", "assetbundleName", "name", "levels",
	})
	if groupID := shared.NormalizeAnyID(honor["groupId"]); groupID != "" {
		if honorGroup, found, err := lookup(ctx, region, "honorgroups", groupID); err == nil && found {
			result["group"] = pickVirtualLiveFields(honorGroup, []string{
				"id", "name", "honorType", "backgroundAssetbundleName", "frameName",
			})
		}
	}

	return result
}

// buildVirtualItemOverrideCost copies a solo virtual live's cheer item cost
// and names its cost item: costResourceName, and costResourceAssetbundleName
// when the item's icon path depends on it.
func buildVirtualItemOverrideCost(ctx context.Context, lookup shared.RecordLookup, region string, record map[string]any) map[string]any {
	cost := make(map[string]any, len(record)+2)
	for key, value := range record {
		cost[key] = value
	}
	if lookup == nil {
		return cost
	}
	item := shared.RewardItemFields(ctx, shared.RewardItemRecordLookup(lookup), region, map[string]any{
		"resourceType": record["costResourceType"],
		"resourceId":   record["costResourceId"],
	})
	if name, ok := item["resourceName"]; ok {
		cost["costResourceName"] = name
	}
	if bundle, ok := item["resourceAssetbundleName"]; ok {
		cost["costResourceAssetbundleName"] = bundle
	}
	return cost
}

// pickVirtualLiveFields copies the listed fields record has.
func pickVirtualLiveFields(record map[string]any, keys []string) map[string]any {
	if record == nil {
		return map[string]any{}
	}

	result := make(map[string]any, len(keys))
	for _, key := range keys {
		if value, ok := record[key]; ok {
			result[key] = value
		}
	}

	return result
}
