package main

import "testing"

func TestDesktopLoopbackPortIsStableByDefault(t *testing.T) {
	t.Setenv("PTXT_DESKTOP_PORT", "")
	if got := desktopLoopbackPort(); got != defaultDesktopLoopbackPort {
		t.Fatalf("desktopLoopbackPort() = %d, want %d", got, defaultDesktopLoopbackPort)
	}
}

func TestDesktopLoopbackPortAllowsValidOverride(t *testing.T) {
	t.Setenv("PTXT_DESKTOP_PORT", "24888")
	if got := desktopLoopbackPort(); got != 24888 {
		t.Fatalf("desktopLoopbackPort() = %d, want 24888", got)
	}
}

func TestDesktopLoopbackPortRejectsUnsafeOrInvalidOverride(t *testing.T) {
	for _, value := range []string{"0", "80", "65536", "not-a-port"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("PTXT_DESKTOP_PORT", value)
			if got := desktopLoopbackPort(); got != defaultDesktopLoopbackPort {
				t.Fatalf("desktopLoopbackPort() = %d, want default %d", got, defaultDesktopLoopbackPort)
			}
		})
	}
}
