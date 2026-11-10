// Package svcapi is the client side of the silo service: where its sockets
// are, the types it answers with, and a client for the CLI.
package svcapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

// Version is the version of this silo binary. The CLI sets it, so the service
// and its clients can tell when they come from different builds.
var Version = "dev"

// StatusInfo is what `silo service status` shows.
type StatusInfo struct {
	Version string    `json:"version"`
	PID     int       `json:"pid"`
	Started time.Time `json:"started"`
	// CachedSecrets is how many 1Password values the service holds.
	CachedSecrets int `json:"cached_secrets"`
	// Incus says whether the service is connected to Incus.
	Incus bool `json:"incus"`
	// Containers are the containers whose sockets the service serves.
	Containers []string `json:"containers"`
	// BaseRefreshes are the images whose base template is being rebuilt.
	BaseRefreshes []string `json:"base_refreshes"`
}

// Client talks to the silo service over its control socket.
type Client struct {
	http *http.Client
	// Version of the service this client is connected to.
	Version string
}

// ErrUnavailable means the service isn't running (or isn't installed).
var ErrUnavailable = errors.New("the silo service is not running (see `silo service status`)")

// Dial connects to the service at the control socket without checking its
// version. It fails fast when nothing listens there.
func Dial() (*Client, error) {
	return dialPath(SocketPath())
}

func dialPath(path string) (*Client, error) {
	c := &Client{http: &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", path)
			},
		},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var st StatusInfo
	if err := c.Do(ctx, "GET", "/v1/status", nil, &st); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	c.Version = st.Version
	return c, nil
}

var (
	sharedOnce   sync.Once
	sharedClient *Client
)

// Connect returns a client for the service, or nil when it isn't running, so
// callers can fall back to doing the work themselves. The connection is made
// once per process.
//
// A service from another silo build (the RPM was upgraded, or you rebuilt
// silo) is asked to stop first. systemd starts the new binary on the next
// connection through silo.socket. If the version still differs afterwards,
// the service runs another binary than this one (e.g. a dev build next to the
// RPM), and it's used as is.
func Connect() *Client {
	sharedOnce.Do(func() {
		c, err := Dial()
		if err != nil {
			return
		}
		if c.Version != Version {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			c.Do(ctx, "POST", "/v1/shutdown", nil, nil)
			cancel()
			for range 20 {
				time.Sleep(100 * time.Millisecond)
				if nc, err := Dial(); err == nil {
					c = nc
					break
				}
			}
		}
		sharedClient = c
	})
	return sharedClient
}

// Do sends a JSON request and decodes the JSON response into out (if not nil).
func (c *Client) Do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://silo"+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return &RemoteError{Status: resp.StatusCode, Msg: e.Error}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// RemoteError is an error the service returned.
type RemoteError struct {
	Status int
	Msg    string
}

func (e *RemoteError) Error() string { return e.Msg }

// Stream opens a long-running GET request (e.g. the event stream) and returns
// the response body.
func (c *Client) Stream(ctx context.Context, path string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "http://silo"+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, &RemoteError{Status: resp.StatusCode, Msg: resp.Status}
	}
	return resp.Body, nil
}

// SecretRef names a cached secret: a 1Password reference, read with the given
// op account (OP_ACCOUNT, may be empty).
type SecretRef struct {
	Ref     string `json:"ref"`
	Account string `json:"account,omitempty"`
}

// SecretValue answers a cache lookup.
type SecretValue struct {
	Value string `json:"value"`
	Found bool   `json:"found"`
}

// SecretPut stores a secret in the cache.
type SecretPut struct {
	Ref   SecretRef `json:"ref"`
	Value string    `json:"value"`
}

// ForgetResult says how many cached secrets were dropped.
type ForgetResult struct {
	Count int `json:"count"`
}

// CachedSecret looks a secret up in the service's cache.
func (c *Client) CachedSecret(ref SecretRef) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var v SecretValue
	if err := c.Do(ctx, "POST", "/v1/secrets/get", ref, &v); err != nil {
		return "", false
	}
	return v.Value, v.Found
}

// CacheSecret stores a secret in the service's cache.
func (c *Client) CacheSecret(ref SecretRef, value string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c.Do(ctx, "POST", "/v1/secrets/put", SecretPut{Ref: ref, Value: value}, nil)
}

// BaseRefresh asks the service to refresh the base template of an image.
type BaseRefresh struct {
	Image string `json:"image"`
}

// RefreshBase has the service rebuild the base template for an image in the
// background (if it's old enough; `silo base refresh` decides).
func (c *Client) RefreshBase(image string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return c.Do(ctx, "POST", "/v1/base/refresh", BaseRefresh{Image: image}, nil)
}

// ContainerRef names a container.
type ContainerRef struct {
	Name string `json:"name"`
}

// StartingContainer tells the service that silo itself is about to start a
// container and takes care of its daemons, so the service leaves that start
// alone.
func (c *Client) StartingContainer(name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c.Do(ctx, "POST", "/v1/containers/starting", ContainerRef{Name: name}, nil)
}
