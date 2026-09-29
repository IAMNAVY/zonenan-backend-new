package pushnotify

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"zonenan-backend/internal/store"
)

type serviceAccount struct {
	ProjectID   string `json:"project_id"`
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
	TokenURI    string `json:"token_uri"`
}

type Dispatcher struct {
	store   *store.PushNotificationStore
	account serviceAccount
	key     *rsa.PrivateKey
	client  *http.Client
	mu      sync.Mutex
	running bool
	token   string
	expires time.Time
}

func New(raw string, notifications *store.PushNotificationStore) *Dispatcher {
	d := &Dispatcher{store: notifications, client: &http.Client{Timeout: 15 * time.Second}}
	if strings.TrimSpace(raw) == "" {
		return d
	}
	if json.Unmarshal([]byte(raw), &d.account) != nil {
		return d
	}
	key, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(d.account.PrivateKey))
	if err == nil {
		d.key = key
	}
	if d.account.TokenURI == "" {
		d.account.TokenURI = "https://oauth2.googleapis.com/token"
	}
	return d
}

func (d *Dispatcher) Available() bool {
	return d.key != nil && d.account.ProjectID != "" && d.account.ClientEmail != ""
}

func (d *Dispatcher) Kick() {
	if !d.Available() {
		return
	}
	d.mu.Lock()
	if d.running {
		d.mu.Unlock()
		return
	}
	d.running = true
	d.mu.Unlock()
	go func() {
		defer func() {
			d.mu.Lock()
			d.running = false
			d.mu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		_ = d.dispatch(ctx)
	}()
}

func (d *Dispatcher) dispatch(ctx context.Context) error {
	for {
		items, err := d.store.PendingDeliveries(ctx, 100)
		if err != nil || len(items) == 0 {
			return err
		}
		accessToken, err := d.accessToken(ctx)
		if err != nil {
			return err
		}
		for _, item := range items {
			status, ref, code := d.sendFCM(ctx, accessToken, item)
			_ = d.store.MarkDelivery(ctx, item.MessageID, item.DeviceID, status, ref, code)
		}
	}
}

func (d *Dispatcher) accessToken(ctx context.Context) (string, error) {
	if d.token != "" && time.Now().Before(d.expires.Add(-time.Minute)) {
		return d.token, nil
	}
	now := time.Now()
	claims := jwt.MapClaims{
		"iss":   d.account.ClientEmail,
		"scope": "https://www.googleapis.com/auth/firebase.messaging",
		"aud":   d.account.TokenURI,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	}
	assertion, err := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(d.key)
	if err != nil {
		return "", err
	}
	form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"}, "assertion": {assertion}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, d.account.TokenURI, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := d.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if resp.StatusCode/100 != 2 || json.NewDecoder(resp.Body).Decode(&result) != nil || result.AccessToken == "" {
		return "", fmt.Errorf("FCM OAuth failed with status %d", resp.StatusCode)
	}
	d.token = result.AccessToken
	d.expires = now.Add(time.Duration(result.ExpiresIn) * time.Second)
	return d.token, nil
}

func (d *Dispatcher) sendFCM(ctx context.Context, accessToken string, item store.PendingPushDelivery) (string, string, string) {
	data := map[string]string{}
	for key, value := range item.Payload {
		data[key] = fmt.Sprint(value)
	}
	body, _ := json.Marshal(map[string]any{"message": map[string]any{
		"token":        item.Token,
		"notification": map[string]string{"title": item.Title, "body": item.Body},
		"data":         data,
		"android":      map[string]any{"priority": "high", "notification": map[string]string{"channel_id": "zonenan_remote"}},
		"apns":         map[string]any{"payload": map[string]any{"aps": map[string]any{"sound": "default"}}},
	}})
	endpoint := fmt.Sprintf("https://fcm.googleapis.com/v1/projects/%s/messages:send", url.PathEscape(d.account.ProjectID))
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.client.Do(req)
	if err != nil {
		return "failed", "", "transport"
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var result map[string]any
	_ = json.Unmarshal(raw, &result)
	if resp.StatusCode/100 == 2 {
		return "sent", fmt.Sprint(result["name"]), ""
	}
	text := string(raw)
	if strings.Contains(text, "UNREGISTERED") || strings.Contains(text, "registration-token-not-registered") {
		return "invalid_token", "", "unregistered"
	}
	return "failed", "", fmt.Sprintf("http_%d", resp.StatusCode)
}
