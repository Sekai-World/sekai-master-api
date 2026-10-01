package shared

import (
	"context"
	"reflect"
	"testing"
)

func TestRewardItemFieldsNamesTitlesWithTheirRarity(t *testing.T) {
	records := map[string]map[string]map[string]any{
		"honors":                     {"3009": {"id": 3009, "name": "MASTER FULL COMBO", "honorRarity": "highest", "assetbundleName": "honor_0000"}},
		"bondshonors":                {"1212603": {"id": 1212603, "name": "ミクとKAITO", "honorRarity": "high"}},
		"gachatickets":               {"17": {"id": 17, "name": "Ticket", "assetbundleName": "mission_gacha_ticket"}},
		"mysekaimaterials":           {"35": {"id": 35, "name": "スカイブルーメモリア", "iconAssetbundleName": "item_memoria_1"}},
		"mysekaitools":               {"10": {"id": 10, "name": "チェーンソー", "assetbundleName": "ax0005"}},
		"stamps":                     {"33": {"id": 33, "name": "[スタンプ]リン：早く歌いたーい！", "assetbundleName": "stamp0038"}},
		"virtuallivetransitionitems": {"1": {"id": 1, "name": "メモリアルレコード", "assetbundleName": "memory_peace_5th_01"}},
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
		// A MySekai material's icon is named by its iconAssetbundleName.
		"mysekai material": {
			detail: map[string]any{"resourceType": "mysekai_material", "resourceId": 35},
			want:   map[string]any{"resourceName": "スカイブルーメモリア", "resourceAssetbundleName": "item_memoria_1"},
		},
		"mysekai tool": {
			detail: map[string]any{"resourceType": "mysekai_tool", "resourceId": 10},
			want:   map[string]any{"resourceName": "チェーンソー", "resourceAssetbundleName": "ax0005"},
		},
		// Stamp bundles do not follow their IDs.
		"stamp": {
			detail: map[string]any{"resourceType": "stamp", "resourceId": 33},
			want:   map[string]any{"resourceName": "[スタンプ]リン：早く歌いたーい！", "resourceAssetbundleName": "stamp0038"},
		},
		"virtual live archive item": {
			detail: map[string]any{"resourceType": "virtual_live_transition_item", "resourceId": 1},
			want:   map[string]any{"resourceName": "メモリアルレコード", "resourceAssetbundleName": "memory_peace_5th_01"},
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
