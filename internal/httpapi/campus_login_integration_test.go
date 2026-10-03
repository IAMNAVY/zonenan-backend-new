package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"zonenan-backend/internal/auth"
	"zonenan-backend/internal/config"
	"zonenan-backend/internal/db"
	"zonenan-backend/internal/ids"
)

// Runs only with an explicitly configured, disposable loopback database.
func TestCampusClaimIntegration(t *testing.T) {
	dsn := os.Getenv("ZONENAN_CAMPUS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set ZONENAN_CAMPUS_TEST_DATABASE_URL to a disposable local database")
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Hostname() != "127.0.0.1" || u.Path != "/zonenan_campus_test" {
		t.Fatal("requires a dedicated loopback test database")
	}
	ctx := context.Background()
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err = pool.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = pool.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{JWTSigningKey: "synthetic-integration-key", JWTExpire: time.Hour, GradePepper: "synthetic-pepper", MapTileCacheDir: t.TempDir()}
	s := New(cfg, pool)
	router := s.Router()
	request := func(method, path, token string, body any, want int) map[string]any {
		t.Helper()
		payload, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(payload))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		var result struct {
			Data map[string]any `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &result)
		return result.Data
	}
	student := fmt.Sprintf("test%d", time.Now().UnixNano())
	hash := auth.StudentHash(student, cfg.GradePepper)
	defer pool.Exec(ctx, `DELETE FROM device_login_challenges WHERE student_hash=$1`, hash)
	defer pool.Exec(ctx, `DELETE FROM trusted_devices WHERE student_hash=$1`, hash)
	defer pool.Exec(ctx, `DELETE FROM email_codes WHERE email=$1`, campusEmail(student))
	// Concurrent registrations on different devices must converge on one account.
	var wg sync.WaitGroup
	accountIDs := make(chan int64, 6)
	errs := make(chan error, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			user, _, err := s.users.ReserveCampusClaim(ctx, hash)
			if err != nil {
				errs <- err
				return
			}
			accountIDs <- user.ID
		}()
	}
	wg.Wait()
	close(accountIDs)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	var uid int64
	for id := range accountIDs {
		if uid != 0 && uid != id {
			t.Fatal("duplicate campus accounts")
		}
		uid = id
	}
	defer pool.Exec(ctx, `DELETE FROM zonenan_users WHERE id=$1`, uid)
	loginBody := map[string]any{"student_id": student, "device_info": "test-device-a", "device_name": "synthetic device"}
	claimResponse := request("POST", "/auth/campus-login", "", loginBody, 200)
	claimToken := claimResponse["token"].(string)
	profile := claimResponse["user"].(map[string]any)
	if profile["campus_verified"] != false || int64(profile["id"].(float64)) != uid {
		t.Fatal("invalid registration response")
	}
	var verified bool
	pool.QueryRow(ctx, `SELECT verified FROM zonenan_identities WHERE provider='cas' AND provider_uid=$1`, hash).Scan(&verified)
	if verified {
		t.Fatal("registration verified school identity")
	}
	var count int
	pool.QueryRow(ctx, `SELECT count(*) FROM email_codes WHERE email=$1`, campusEmail(student)).Scan(&count)
	if count != 0 {
		t.Fatal("login sent email")
	}
	request("POST", "/grade/search", claimToken, map[string]any{}, 403)
	request("POST", "/auth/bind-email", claimToken, map[string]any{}, 403)
	request("POST", "/auth/campus-verify", claimToken, map[string]any{"student_id": "different-student"}, 403)
	// Existing lifetime usage must survive verification, including registration
	// records created before this flow was introduced.
	if _, err = pool.Exec(ctx, `INSERT INTO grade_free_queries(user_id,device_fingerprint) VALUES ($1,'test-device-a')`, uid); err != nil {
		t.Fatal(err)
	}
	challenge := request("POST", "/auth/campus-verify", claimToken, map[string]any{"student_id": student, "device_name": "synthetic device"}, 200)
	credentials := map[string]any{"challenge_id": challenge["challenge_id"], "secret": challenge["secret"], "student_id": student, "code": "123456"}
	request("POST", "/auth/device-challenge/finish", "", credentials, 409)
	if err = s.codes.Save(ctx, campusEmail(student), "123456", "device_verify", time.Minute); err != nil {
		t.Fatal(err)
	}
	request("POST", "/auth/device-challenge/email/verify", "", credentials, 200)
	full := request("POST", "/auth/device-challenge/finish", "", credentials, 200)
	fullToken := full["token"].(string)
	if parsed, err := s.tokens.Parse(fullToken); err != nil || parsed != uid {
		t.Fatalf("verification changed account: %v", err)
	}
	request("POST", "/auth/device-challenge/finish", "", credentials, 409)
	request("GET", "/auth/me", fullToken, nil, 200)
	if _, err = pool.Exec(ctx, `UPDATE zonenan_users SET nickname='private nickname',role='admin' WHERE id=$1`, uid); err != nil {
		t.Fatal(err)
	}
	second := request("POST", "/auth/campus-login", "", map[string]any{"student_id": student, "device_info": "test-device-b"}, 200)
	secondToken := second["token"].(string)
	secondProfile := request("GET", "/auth/me", secondToken, nil, 200)
	if secondProfile["nickname"] == "private nickname" || secondProfile["role"] != "user" || secondProfile["campus_verified"] != false {
		t.Fatal("new device inherited private account state")
	}
	request("GET", "/membership/me", secondToken, nil, 403)
	request("GET", "/grade/auth-status", secondToken, nil, 403)
	secondChallenge := request("POST", "/auth/campus-verify", secondToken, map[string]any{"student_id": student}, 200)
	secondCredentials := map[string]any{"challenge_id": secondChallenge["challenge_id"], "secret": secondChallenge["secret"], "student_id": student, "code": "654321"}
	if err = s.codes.Save(ctx, campusEmail(student), "654321", "device_verify", time.Minute); err != nil {
		t.Fatal(err)
	}
	request("POST", "/auth/device-challenge/email/verify", "", secondCredentials, 200)
	secondFull := request("POST", "/auth/device-challenge/finish", "", secondCredentials, 200)
	if parsed, err := s.tokens.Parse(secondFull["token"].(string)); err != nil || parsed != uid {
		t.Fatal("second device changed account")
	}
	resumed := request("POST", "/auth/campus-login", fullToken, loginBody, 200)
	if _, err = s.tokens.Parse(resumed["token"].(string)); err != nil {
		t.Fatal("valid ZoneNaN credential did not resume account")
	}
	pool.QueryRow(ctx, `SELECT count(*) FROM grade_free_queries WHERE user_id=$1`, uid).Scan(&count)
	if count != 1 {
		t.Fatal("lifetime usage reset")
	}
	// The old CAS endpoint still validates tickets and logs into the same account.
	schoolCalls := 0
	school := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		schoolCalls++
		if r.URL.Path != "/serviceValidate" || r.URL.Query().Get("ticket") != "synthetic-ticket" {
			t.Error("unexpected school verification")
		}
		fmt.Fprintf(w, "<serviceResponse><authenticationSuccess><user>%s</user></authenticationSuccess></serviceResponse>", student)
	}))
	defer school.Close()
	s.ids = ids.NewVerifierWithCASService(school.URL, "", "", "https://school.invalid/service")
	legacy := request("POST", "/auth/cas-login", "", map[string]any{"student_id": student, "cas_ticket": "synthetic-ticket", "device_info": "test-device-a", "defer_device_email": true}, 200)
	if parsed, err := s.tokens.Parse(legacy["token"].(string)); err != nil || parsed != uid || schoolCalls != 1 {
		t.Fatal("legacy CAS compatibility or credential validation failed")
	}
	// Old sessions stay restricted after another device verifies the account.
	request("POST", "/grade/search", claimToken, map[string]any{}, 403)
}
