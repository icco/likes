package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.icco.me/trakt"
)

func validTestToken() *trakt.Token {
	return &trakt.Token{
		AccessToken: "test-access", RefreshToken: "test-refresh",
		CreatedAt: time.Now().Unix(), ExpiresIn: 7 * 24 * 60 * 60,
	}
}

func TestStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "likes", "trakt.json")
	got, err := load(path)
	if err != nil || !reflect.DeepEqual(got, state{}) {
		t.Fatalf("missing state = %+v, %v", got, err)
	}
	got, err = load(filepath.Join(dir, "missing.json"))
	if err != nil || !reflect.DeepEqual(got, state{}) {
		t.Fatalf("missing file in existing directory = %+v, %v", got, err)
	}
	want := state{Token: validTestToken(), Skipped: []int{12, -34}}
	if err := save(path, want); err != nil {
		t.Fatal(err)
	}
	got, err = load(path)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("loaded state = %+v, %v; want %+v", got, err, want)
	}
	for _, tt := range []struct {
		path string
		mode os.FileMode
	}{{filepath.Dir(path), 0o700}, {path, 0o600}} {
		info, err := os.Stat(tt.path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != tt.mode {
			t.Errorf("%s permissions = %o, want %o", tt.path, info.Mode().Perm(), tt.mode)
		}
	}
	if err := save(path, state{}); err != nil {
		t.Fatal(err)
	}
	got, err = load(path)
	if err != nil || !reflect.DeepEqual(got, state{}) {
		t.Fatalf("empty state = %+v, %v", got, err)
	}
}

func TestStateFileErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte("{invalid"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := load(path); err == nil {
		t.Fatal("invalid JSON loaded successfully")
	}
	if _, err := load(dir); err == nil {
		t.Fatal("directory loaded as state")
	}
	if err := save(filepath.Join(path, "state.json"), state{}); err == nil {
		t.Fatal("save succeeded with a file as parent")
	}
	if err := save(dir, state{}); err == nil {
		t.Fatal("save overwrote a directory")
	}
	if _, err := load(filepath.Join(path, "child")); err == nil {
		t.Fatal("load succeeded with a file as parent")
	}
	t.Run("permission denied", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root bypasses file permission checks")
		}
		if err := os.Chmod(path, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := os.Chmod(path, 0o600); err != nil {
				t.Error(err)
			}
		})
		if _, err := load(path); !errors.Is(err, os.ErrPermission) {
			t.Errorf("load error = %v, want permission denied", err)
		}
		if err := save(path, state{}); !errors.Is(err, os.ErrPermission) {
			t.Errorf("save error = %v, want permission denied", err)
		}
	})
}

func TestEnsureTokenValid(t *testing.T) {
	tc := trakt.NewClient("client", "secret")
	tc.BaseURL = "://invalid"
	tok := validTestToken()
	st := state{Token: tok}
	if err := ensureToken(t.Context(), tc, &st); err != nil {
		t.Fatal(err)
	}
	if st.Token != tok {
		t.Fatal("valid token was replaced")
	}
}

