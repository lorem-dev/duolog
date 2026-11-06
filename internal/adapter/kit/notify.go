package kit

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"time"
)

// Notify shows the person a desktop notification. Notification text is data,
// never shell source. A missing notifier or a desktop that refuses
// notifications does not interrupt the caller.
func Notify(title, message string) {
	program, args := notificationArgv(runtime.GOOS, title, message)
	if program == "" {
		return
	}
	path, err := exec.LookPath(program)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = append(os.Environ(), "DUOLOG_NOTIFICATION_TITLE="+title, "DUOLOG_NOTIFICATION_TEXT="+message)
	configureProcess(cmd)
	cmd.WaitDelay = time.Second
	_ = cmd.Run()
}

func notificationArgv(goos, title, message string) (string, []string) {
	switch goos {
	case "darwin":
		return "osascript", []string{"-e", `on run argv`, "-e", `display notification (item 2 of argv) with title (item 1 of argv)`, "-e", `end run`, title, message}
	case "linux":
		return "notify-send", []string{"--", title, message}
	case "windows":
		return "powershell", []string{"-NoProfile", "-NonInteractive", "-Command", `$ErrorActionPreference='Stop'; [Windows.UI.Notifications.ToastNotificationManager,Windows.UI.Notifications,ContentType=WindowsRuntime] > $null; $xml=[Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent([Windows.UI.Notifications.ToastTemplateType]::ToastText02); $text=$xml.GetElementsByTagName('text'); $text.Item(0).InnerText=$env:DUOLOG_NOTIFICATION_TITLE; $text.Item(1).InnerText=$env:DUOLOG_NOTIFICATION_TEXT; $toast=[Windows.UI.Notifications.ToastNotification]::new($xml); [Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('duolog').Show($toast)`}
	}
	return "", nil
}
