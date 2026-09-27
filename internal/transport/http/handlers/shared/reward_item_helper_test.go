package shared

import (
	"context"
	"reflect"
	"testing"
)

func TestRewardItemFieldsNamesTitlesWithTheirRarity(t *testing.T) {
	records := map[string]map[string]map[string]any{
		"honors":       {"3009": {"id": 3009, "name": "MASTER FULL COMBO", "honorRarity": "highest", "assetbundleName": "honor_0000"}},
		"bondshonors":  {"1212603": {"id": 1212603, "name": "ミクとKAITO", "honorRarity": "high"}},
		"gachatickets": {"17": {"id": 17, "name": "Ticket", "assetbundleName": "mission_gacha_ticket"}},
	}
	// Live Master titles leave honorRarity empty and set it per level.
	records["honors"]["3009"+"0"] = map[string]any{
		"id": 30090, "name": "Live Master", "honorRarity": "",
		"levels": []any{
			map[string]any{"level": 1, "honorRarity": "low"},
			map[string]any{"level": 2, "honorRarity": "middle"},
		},
	}
	lookup := func(_ context.Context, _ string, entity string, id string) (map[string]any, bool, error) {
		record, ok := records[entity][id]
		return record, ok, nil
	}

	for name, test := range map[string]struct {
		detail map[string]any
		want   map[string]any
	}{
		// A title's own asset bundle is not its icon path, so it is left out.
		"honor": {
			detail: map[string]any{"resourceType": "honor", "resourceId": 3009},
			want:   map[string]any{"resourceName": "MASTER FULL COMBO", "resourceRarity": "highest"},
		},
		"kizuna title": {
			detail: map[string]any{"resourceType": "bonds_honor", "resourceId": 1212603},
			want:   map[string]any{"resourceName": "ミクとKAITO", "resourceRarity": "high"},
		},
		"gacha ticket": {
			detail: map[string]any{"resourceType": "gacha_ticket", "resourceId": 17},
			want:   map[string]any{"resourceName": "Ticket", "resourceAssetbundleName": "mission_gacha_ticket"},
		},
		"live master at its rewarded level": {
			detail: map[string]any{"resourceType": "honor", "resourceId": 30090, "resourceLevel": 2},
			want:   map[string]any{"resourceName": "Live Master", "resourceRarity": "middle"},
		},
		"live master without a matching level": {
			detail: map[string]any{"resourceType": "honor", "resourceId": 30090, "resourceLevel": 9},
			want:   map[string]any{"resourceName": "Live Master", "resourceRarity": "low"},
		},
		"currency": {
			detail: map[string]any{"resourceType": "jewel", "resourceQuantity": 50},
			want:   nil,
		},
	} {
		got := RewardItemFields(context.Background(), lookup, "jp", test.detail)
		if !reflect.DeepEqual(got, test.want) {
			t.Fatalf("%s: expected %#v, got %#v", name, test.want, got)
		}
	}
}
