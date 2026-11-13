package service

import (
	"reflect"
	"testing"
)

func TestReleaseVersionsFromJSON(t *testing.T) {
	releases := []rustdeskRelease{
		{TagName: "1.5.3"},
		{TagName: "1.5.2", Prerelease: true},
		{TagName: "v1.5.1"},
		{TagName: "1.5.0"},
		{TagName: "1.4.9"},
		{TagName: "draft", Draft: true},
	}
	got := releaseVersionsFromJSON(releases, 3)
	want := []string{"1.5.3", "1.5.1", "1.5.0"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("releaseVersionsFromJSON() = %v, want %v", got, want)
	}
}
