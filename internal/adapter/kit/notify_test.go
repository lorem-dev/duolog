package kit

import (
	"strings"
	"testing"
)

func TestNotificationTextIsNotExecutableSource(t *testing.T) {
	message := "peer-id: a quote \"; $(do not run)\nnext line"
	for _, goos := range []string{"darwin", "linux", "windows"} {
		program, args := notificationArgv(goos, "duolog", message)
		if program == "" || len(args) == 0 {
			t.Fatalf("missing notifier for %s", goos)
		}
		if goos == "windows" {
			if strings.Contains(args[len(args)-1], message) || !strings.Contains(args[len(args)-1], "$env:DUOLOG_NOTIFICATION_TEXT") {
				t.Fatal(args)
			}
		} else if args[len(args)-1] != message {
			t.Fatalf("notification text changed: %v", args)
		}
	}
	if program, _ := notificationArgv("unknown", "duolog", message); program != "" {
		t.Fatal("unsupported OS got a notifier")
	}
}