func TestEnsureTokenFlow(t *testing.T) {
	for _, tt := range []struct {
		name        string
		refresh     bool
		refreshFail bool
		deviceCode  string
		pollStatus  int
		pending     bool
		cancel      bool
		wantErr     string
	}{
		{name: "refresh", refresh: true},
		{name: "device"},
		{name: "failed refresh falls back", refresh: true, refreshFail: true},
		{name: "pending then authorized", pending: true},
		{name: "device request rejected", deviceCode: "rejected", wantErr: "device code:"},
		{name: "invalid device response", deviceCode: "invalid", wantErr: "device code:"},
		{name: "expired device", deviceCode: "expired", wantErr: "device code expired"},
		{name: "poll rejected", pollStatus: http.StatusForbidden, wantErr: "poll token:"},
		{name: "canceled while waiting", cancel: true, wantErr: context.Canceled.Error()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			want := validTestToken()
			var paths []string
			polls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				paths = append(paths, r.URL.Path)
				if r.Method != http.MethodPost {
					t.Errorf("method = %s, want POST", r.Method)
				}
				var body map[string]string
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body["client_id"] != "client" {
					t.Errorf("client_id = %q", body["client_id"])
				}
				switch r.URL.Path {
				case "/oauth/token":
					if body["refresh_token"] != "old-refresh" || body["grant_type"] != "refresh_token" || body["client_secret"] != "secret" {
						t.Errorf("refresh body = %v", body)
					}
					if tt.refreshFail {
						w.WriteHeader(http.StatusUnauthorized)
						return
					}
					writeMainTestJSON(t, w, want)
				case "/oauth/device/code":
					switch tt.deviceCode {
					case "rejected":
						w.WriteHeader(http.StatusForbidden)
						return
					case "invalid":
						if _, err := io.WriteString(w, "{"); err != nil {
							t.Error(err)
						}
						return
					}
					expires := 30
					if tt.deviceCode == "expired" {
						expires = 0
					}
					writeMainTestJSON(t, w, trakt.DeviceCode{
						DeviceCode: "device", UserCode: "user", VerificationURL: "https://example.test/activate",
						ExpiresIn: expires, Interval: 0,
					})
					if tt.cancel {
						time.AfterFunc(100*time.Millisecond, cancel)
					}
				case "/oauth/device/token":
					polls++
					if body["code"] != "device" || body["client_secret"] != "secret" {
						t.Errorf("poll body = %v", body)
					}
					if tt.pollStatus != 0 {
						w.WriteHeader(tt.pollStatus)
						return
					}
					if tt.pending && polls == 1 {
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					writeMainTestJSON(t, w, want)
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer srv.Close()
			tc := trakt.NewClient("client", "secret")
			tc.BaseURL = srv.URL
			st := state{}
			if tt.refresh {
				st.Token = &trakt.Token{RefreshToken: "old-refresh", CreatedAt: time.Now().Unix(), ExpiresIn: 3600}
			}
			err := ensureToken(ctx, tc, &st)
			// Wait for handlers to finish before inspecting their request log.
			srv.Close()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ensureToken error = %v, want %q", err, tt.wantErr)
				}
				if tt.cancel && !errors.Is(err, context.Canceled) {
					t.Errorf("waiting did not return context cancellation: %v", err)
				}
				if st.Token != nil {
					t.Errorf("failed authorization replaced token: %+v", st.Token)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(st.Token, want) {
				t.Fatalf("token = %+v, error = %v, want %+v", st.Token, err, want)
			}
			wantPaths := []string{"/oauth/device/code", "/oauth/device/token"}
			if tt.pending {
				wantPaths = append(wantPaths, "/oauth/device/token")
			}
			if tt.refresh {
				if tt.refreshFail {
					wantPaths = append([]string{"/oauth/token"}, wantPaths...)
				} else {
					wantPaths = []string{"/oauth/token"}
				}
			}
			if !reflect.DeepEqual(paths, wantPaths) {
				t.Errorf("requests = %v, want %v", paths, wantPaths)
			}
		})
	}
}

func writeMainTestJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Error(err)
	}
}

type mainTestTransport func(*http.Request) (*http.Response, error)

