package navorder

import (
	"reflect"
	"testing"
)

func TestNormalizeNavOrder_AppendsMissingRoutesAndDropsUnknown(t *testing.T) {
	defaults := []string{"home", "channel", "group", "model", "analytics", "log", "notification", "ops", "apikey", "setting", "user"}
	got := NormalizeNavOrder(`["group","group","unknown","setting"]`, defaults)
	want := []string{"group", "setting", "home", "channel", "model", "analytics", "log", "notification", "ops", "apikey", "user"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeNavOrder() = %v, want %v", got, want)
	}
}
