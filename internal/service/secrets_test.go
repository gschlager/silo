package service

import (
	"context"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/gschlager/silo/internal/svcapi"
)

func TestSecretCacheExpiresAndForgets(t *testing.T) {
	c := newSecretCache()
	now := time.Unix(1000, 0)
	c.now = func() time.Time { return now }

	a := svcapi.SecretRef{Ref: "op://v/i/f"}
	b := svcapi.SecretRef{Ref: "op://v/i/f", Account: "work"}
	c.put(a, "one")
	c.put(b, "two")
	if v, ok := c.get(a); !ok || v != "one" {
		t.Fatalf("get a = %q %v", v, ok)
	}
	if v, ok := c.get(b); !ok || v != "two" {
		t.Fatalf("the account is part of the key: get b = %q %v", v, ok)
	}

	now = now.Add(secretMaxAge + time.Second)
	if _, ok := c.get(a); ok {
		t.Error("expired secret still returned")
	}

	c.put(a, "one")
	if n := c.forget("test"); n != 2 {
		t.Errorf("forget = %d, want 2", n)
	}
	if _, ok := c.get(a); ok {
		t.Error("secret still there after forget")
	}
}

func TestSecretEndpoints(t *testing.T) {
	c := startTestServer(t)
	ref := svcapi.SecretRef{Ref: "op://v/i/f"}
	if _, ok := c.CachedSecret(ref); ok {
		t.Fatal("found a secret in an empty cache")
	}
	c.CacheSecret(ref, "s3cret")
	if v, ok := c.CachedSecret(ref); !ok || v != "s3cret" {
		t.Fatalf("CachedSecret = %q %v", v, ok)
	}
	var res svcapi.ForgetResult
	if err := c.Do(context.Background(), "POST", "/v1/secrets/forget", nil, &res); err != nil || res.Count != 1 {
		t.Fatalf("forget = %+v %v", res, err)
	}
	if _, ok := c.CachedSecret(ref); ok {
		t.Error("secret still cached after forget")
	}
}

func TestLockReason(t *testing.T) {
	sessionProps := func(changed map[string]dbus.Variant) *dbus.Signal {
		return &dbus.Signal{
			Name: "org.freedesktop.DBus.Properties.PropertiesChanged",
			Body: []any{"org.freedesktop.login1.Session", changed, []string{}},
		}
	}
	tests := []struct {
		name string
		sig  *dbus.Signal
		want bool
	}{
		{"locked hint", sessionProps(map[string]dbus.Variant{"LockedHint": dbus.MakeVariant(true)}), true},
		{"unlocked hint", sessionProps(map[string]dbus.Variant{"LockedHint": dbus.MakeVariant(false)}), false},
		{"other property", sessionProps(map[string]dbus.Variant{"Active": dbus.MakeVariant(true)}), false},
		{"lock", &dbus.Signal{Name: "org.freedesktop.login1.Session.Lock"}, true},
		{"suspend", &dbus.Signal{Name: "org.freedesktop.login1.Manager.PrepareForSleep", Body: []any{true}}, true},
		{"resume", &dbus.Signal{Name: "org.freedesktop.login1.Manager.PrepareForSleep", Body: []any{false}}, false},
	}
	for _, tt := range tests {
		if got := lockReason(tt.sig) != ""; got != tt.want {
			t.Errorf("%s: lockReason = %v, want %v", tt.name, got, tt.want)
		}
	}
}
