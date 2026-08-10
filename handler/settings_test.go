package handler

import (
	"encoding/json"
	"testing"
)

func TestSettingDefJSON_Unmarshal(t *testing.T) {
	raw := []byte(`[{"Key":"a","Label":"A","Type":"string","Value":"1","Group":"G"}]`)
	var defs []settingDefJSON
	if err := json.Unmarshal(raw, &defs); err != nil {
		t.Fatal(err)
	}
	if len(defs) != 1 || defs[0].Key != "a" || defs[0].Group != "G" {
		t.Fatalf("%+v", defs)
	}
}
