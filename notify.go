package main

import (
	"fmt"
	"os/exec"
	"runtime"
)

func Notify(title, message string) {
	if !appConfig.Notifications {
		return
	}

	switch runtime.GOOS {
	case "darwin":
		script := fmt.Sprintf(`display notification "%s" with title "%s" sound name "default"`, message, title)
		exec.Command("osascript", "-e", script).Start()
	case "linux":
		exec.Command("notify-send", "-a", "ZenTorrent", title, message).Start()
	case "windows":
		ps := fmt.Sprintf(`[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null; `+
			`$template = [Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent(0); `+
			`$text = $template.GetElementsByTagName("text"); `+
			`$text.Item(0).AppendChild($template.CreateTextNode("%s - %s")); `+
			`$toast = [Windows.UI.Notifications.ToastNotification]::new($template); `+
			`[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier("ZenTorrent").Show($toast)`,
			title, message)
		exec.Command("powershell", "-Command", ps).Start()
	}
}
