// Package notify sends best-effort local desktop notifications. It is used for
// opt-in reminder delivery; callers treat failures as non-fatal because a
// missing notifier must never block local task use.
package notify

import (
	"fmt"
	"os/exec"
	"runtime"
)

// Send shows a desktop notification with the given title and message. Only the
// task title is passed through here, never body text, so reminder delivery
// reveals no more than `tasks list` already shows on an unlocked device.
func Send(title, message string) error {
	switch runtime.GOOS {
	case "linux":
		return run("notify-send", "--", title, message)
	case "darwin":
		script := fmt.Sprintf("display notification %s with title %s", quote(message), quote(title))
		return run("osascript", "-e", script)
	case "windows":
		return run("powershell", "-NoProfile", "-Command", windowsToast(title, message))
	default:
		return fmt.Errorf("desktop notifications are not supported on %s", runtime.GOOS)
	}
}

func run(name string, args ...string) error {
	if _, err := exec.LookPath(name); err != nil {
		return fmt.Errorf("notification tool %q not found: %w", name, err)
	}
	if err := exec.Command(name, args...).Run(); err != nil {
		return fmt.Errorf("send notification via %s: %w", name, err)
	}
	return nil
}

// quote wraps a string as an AppleScript string literal.
func quote(s string) string {
	out := make([]rune, 0, len(s)+2)
	out = append(out, '"')
	for _, r := range s {
		if r == '"' || r == '\\' {
			out = append(out, '\\')
		}
		out = append(out, r)
	}
	out = append(out, '"')
	return string(out)
}

func windowsToast(title, message string) string {
	return fmt.Sprintf(
		"[reflection.assembly]::loadwithpartialname('System.Windows.Forms') | Out-Null; "+
			"[System.Windows.Forms.MessageBox]::Show(%s, %s) | Out-Null",
		quote(message), quote(title))
}
