package virtuallives

import (
	"context"

	"sekai-master-api/internal/transport/http/handlers/shared"
)

// resolveVirtualLiveRewardResourceBox looks up the resource box for a virtual
// live reward. The lookup is strict to the current region and only accepts
// resource boxes whose purpose is "virtual_live_reward". There is no JP
// fallback: a missing or non-matching box keeps the reward present and the
// resourceBox absent (nil).
//
// resourceboxes IDs are NOT unique across resourceBoxPurpose, so a GetByID
// lookup can return an arbitrary colliding record. resourceBoxes is therefore
// an index of the region's virtual_live_reward boxes built from the full list.
func (handler *VirtualLiveHandler) resolveVirtualLiveRewardResourceBox(ctx context.Context, region string, reward map[string]any, resourceBoxes map[string]map[string]any) map[string]any {
	if handler == nil || handler.masterDataSync == nil {
		return nil
	}

	resourceBoxID := shared.NormalizeAnyID(reward["resourceBoxId"])
	if resourceBoxID == "" {
		return nil
	}

	resourceBox := resourceBoxes[resourceBoxID]
	if resourceBox == nil || !isUsableVirtualLiveRewardResourceBox(resourceBox) {
		return nil
	}

	result := pickVirtualLiveRewardResourceBoxFields(resourceBox, []string{"id", "resourceBoxPurpose", "resourceBoxType", "details"})
	details, _ := resourceBox["details"].([]any)
	result["details"] = enrichVirtualLiveRewardResourceBoxDetails(ctx, handler, region, details)
	return result
}

// indexVirtualLiveRewardResourceBoxes keeps the first virtual_live_reward box
// for each ID, so a same-id record with a different purpose is ignored rather
// than wrongly matched.
func indexVirtualLiveRewardResourceBoxes(resourceBoxes []map[string]any) map[string]map[string]any {
	index := make(map[string]map[string]any)
	for _, box := range resourceBoxes {
		if box == nil || shared.NormalizeComparableText(box["resourceBoxPurpose"]) != "virtual_live_reward" {
			continue
		}
		id := shared.NormalizeAnyID(box["id"])
		if _, seen := index[id]; id != "" && !seen {
			index[id] = box
		}
	}
	return index
}

func isUsableVirtualLiveRewardResourceBox(resourceBox map[string]any) bool {
	if shared.NormalizeComparableText(resourceBox["resourceBoxPurpose"]) != "virtual_live_reward" {
		return false
	}
	details, ok := resourceBox["details"].([]any)
	return ok && len(details) > 0
}

func enrichVirtualLiveRewardResourceBoxDetails(ctx context.Context, handler *VirtualLiveHandler, region string, details []any) []any {
	items := make([]any, 0, len(details))
	for _, item := range details {
		detailRecord, ok := item.(map[string]any)
		if !ok {
			items = append(items, item)
			continue
		}

		detail := pickVirtualLiveRewardResourceBoxFields(detailRecord, []string{
			"resourceType", "resourceId", "resourceLevel", "resourceQuantity", "seq",
		})
		if honor := resolveVirtualLiveRewardHonor(ctx, handler, region, detailRecord); honor != nil {
			detail["honor"] = honor
		}
		if handler != nil && handler.masterDataSync != nil {
			for key, value := range shared.RewardItemFields(ctx, handler.masterDataSync.GetByID, region, detailRecord) {
				detail[key] = value
			}
		}
		items = append(items, detail)
	}

	return items
}

func resolveVirtualLiveRewardHonor(ctx context.Context, handler *VirtualLiveHandler, region string, detail map[string]any) map[string]any {
	if handler == nil || handler.masterDataSync == nil {
		return nil
	}

	if shared.NormalizeComparableText(detail["resourceType"]) != "honor" {
		return nil
	}

	honorID := shared.NormalizeAnyID(detail["resourceId"])
	if honorID == "" {
		return nil
	}

	honor, found, err := handler.masterDataSync.GetByID(ctx, region, "honors", honorID)
	if err != nil || !found {
		return nil
	}

	result := pickVirtualLiveRewardResourceBoxFields(honor, []string{
		"id", "groupId", "honorRarity", "honorMissionType", "honorType", "assetbundleName", "name", "levels",
	})
	if groupID := shared.NormalizeAnyID(honor["groupId"]); groupID != "" {
		if honorGroup, found, err := handler.masterDataSync.GetByID(ctx, region, "honorgroups", groupID); err == nil && found {
			result["group"] = pickVirtualLiveRewardResourceBoxFields(honorGroup, []string{
				"id", "name", "honorType", "backgroundAssetbundleName", "frameName",
			})
		}
	}

	return result
}

func pickVirtualLiveRewardResourceBoxFields(record map[string]any, keys []string) map[string]any {
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
