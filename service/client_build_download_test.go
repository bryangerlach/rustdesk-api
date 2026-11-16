package service

import (
	"strings"
	"testing"
	"time"
)

func TestClientBuildDownloadTokenRoundTrip(t *testing.T) {
	token, err := CreateClientBuildDownloadToken(42, "rustdesk-1.5.1.exe", time.Minute)
	if err != nil {
		t.Fatalf("CreateClientBuildDownloadToken: %v", err)
	}
	id, file, err := VerifyClientBuildDownloadToken(token)
	if err != nil {
		t.Fatalf("VerifyClientBuildDownloadToken: %v", err)
	}
	if id != 42 || file != "rustdesk-1.5.1.exe" {
		t.Errorf("got id=%d file=%q", id, file)
	}
}

func TestClientBuildDownloadTokenRejectsTampering(t *testing.T) {
	token, err := CreateClientBuildDownloadToken(42, "rustdesk-1.5.1.exe", time.Minute)
	if err != nil {
		t.Fatalf("CreateClientBuildDownloadToken: %v", err)
	}
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		t.Fatalf("unexpected token shape: %q", token)
	}
	tampered := parts[0] + "x." + parts[1]
	if _, _, err := VerifyClientBuildDownloadToken(tampered); err == nil {
		t.Error("tampered payload was accepted")
	}
	if _, _, err := VerifyClientBuildDownloadToken(token + "x"); err == nil {
		t.Error("tampered signature was accepted")
	}
}

func TestClientBuildDownloadTokenExpires(t *testing.T) {
	token, err := CreateClientBuildDownloadToken(42, "rustdesk-1.5.1.exe", -time.Second)
	if err != nil {
		t.Fatalf("CreateClientBuildDownloadToken: %v", err)
	}
	if _, _, err := VerifyClientBuildDownloadToken(token); err == nil {
		t.Error("expired token was accepted")
	}
}

func TestClientBuildDownloadTokenRejectsTraversal(t *testing.T) {
	for _, file := range []string{"", ".", "..", "../secret", `dir\file.exe`} {
		if _, err := CreateClientBuildDownloadToken(1, file, time.Minute); err == nil {
			t.Errorf("file %q was accepted", file)
		}
	}
}
