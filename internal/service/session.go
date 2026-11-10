package service

import (
	"context"
	"log"
	"time"

	"github.com/godbus/dbus/v5"
)

// watchSession calls onLock when a session locks its screen or the machine
// is about to suspend, as logind reports it on the system bus. GNOME and KDE
// set the session's LockedHint when the lock screen comes up; `loginctl
// lock-session` sends Lock. It reconnects when the bus goes away.
func watchSession(ctx context.Context, onLock func(reason string)) {
	for ctx.Err() == nil {
		if err := watchSessionOnce(ctx, onLock); err != nil && ctx.Err() == nil {
			log.Printf("watching logind for screen lock: %v (retrying in a minute)", err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(time.Minute):
		}
	}
}

func watchSessionOnce(ctx context.Context, onLock func(reason string)) error {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return err
	}
	defer conn.Close()

	matches := [][]dbus.MatchOption{
		{
			dbus.WithMatchInterface("org.freedesktop.DBus.Properties"),
			dbus.WithMatchMember("PropertiesChanged"),
			dbus.WithMatchPathNamespace("/org/freedesktop/login1/session"),
		},
		{
			dbus.WithMatchInterface("org.freedesktop.login1.Session"),
			dbus.WithMatchMember("Lock"),
			dbus.WithMatchPathNamespace("/org/freedesktop/login1/session"),
		},
		{
			dbus.WithMatchInterface("org.freedesktop.login1.Manager"),
			dbus.WithMatchMember("PrepareForSleep"),
		},
	}
	for _, m := range matches {
		if err := conn.AddMatchSignal(m...); err != nil {
			return err
		}
	}

	signals := make(chan *dbus.Signal, 16)
	conn.Signal(signals)
	for {
		select {
		case <-ctx.Done():
			return nil
		case sig, ok := <-signals:
			if !ok {
				return dbus.ErrClosed
			}
			if reason := lockReason(sig); reason != "" {
				onLock(reason)
			}
		}
	}
}

// lockReason returns why a logind signal means "forget secrets", or "".
func lockReason(sig *dbus.Signal) string {
	switch sig.Name {
	case "org.freedesktop.login1.Session.Lock":
		return "session locked"
	case "org.freedesktop.login1.Manager.PrepareForSleep":
		if len(sig.Body) > 0 {
			if start, ok := sig.Body[0].(bool); ok && start {
				return "suspend"
			}
		}
	case "org.freedesktop.DBus.Properties.PropertiesChanged":
		if len(sig.Body) < 2 {
			return ""
		}
		if iface, _ := sig.Body[0].(string); iface != "org.freedesktop.login1.Session" {
			return ""
		}
		changed, _ := sig.Body[1].(map[string]dbus.Variant)
		if v, ok := changed["LockedHint"]; ok {
			if locked, _ := v.Value().(bool); locked {
				return "screen locked"
			}
		}
	}
	return ""
}