func (f mainTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestRun(t *testing.T) {
	for _, tt := range []struct {
		name    string
		setup   string
		input   string
		wantErr string
	}{
		{name: "missing client id", setup: "no id", wantErr: "TRAKT_CLIENT_ID and TRAKT_CLIENT_SECRET are required"},
		{name: "missing secret", setup: "no secret", wantErr: "TRAKT_CLIENT_ID and TRAKT_CLIENT_SECRET are required"},
		{name: "missing config directory", setup: "no config", wantErr: "neither $XDG_CONFIG_HOME nor $HOME"},
		{name: "invalid state", setup: "invalid", wantErr: "invalid character"},
		{name: "authentication failure", setup: "no token", wantErr: "device code:"},
		{name: "save failure", setup: "save failure", wantErr: "is a directory"},
		{name: "candidate request failure", setup: "api failure", wantErr: "HTTP 503"},
		{name: "all candidates skipped", setup: "skipped"},
		{name: "skip and rate", input: "s\n8\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("TRAKT_CLIENT_ID", "client")
			t.Setenv("TRAKT_CLIENT_SECRET", "secret")
			t.Setenv("XDG_CONFIG_HOME", dir)
			t.Setenv("HOME", dir)
			path := filepath.Join(dir, "likes", "trakt.json")
			st := state{Token: validTestToken()}
			if tt.setup == "skipped" {
				st.Skipped = []int{1, -2}
			}
			if tt.setup == "no token" || tt.setup == "save failure" {
				st.Token = nil
			}
			if err := save(path, st); err != nil {
				t.Fatal(err)
			}
			switch tt.setup {
			case "no id":
				t.Setenv("TRAKT_CLIENT_ID", "")
			case "no secret":
				t.Setenv("TRAKT_CLIENT_SECRET", "")
			case "no config":
				t.Setenv("XDG_CONFIG_HOME", "")
				t.Setenv("HOME", "")
			case "invalid":
				if err := os.WriteFile(path, []byte("invalid"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			input, err := os.CreateTemp(dir, "input")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := input.Close(); err != nil {
					t.Error(err)
				}
			})
			if _, err := input.WriteString(tt.input); err != nil {
				t.Fatal(err)
			}
			if _, err := input.Seek(0, io.SeekStart); err != nil {
				t.Fatal(err)
			}
			oldStdin := os.Stdin
			os.Stdin = input
			t.Cleanup(func() { os.Stdin = oldStdin })
			var rated int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/oauth/device/code":
					if tt.setup != "save failure" {
						w.WriteHeader(http.StatusForbidden)
						return
					}
					writeMainTestJSON(t, w, trakt.DeviceCode{DeviceCode: "device", ExpiresIn: 30, Interval: 1})
				case "/oauth/device/token":
					if err := os.Remove(path); err != nil {
						t.Error(err)
					}
					if err := os.Mkdir(path, 0o700); err != nil {
						t.Error(err)
					}
					writeMainTestJSON(t, w, validTestToken())
				case "/sync/ratings/movies", "/sync/ratings/shows":
					if tt.setup == "api failure" {
						w.WriteHeader(http.StatusServiceUnavailable)
						return
					}
					writeMainTestJSON(t, w, []row{})
				case "/sync/watched/movies":
					writeMainTestJSON(t, w, []map[string]any{{"movie": map[string]any{"title": "Movie", "ids": map[string]int{"trakt": 1}}}})
				case "/sync/watched/shows":
					writeMainTestJSON(t, w, []map[string]any{{"show": map[string]any{"title": "Show", "ids": map[string]int{"trakt": 2}}}})
				case "/sync/ratings":
					rated++
					if r.Method != http.MethodPost {
						t.Errorf("rating method = %s", r.Method)
					}
					var body map[string][]struct {
						Rating int `json:"rating"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					} else if len(body["shows"]) != 1 || body["shows"][0].Rating != 8 {
						t.Errorf("rating body = %+v", body)
					}
				default:
					t.Errorf("unexpected request %s", r.URL)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer srv.Close()
			target, err := url.Parse(srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			transport := http.DefaultTransport
			http.DefaultTransport = mainTestTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "api.trakt.tv" {
					t.Errorf("unexpected API host %s", r.URL.Host)
				}
				clone := r.Clone(r.Context())
				clone.URL.Scheme = target.Scheme
				clone.URL.Host = target.Host
				return transport.RoundTrip(clone)
			})
			t.Cleanup(func() { http.DefaultTransport = transport })
			err = run()
			srv.Close()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("run error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := load(path)
			if err != nil {
				t.Fatal(err)
			}
			wantSkipped, wantRated := []int{1, -2}, 0
			if tt.setup != "skipped" {
				wantSkipped, wantRated = []int{1}, 1
			}
			if !reflect.DeepEqual(got.Skipped, wantSkipped) || !reflect.DeepEqual(got.Token, st.Token) || rated != wantRated {
				t.Errorf("persisted = %+v, rated = %d; want skipped %v, rated %d", got, rated, wantSkipped, wantRated)
			}
		})
	}
}
