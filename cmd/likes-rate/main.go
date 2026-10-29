package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"go.icco.me/trakt"
)

type state struct {
	Token   *trakt.Token `json:"token,omitempty"`
	Skipped []int        `json:"skipped,omitempty"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "likes-rate:", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	id, secret := os.Getenv("TRAKT_CLIENT_ID"), os.Getenv("TRAKT_CLIENT_SECRET")
	if id == "" || secret == "" {
		return errors.New("TRAKT_CLIENT_ID and TRAKT_CLIENT_SECRET are required")
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "likes", "trakt.json")
	st, err := load(path)
	if err != nil {
		return err
	}

	tc := trakt.NewClient(id, secret)
	if err := ensureToken(ctx, tc, &st); err != nil {
		return err
	}
	if err := save(path, st); err != nil {
		return err
	}

	a := api{base: tc.BaseURL, clientID: id, token: st.Token.AccessToken, hc: &http.Client{Timeout: 30 * time.Second}}
	skipped := map[int]bool{}
	for _, k := range st.Skipped {
		skipped[k] = true
	}
	cs, err := candidates(ctx, a, skipped)
	if err != nil {
		return err
	}
	if len(cs) == 0 {
		_, err := lipgloss.Println("Nothing left to rate.")
		return err
	}
	final, err := tea.NewProgram(newModel(ctx, a, cs, func(c candidate) error {
		st.Skipped = append(st.Skipped, skipKey(c.Kind, c.Media.IDs.Trakt))
		return save(path, st)
	}), tea.WithContext(ctx), tea.WithInput(os.Stdin), tea.WithOutput(os.Stdout)).Run()
	if err != nil {
		return err
	}
	m := final.(model)
	if _, err := lipgloss.Println(m.summary()); err != nil {
		return err
	}
	return m.err
}

func ensureToken(ctx context.Context, tc *trakt.Client, st *state) error {
	if st.Token != nil {
		if time.Until(st.Token.ExpiresAt()) > 24*time.Hour {
			return nil
		}
		if tok, err := tc.RefreshToken(ctx, st.Token.RefreshToken); err == nil {
			st.Token = tok
			return nil
		}
	}
	dc, err := tc.RequestDeviceCode(ctx)
	if err != nil {
		return fmt.Errorf("device code: %w", err)
	}
	fmt.Printf("Go to %s and enter %s\n", dc.VerificationURL, dc.UserCode)
	interval := time.Duration(max(dc.Interval, 1)) * time.Second
	deadline := time.Now().Add(time.Duration(dc.ExpiresIn) * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
		tok, err := tc.PollForToken(ctx, dc.DeviceCode)
		if err != nil {
			return fmt.Errorf("poll token: %w", err)
		}
		if tok != nil {
			st.Token = tok
			return nil
		}
	}
	return errors.New("device code expired")
}

func load(path string) (state, error) {
	var st state
	cleanPath := filepath.Clean(path)
	data, err := os.ReadFile(cleanPath) // #nosec G304
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	return st, json.Unmarshal(data, &st)
}

func save(path string, st state) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
