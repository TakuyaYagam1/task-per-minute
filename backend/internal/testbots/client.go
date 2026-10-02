package testbots

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
)

type apiError struct {
	Status    int
	Retry     time.Duration
	Operation string
}

func (e *apiError) Error() string { return fmt.Sprintf("API %s: status %d", e.Operation, e.Status) }

type client struct {
	base      string
	origin    string
	http      *http.Client
	cookie    string
	key       string
	mu        sync.Mutex
	wsCancel  context.CancelFunc
	resume    string
	connected bool
}

func newClient(cfg Config) *client {
	jar, _ := cookiejar.New(nil)
	base := cfg.Origin
	if base == "" {
		base = cfg.BackendURL
	}
	backend, _ := url.Parse(cfg.BackendURL)
	return &client{base: strings.TrimRight(base, "/"), origin: cfg.Origin,
		http: &http.Client{Jar: jar, Transport: backendTransport{backend: backend}, Timeout: 8 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}
}

// Keep cookies scoped to the public origin (including Secure on HTTPS stands),
// while routing the test client through the private Compose network, like the
// application's reverse proxy. No requests are sent to the public host.
type backendTransport struct{ backend *url.URL }

func (t backendTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if t.backend == nil || t.backend.Host == "" {
		return nil, errors.New("invalid backend address")
	}
	upstream := request.Clone(request.Context())
	upstream.URL.Scheme = t.backend.Scheme
	upstream.URL.Host = t.backend.Host
	upstream.Host = t.backend.Host
	return http.DefaultTransport.RoundTrip(upstream)
}

//nolint:gocyclo // Keep cookie, CSRF, idempotency and rate-limit handling in one client request boundary.
func (c *client) request(ctx context.Context, method, path string, body any, command string, out any) error {
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return errors.New("invalid request")
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(data)) //nolint:gosec // Configured origin and internal paths; transport pins every request to the configured backend.
	if err != nil {
		return errors.New("invalid backend address")
	}
	req.Header.Set("Origin", c.origin)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.cookie != "" {
		req.Header.Set("Cookie", c.cookie)
	}
	if c.key != "" {
		req.Header.Set("X-Test-Bots-Key", c.key)
	}
	if command != "" {
		req.Header.Set("Idempotency-Key", command)
	}
	for _, cookie := range c.http.Jar.Cookies(req.URL) {
		if cookie.Name == "tpm_player_csrf" {
			req.Header.Set("X-CSRF-Token", cookie.Value)
		}
	}
	resp, err := c.http.Do(req) //nolint:gosec // Redirects are disabled and backendTransport pins the destination to the private backend.
	if err != nil {
		return &apiError{Status: http.StatusServiceUnavailable, Retry: 5 * time.Second}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		retry := time.Second
		if seconds, parseErr := strconv.Atoi(resp.Header.Get("Retry-After")); parseErr == nil && seconds > 0 {
			retry = time.Duration(seconds) * time.Second
		} else if deadline, parseErr := http.ParseTime(resp.Header.Get("Retry-After")); parseErr == nil {
			retry = max(time.Second, time.Until(deadline))
		}
		return &apiError{Status: resp.StatusCode, Retry: retry, Operation: method + " " + path}
	}
	if out != nil {
		if err = json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out); err != nil {
			return errors.New("invalid backend response")
		}
	}
	return nil
}
func (c *client) login(ctx context.Context, account Account) error {
	var response struct {
		PlayerID string `json:"player_id"`
	}
	err := c.request(ctx, http.MethodPost, "/api/v1/players/login", map[string]string{"login": account.Username, "password": account.Password}, "", &response)
	if err != nil {
		return err
	}
	if response.PlayerID != account.PlayerID {
		return errors.New("account identity changed")
	}
	return nil
}
func (c *client) isConnected() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.connected }
func (c *client) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.wsCancel != nil {
		c.wsCancel()
		c.wsCancel = nil
	}
	c.connected = false
	c.resume = ""
}
func (c *client) connect(parent context.Context, tournament string) {
	c.mu.Lock()
	if c.wsCancel != nil {
		c.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	c.wsCancel = cancel
	c.mu.Unlock()
	go c.socketLoop(ctx, tournament)
}

//nolint:gocyclo // One reconnect loop owns resume state, presence, rejected cursors and backoff.
func (c *client) socketLoop(ctx context.Context, tournament string) {
	delay := time.Second
	for ctx.Err() == nil {
		endpoint, err := url.Parse(c.base + "/api/v1/tournaments/" + tournament + "/participant/realtime")
		if err != nil {
			return
		}
		c.mu.Lock()
		resume := c.resume
		c.mu.Unlock()
		if resume != "" {
			query := endpoint.Query()
			query.Set("resume_id", resume)
			endpoint.RawQuery = query.Encode()
		}
		headers := http.Header{"Origin": []string{c.origin}}
		dialCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		conn, resp, err := websocket.Dial(dialCtx, endpoint.String(), &websocket.DialOptions{HTTPClient: c.http, HTTPHeader: headers}) //nolint:bodyclose // coder/websocket owns the upgrade response body, including failed handshakes.
		cancel()
		if err == nil {
			delay = time.Second
			conn.SetReadLimit(8 << 20)
			for ctx.Err() == nil {
				_, data, readErr := conn.Read(ctx)
				if readErr != nil {
					break
				}
				var message struct {
					Type    string `json:"type"`
					Payload struct {
						Envelope struct {
							ResumeID     string `json:"resume_id"`
							TournamentID string `json:"tournament_id"`
						} `json:"envelope"`
					} `json:"payload"`
				}
				if json.Unmarshal(data, &message) != nil {
					continue
				}
				if message.Type == "tournament.rejected" {
					c.mu.Lock()
					c.resume = ""
					c.mu.Unlock()
					break
				}
				if message.Payload.Envelope.TournamentID == tournament && ctx.Err() == nil {
					c.mu.Lock()
					if validID(message.Payload.Envelope.ResumeID) {
						c.resume = message.Payload.Envelope.ResumeID
					}
					c.connected = true
					c.mu.Unlock()
				}
			}
			_ = conn.CloseNow()
		} else if resp != nil && (resp.StatusCode == http.StatusConflict || resp.StatusCode == http.StatusGone) {
			c.mu.Lock()
			c.resume = ""
			c.mu.Unlock()
		}
		if ctx.Err() != nil {
			return
		}
		if resp != nil && resp.StatusCode == http.StatusTooManyRequests {
			if seconds, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && seconds > 0 {
				delay = time.Duration(seconds) * time.Second
			} else if deadline, err := http.ParseTime(resp.Header.Get("Retry-After")); err == nil {
				delay = max(time.Second, time.Until(deadline))
			}
		}
		c.mu.Lock()
		c.connected = false
		c.mu.Unlock()
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		delay = min(delay*2, 5*time.Second)
	}
}

func newCommand() string { return uuid.NewString() }
