package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/anup-a/byagent/internal/ids"
	"github.com/anup-a/byagent/internal/store"
)

// StateTTL bounds how long a started OAuth flow may take.
const StateTTL = 10 * time.Minute

const (
	githubAuthURL  = "https://github.com/login/oauth/authorize"
	githubTokenURL = "https://github.com/login/oauth/access_token"
	githubAPIURL   = "https://api.github.com"
	githubScope    = "read:user user:email"
)

var (
	// ErrBadState means the callback's state is unknown, used or expired.
	ErrBadState = errors.New("sign-in expired or was tampered with, try again")
	// ErrNoVerifiedEmail means the GitHub account has no verified address.
	ErrNoVerifiedEmail = errors.New("your GitHub account has no verified email address")
)

// Profile is what a completed GitHub sign-in yields.
type Profile struct {
	Subject string // GitHub numeric user id
	Email   string // primary verified address, lowercased
	Name    string
}

// GitHub runs the OAuth web flow. The URL fields default to github.com and
// exist so tests can point at a fake.
type GitHub struct {
	ClientID, ClientSecret string
	RedirectURL            string
	Store                  store.AuthStore
	Now                    func() time.Time
	AuthURL, TokenURL      string
	APIURL                 string
	HTTP                   *http.Client
}

// Start records a fresh state and returns the GitHub URL to send the user to.
func (g *GitHub) Start(ctx context.Context) (string, error) {
	state := ids.NewToken()
	now := g.Now()
	if err := g.Store.CreateOAuthState(ctx, state, now, now.Add(StateTTL)); err != nil {
		return "", err
	}
	q := url.Values{"client_id": {g.ClientID}, "redirect_uri": {g.RedirectURL}, "scope": {githubScope}, "state": {state}}
	return orDefault(g.AuthURL, githubAuthURL) + "?" + q.Encode(), nil
}

// Callback verifies the state, trades the code for a token and reads the
// user's id and verified primary email.
func (g *GitHub) Callback(ctx context.Context, state, code string) (Profile, error) {
	if state == "" || code == "" {
		return Profile{}, ErrBadState
	}
	if err := g.Store.ConsumeOAuthState(ctx, state, g.Now()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return Profile{}, ErrBadState
		}
		return Profile{}, err
	}
	token, err := g.exchange(ctx, code)
	if err != nil {
		return Profile{}, err
	}
	var user struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	if err := g.get(ctx, token, "/user", &user); err != nil {
		return Profile{}, err
	}
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := g.get(ctx, token, "/user/emails", &emails); err != nil {
		return Profile{}, err
	}
	email := ""
	for _, e := range emails {
		if !e.Verified {
			continue
		}
		if e.Primary || email == "" {
			email = strings.ToLower(e.Email)
		}
	}
	if email == "" {
		return Profile{}, ErrNoVerifiedEmail
	}
	return Profile{Subject: strconv.FormatInt(user.ID, 10), Email: email, Name: user.Name}, nil
}

func (g *GitHub) exchange(ctx context.Context, code string) (string, error) {
	form := url.Values{"client_id": {g.ClientID}, "client_secret": {g.ClientSecret}, "code": {code}, "redirect_uri": {g.RedirectURL}}
	req, err := http.NewRequestWithContext(ctx, "POST", orDefault(g.TokenURL, githubTokenURL), strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	var out struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := g.do(req, &out); err != nil {
		return "", err
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("github: code exchange failed: %s", orDefault(out.Error, "no access token"))
	}
	return out.AccessToken, nil
}

func (g *GitHub) get(ctx context.Context, token, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", orDefault(g.APIURL, githubAPIURL)+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	return g.do(req, out)
}

func (g *GitHub) do(req *http.Request, out any) error {
	client := g.HTTP
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return err
	}
	if res.StatusCode >= 300 {
		return fmt.Errorf("github: %s returned %d", req.URL.Path, res.StatusCode)
	}
	return json.Unmarshal(body, out)
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
