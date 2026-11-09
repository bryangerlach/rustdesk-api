package api

import (
	"reflect"
	"testing"
)

func TestCandidateTargets(t *testing.T) {
	cases := []struct {
		os       string
		arch     string
		contains []string
	}{
		// rdgen OS-level target must be a candidate for every arch
		{"windows", "x86_64", []string{"windows", "windows-x86_64-exe"}},
		{"windows", "amd64", []string{"windows", "windows-amd64-exe"}},
		{"windows", "386", []string{"windows-x86"}},
		{"linux", "amd64", []string{"linux", "linux-x86_64-deb"}},
		{"linux", "x86_64", []string{"linux", "linux-x86_64-deb"}},
		{"linux", "arm64", []string{"linux"}},
		{"linux", "aarch64", []string{"linux"}},
		{"macos", "arm64", []string{"macos", "macos-arm64-dmg"}},
		{"macos", "x86_64", []string{"macos", "macos-x86_64-dmg"}},
		{"android", "arm64-v8a", []string{"android", "android-arm64-v8a"}},
		{"android", "armeabi-v7a", []string{"android", "android-armeabi-v7a"}},
		// older rdgen builds used these targets
		{"android", "arm64", []string{"android-arm64"}},
	}
	for _, c := range cases {
		got := candidateTargets(c.os, c.arch)
		for _, want := range c.contains {
			found := false
			for _, g := range got {
				if g == want {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("candidateTargets(%q,%q) = %v, missing %q", c.os, c.arch, got, want)
			}
		}
	}

	if got := candidateTargets("plan9", "mips"); got != nil {
		t.Errorf("expected nil for unsupported platform, got %v", got)
	}
}

func TestSelectUpdateFile(t *testing.T) {
	windowsFiles := []string{"rustdesk-normal-1_5_0.exe", "rustdesk-normal-1_5_0.msi"}
	linuxFiles := []string{
		"rustdesk-normal-1_5_0-aarch64.deb",
		"rustdesk-normal-1_5_0-x86_64.deb",
		"rustdesk-normal-1_5_0-aarch64.AppImage",
	}
	androidFiles := []string{
		"rustdesk-normal-1_5_0-armv7.apk",
		"rustdesk-normal-1_5_0-aarch64.apk",
	}
	macosFiles := []string{
		"rustdesk-normal-1_5_0-x86_64.dmg",
		"rustdesk-normal-1_5_0-arm64.dmg",
	}

	cases := []struct {
		name  string
		files []string
		os    string
		arch  string
		want  string
	}{
		{"windows prefers exe", windowsFiles, "windows", "x86_64", "rustdesk-normal-1_5_0.exe"},
		{"linux amd64", linuxFiles, "linux", "amd64", "rustdesk-normal-1_5_0-x86_64.deb"},
		{"linux arm64", linuxFiles, "linux", "arm64", "rustdesk-normal-1_5_0-aarch64.deb"},
		{"android arm64-v8a", androidFiles, "android", "arm64-v8a", "rustdesk-normal-1_5_0-aarch64.apk"},
		{"android armeabi-v7a", androidFiles, "android", "armeabi-v7a", "rustdesk-normal-1_5_0-armv7.apk"},
		{"macos arm64", macosFiles, "macos", "arm64", "rustdesk-normal-1_5_0-arm64.dmg"},
		{"macos intel", macosFiles, "macos", "x86_64", "rustdesk-normal-1_5_0-x86_64.dmg"},
	}
	for _, c := range cases {
		if got := selectUpdateFile(c.files, c.os, c.arch); got != c.want {
			t.Errorf("%s: selectUpdateFile() = %q, want %q", c.name, got, c.want)
		}
	}

	if got := selectUpdateFile(nil, "windows", "amd64"); got != "" {
		t.Errorf("expected empty string for empty list, got %q", got)
	}
}

func TestCandidateTargetsStableOrder(t *testing.T) {
	// dvforge-specific targets should be preferred (first) over OS-level ones.
	got := candidateTargets("windows", "x86_64")
	want := []string{"windows-x86_64-exe", "windows-amd64-exe", "windows"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("candidateTargets = %v, want %v", got, want)
	}
}
