package peer

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/lorem-dev/duolog/internal/runner"
	"github.com/lorem-dev/duolog/internal/session"
)

var notifyPerson = notifyOS

// notifyStuck claims a reminder under the registry lock before sending it.
// Separate wait processes cannot notify twice for the same question.
func (e *env) notifyStuck(ctx context.Context, entry session.Entry, now time.Time) error {
	if e.cfg.Rex.StuckNotifyMinutes <= 0 || ctx.Err() != nil {
		return nil
	}
	var message string
	_, err := e.reg.Update(entry.ID, func(x *session.Entry) {
		if x.Closed || x.Asked == "" || x.QuestionSince.IsZero() || x.QuestionNotified || now.Sub(x.QuestionSince) < time.Duration(e.cfg.Rex.StuckNotifyMinutes)*time.Minute {
			return
		}
		x.QuestionNotified = true
		message = x.ID + ": " + oneLine(x.Asked)
	})
	if err != nil {
		return err
	}
	if message != "" {
		notifyPerson("duolog", message)
	}
	return nil
}

// Notification text is data, never shell source. A missing notifier or a
// desktop that refuses notifications does not interrupt a peer wait.
func notifyOS(title, message string) {
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
	runner.ProcessTree(cmd)
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
