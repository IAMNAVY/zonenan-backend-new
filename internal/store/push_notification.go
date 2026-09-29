package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"zonenan-backend/internal/db"
)

var pushTopics = map[string]bool{
	"campus_incidents": true,
	"announcements":    true,
	"service_status":   true,
	"academic":         true,
}

type PushNotificationStore struct{ pool *db.Pool }

type PushMessageSummary struct {
	ID        int64  `json:"id"`
	Topic     string `json:"topic"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Status    string `json:"status"`
	Pending   int64  `json:"pending"`
	Sent      int64  `json:"sent"`
	Failed    int64  `json:"failed"`
	CreatedAt string `json:"created_at"`
}

type PendingPushDelivery struct {
	MessageID int64
	DeviceID  int64
	Provider  string
	Token     string
	Title     string
	Body      string
	Payload   map[string]any
}

func NewPushNotificationStore(pool *db.Pool) *PushNotificationStore {
	return &PushNotificationStore{pool: pool}
}

func (s *PushNotificationStore) ListMessages(ctx context.Context, limit int) ([]PushMessageSummary, error) {
	if limit < 1 || limit > 200 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `SELECT m.id,m.topic,m.title,m.body,m.status,
		COUNT(*) FILTER (WHERE d.status='pending'),COUNT(*) FILTER (WHERE d.status='sent'),
		COUNT(*) FILTER (WHERE d.status IN ('failed','invalid_token')),m.created_at::TEXT
		FROM push_messages m LEFT JOIN push_deliveries d ON d.message_id=m.id
		GROUP BY m.id ORDER BY m.id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PushMessageSummary{}
	for rows.Next() {
		var item PushMessageSummary
		if err := rows.Scan(&item.ID, &item.Topic, &item.Title, &item.Body, &item.Status, &item.Pending, &item.Sent, &item.Failed, &item.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PushNotificationStore) DeleteMessage(ctx context.Context, id int64) error {
	if id <= 0 {
		return errors.New("消息 ID 无效")
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM push_messages WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("推送消息不存在")
	}
	return nil
}

func (s *PushNotificationStore) RegisterDevice(ctx context.Context, userID int64, installationID, platform, provider, token, pollSecret, appVersion string, topics map[string]bool) error {
	installationID = strings.TrimSpace(installationID)
	platform = strings.ToLower(strings.TrimSpace(platform))
	provider = strings.ToLower(strings.TrimSpace(provider))
	token = strings.TrimSpace(token)
	pollSecret = strings.TrimSpace(pollSecret)
	if userID <= 0 || installationID == "" || len(installationID) > 200 || token == "" || len(token) > 4096 ||
		(platform != "android" && platform != "ios") ||
		(provider != "fcm" && provider != "apns" && provider != "poll") ||
		(platform == "android" && provider != "fcm") || (platform == "ios" && provider != "fcm" && provider != "apns") {
		if !(platform == "android" && provider == "poll" && token == "poll" && len(pollSecret) >= 32 && len(pollSecret) <= 512) {
			return errors.New("推送设备参数无效")
		}
	}
	pollHash := ""
	if provider == "poll" {
		sum := sha256.Sum256([]byte(pollSecret))
		pollHash = fmt.Sprintf("%x", sum[:])
		token = "poll:" + installationID
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// A provider token identifies one physical app installation. Transfer it when
	// the user changes accounts instead of leaving the old account subscribed.
	if _, err := tx.Exec(ctx, `
		DELETE FROM push_devices
		WHERE provider=$1 AND push_token=$2
		  AND NOT (user_id=$3 AND installation_id=$4 AND platform=$5)`,
		provider, token, userID, installationID, platform); err != nil {
		return err
	}
	var deviceID int64
	err = tx.QueryRow(ctx, `
		INSERT INTO push_devices(user_id,installation_id,platform,provider,push_token,poll_secret_hash,app_version)
		VALUES($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT(user_id,installation_id,platform) DO UPDATE SET
		 provider=EXCLUDED.provider,push_token=EXCLUDED.push_token,poll_secret_hash=EXCLUDED.poll_secret_hash,app_version=EXCLUDED.app_version,
		 enabled=TRUE,updated_at=NOW()
		RETURNING id`, userID, installationID, platform, provider, token, pollHash, strings.TrimSpace(appVersion)).Scan(&deviceID)
	if err != nil {
		return err
	}
	if topics == nil {
		topics = map[string]bool{"campus_incidents": true}
	}
	for topic, enabled := range topics {
		if !pushTopics[topic] {
			return errors.New("不支持的推送类别")
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO push_subscriptions(device_id,topic,enabled) VALUES($1,$2,$3)
			ON CONFLICT(device_id,topic) DO UPDATE SET enabled=EXCLUDED.enabled,updated_at=NOW()`, deviceID, topic, enabled); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

type PolledPushMessage struct {
	ID      int64          `json:"id"`
	Topic   string         `json:"topic"`
	Title   string         `json:"title"`
	Body    string         `json:"body"`
	Payload map[string]any `json:"payload"`
}

func pollSecretHash(secret string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(secret)))
	return fmt.Sprintf("%x", sum[:])
}

func (s *PushNotificationStore) Poll(ctx context.Context, installationID, secret string, limit int) ([]PolledPushMessage, error) {
	if strings.TrimSpace(installationID) == "" || len(strings.TrimSpace(secret)) < 32 {
		return nil, errors.New("推送轮询凭证无效")
	}
	if limit < 1 || limit > 50 {
		limit = 20
	}
	rows, err := s.pool.Query(ctx, `
		SELECT m.id,m.topic,m.title,m.body,m.payload
		FROM push_devices p
		JOIN push_deliveries d ON d.device_id=p.id AND d.status='pending'
		JOIN push_messages m ON m.id=d.message_id AND m.status IN ('queued','sending')
		WHERE p.installation_id=$1 AND p.provider='poll' AND p.enabled
		  AND p.poll_secret_hash=$2
		ORDER BY m.created_at LIMIT $3`, strings.TrimSpace(installationID), pollSecretHash(secret), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PolledPushMessage{}
	for rows.Next() {
		var item PolledPushMessage
		var raw []byte
		if err := rows.Scan(&item.ID, &item.Topic, &item.Title, &item.Body, &raw); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &item.Payload)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PushNotificationStore) AcknowledgePolled(ctx context.Context, installationID, secret string, messageIDs []int64) error {
	if len(messageIDs) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, messageID := range messageIDs {
		if messageID <= 0 {
			continue
		}
		var deviceID int64
		err := tx.QueryRow(ctx, `SELECT id FROM push_devices WHERE installation_id=$1 AND provider='poll' AND enabled AND poll_secret_hash=$2`, strings.TrimSpace(installationID), pollSecretHash(secret)).Scan(&deviceID)
		if err != nil {
			return errors.New("推送轮询凭证无效")
		}
		if _, err = tx.Exec(ctx, `UPDATE push_deliveries SET status='sent',provider_ref='poll',attempted_at=NOW() WHERE message_id=$1 AND device_id=$2 AND status='pending'`, messageID, deviceID); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE push_messages m SET status=CASE WHEN EXISTS(SELECT 1 FROM push_deliveries WHERE message_id=m.id AND status='pending') THEN 'sending' ELSE 'sent' END,sent_at=CASE WHEN NOT EXISTS(SELECT 1 FROM push_deliveries WHERE message_id=m.id AND status='pending') THEN NOW() ELSE sent_at END WHERE id=$1`, messageID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *PushNotificationStore) RemoveDevice(ctx context.Context, userID int64, installationID string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM push_devices WHERE user_id=$1 AND installation_id=$2`, userID, strings.TrimSpace(installationID))
	return err
}

func (s *PushNotificationStore) Preferences(ctx context.Context, userID int64, installationID string) (map[string]bool, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT s.topic,s.enabled FROM push_subscriptions s
		JOIN push_devices d ON d.id=s.device_id
		WHERE d.user_id=$1 AND d.installation_id=$2`, userID, strings.TrimSpace(installationID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{"campus_incidents": true, "announcements": false, "service_status": false, "academic": false}
	for rows.Next() {
		var topic string
		var enabled bool
		if err := rows.Scan(&topic, &enabled); err != nil {
			return nil, err
		}
		out[topic] = enabled
	}
	return out, rows.Err()
}

func (s *PushNotificationStore) Enqueue(ctx context.Context, topic, title, body, dedupKey string, payload map[string]any) (int64, error) {
	if !pushTopics[topic] || strings.TrimSpace(title) == "" || strings.TrimSpace(body) == "" {
		return 0, errors.New("推送消息参数无效")
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	var id int64
	err = s.pool.QueryRow(ctx, `
		INSERT INTO push_messages(topic,title,body,payload,dedup_key)
		VALUES($1,$2,$3,$4,NULLIF($5,''))
		ON CONFLICT(dedup_key) DO UPDATE SET dedup_key=EXCLUDED.dedup_key
		RETURNING id`, topic, strings.TrimSpace(title), strings.TrimSpace(body), raw, strings.TrimSpace(dedupKey)).Scan(&id)
	if err != nil {
		return 0, err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO push_deliveries(message_id,device_id)
		SELECT $1,d.id FROM push_devices d
		JOIN push_subscriptions s ON s.device_id=d.id AND s.topic=$2 AND s.enabled
		WHERE d.enabled
		ON CONFLICT DO NOTHING`, id, topic)
	return id, err
}

func (s *PushNotificationStore) PendingDeliveries(ctx context.Context, limit int) ([]PendingPushDelivery, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT d.message_id,d.device_id,p.provider,p.push_token,m.title,m.body,m.payload
		FROM push_deliveries d
		JOIN push_devices p ON p.id=d.device_id
		JOIN push_messages m ON m.id=d.message_id
		WHERE d.status='pending' AND p.enabled AND p.provider IN ('fcm','apns') AND m.status IN ('queued','sending')
		ORDER BY m.created_at,d.device_id LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PendingPushDelivery
	for rows.Next() {
		var item PendingPushDelivery
		var raw []byte
		if err := rows.Scan(&item.MessageID, &item.DeviceID, &item.Provider, &item.Token, &item.Title, &item.Body, &raw); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &item.Payload)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PushNotificationStore) MarkDelivery(ctx context.Context, messageID, deviceID int64, status, providerRef, errorCode string) error {
	if status != "sent" && status != "failed" && status != "invalid_token" {
		return errors.New("推送投递状态无效")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE push_deliveries SET status=$3,provider_ref=$4,error_code=$5,attempted_at=NOW() WHERE message_id=$1 AND device_id=$2`, messageID, deviceID, status, providerRef, errorCode); err != nil {
		return err
	}
	if status == "invalid_token" {
		if _, err := tx.Exec(ctx, `UPDATE push_devices SET enabled=FALSE,updated_at=NOW() WHERE id=$1`, deviceID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE push_messages m SET status=CASE
		 WHEN EXISTS(SELECT 1 FROM push_deliveries WHERE message_id=m.id AND status='pending') THEN 'sending'
		 WHEN EXISTS(SELECT 1 FROM push_deliveries WHERE message_id=m.id AND status='sent') THEN 'sent'
		 ELSE 'failed' END,
		 sent_at=CASE WHEN NOT EXISTS(SELECT 1 FROM push_deliveries WHERE message_id=m.id AND status='pending') THEN NOW() ELSE sent_at END
		WHERE m.id=$1`, messageID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
