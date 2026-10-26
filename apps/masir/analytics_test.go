package main

import (
	"testing"
)

func TestErrorPropsNoRawMessage(t *testing.T) {
	props := errorProps(fail("secret.example.com: permission denied"))
	if props["error_kind"] != "permission_denied" {
		t.Fatalf("kind = %v", props["error_kind"])
	}
	if _, ok := props["error_message"]; ok {
		t.Fatal("CLI errors must not send raw messages")
	}
}
