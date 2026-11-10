package service

import (
	"context"
	"os/exec"
	"time"
)

// desktopNotify shows a desktop notification. A variable, so tests can catch
// notifications.
var desktopNotify = notifySend

// notifySend shows a desktop notification through notify-send (or kdialog
// on KDE without it). It does nothing when neither is installed.
func notifySend(ctx context.Context, title, body string) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if path, err := exec.LookPath("notify-send"); err == nil {
		exec.CommandContext(ctx, path, "--app-name=silo", title, body).Run()
		return
	}
	if path, err := exec.LookPath("kdialog"); err == nil {
		exec.CommandContext(ctx, path, "--passivepopup", body, "5", "--title", title).Run()
	}
}
