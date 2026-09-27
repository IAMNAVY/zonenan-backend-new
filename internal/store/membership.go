package store

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"zonenan-backend/internal/afdian"
	"zonenan-backend/internal/db"
	"zonenan-backend/internal/membership"
)

var (
	ErrActivationInvalid = errors.New("激活码无效")
	ErrActivationUsed    = errors.New("激活码不可用")
	ErrMembershipType    = errors.New("会员类型不存在")
	ErrGrantNotFound     = errors.New("会员授予记录不存在")
)

var bindingCodePattern = regexp.MustCompile(`(?i)ZN1[-\s]*[0-9]{2,18}[-\s]*[0-9]`)
var featureKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
var amountPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,9})(\.[0-9]{1,2})?$`)

var longTermExpiry = time.Date(2099, 12, 31, 23, 59, 59, 0, time.FixedZone("CST", 8*60*60))

func LongTermExpiry() time.Time { return longTermExpiry }

type MembershipStore struct {
	pool             *db.Pool
	bindingOffset    int64
	activationPepper []byte
}

func NewMembershipStore(pool *db.Pool, bindingOffset int64, activationPepper string) *MembershipStore {
	return &MembershipStore{
		pool:             pool,
		bindingOffset:    bindingOffset,
		activationPepper: []byte(activationPepper),
	}
}

type MembershipType struct {
	Key         string          `json:"key"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Features    json.RawMessage `json:"features"`
	AfdianPlan  string          `json:"afdian_plan_id,omitempty"`
	Enabled     bool            `json:"enabled"`
}

type MembershipView struct {
	Key         string          `json:"key"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Features    json.RawMessage `json:"features"`
	Active      bool            `json:"active"`
	StartsAt    *time.Time      `json:"starts_at,omitempty"`
	ExpiresAt   *time.Time      `json:"expires_at,omitempty"`
	Source      string          `json:"source,omitempty"`
}

type MembershipStatus struct {
	BindingCode       string           `json:"binding_code"`
	Memberships       []MembershipView `json:"memberships"`
	ActivationEnabled bool             `json:"activation_enabled"`
	PurchaseChannels  []map[string]any `json:"purchase_channels"`
}

type ProcessResult struct {
	OrderID   string
	Status    string
	UserID    int64
	TypeKey   string
	Message   string
	Duplicate bool
}

func (s *MembershipStore) BindingCode(ctx context.Context, userID int64) (string, error) {
	var code string
	err := s.pool.QueryRow(ctx, `SELECT binding_code FROM membership_binding_codes WHERE user_id=$1`, userID).Scan(&code)
	if err == nil {
		return code, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	code, err = membership.GenerateBindingCode(userID, s.bindingOffset)
	if err != nil {
		return "", err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO membership_binding_codes(user_id, binding_code, algorithm_version)
		VALUES($1,$2,$3) ON CONFLICT(user_id) DO NOTHING`, userID, code, membership.BindingAlgorithmV1)
	if err != nil {
		return "", err
	}
	if err := s.pool.QueryRow(ctx, `SELECT binding_code FROM membership_binding_codes WHERE user_id=$1`, userID).Scan(&code); err != nil {
		return "", err
	}
	return code, nil
}

func (s *MembershipStore) Status(ctx context.Context, userID int64) (*MembershipStatus, error) {
	binding, err := s.BindingCode(ctx, userID)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT mt.type_key, mt.title, mt.description, mt.features,
		       EXISTS(
		         SELECT 1 FROM membership_grants g
		          WHERE g.user_id=$1 AND g.membership_type_key=mt.type_key
		            AND g.revoked_at IS NULL AND g.starts_at <= NOW() AND g.expires_at > NOW()
		       ) AS active,
		       (SELECT MIN(g.starts_at) FROM membership_grants g
		          WHERE g.user_id=$1 AND g.membership_type_key=mt.type_key
		            AND g.revoked_at IS NULL AND g.starts_at <= NOW() AND g.expires_at > NOW()) AS starts_at,
		       (SELECT MAX(g.expires_at) FROM membership_grants g
		          WHERE g.user_id=$1 AND g.membership_type_key=mt.type_key
		            AND g.revoked_at IS NULL AND g.starts_at <= NOW() AND g.expires_at > NOW()) AS expires_at,
		       (SELECT g.source FROM membership_grants g
		          WHERE g.user_id=$1 AND g.membership_type_key=mt.type_key
		            AND g.revoked_at IS NULL AND g.starts_at <= NOW() AND g.expires_at > NOW()
		          ORDER BY g.expires_at DESC LIMIT 1) AS source
		  FROM membership_types mt WHERE mt.enabled=true ORDER BY mt.type_key`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	memberships := make([]MembershipView, 0)
	for rows.Next() {
		var item MembershipView
		var features []byte
		var source *string
		if err := rows.Scan(&item.Key, &item.Title, &item.Description, &features,
			&item.Active, &item.StartsAt, &item.ExpiresAt, &source); err != nil {
			return nil, err
		}
		item.Features = json.RawMessage(features)
		if source != nil {
			item.Source = *source
		}
		memberships = append(memberships, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return &MembershipStatus{
		BindingCode:       binding,
		Memberships:       memberships,
		ActivationEnabled: true,
		PurchaseChannels: []map[string]any{
			{"key": "afdian", "title": "爱发电购买", "url": "", "enabled": false, "disabled_reason": "购买渠道暂未开放"},
		},
	}, nil
}

func (s *MembershipStore) IsActive(ctx context.Context, userID int64, typeKey string) (bool, error) {
	if userID <= 0 || strings.TrimSpace(typeKey) == "" {
		return false, nil
	}
	var active bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM membership_grants g
			 WHERE g.user_id=$1 AND g.membership_type_key=$2
			   AND g.revoked_at IS NULL AND g.starts_at <= NOW() AND g.expires_at > NOW()
		)`, userID, strings.TrimSpace(typeKey)).Scan(&active)
	return active, err
}

// HasFeature is the server-side entitlement check. The JSON feature list is
// configuration, not a client-provided permission claim.
func (s *MembershipStore) HasFeature(ctx context.Context, userID int64, featureKey string) (bool, error) {
	if userID <= 0 || strings.TrimSpace(featureKey) == "" {
		return false, nil
	}
	var allowed bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			  FROM membership_grants g
			  JOIN membership_types mt ON mt.type_key=g.membership_type_key AND mt.enabled=true
			 CROSS JOIN LATERAL jsonb_array_elements(
			   CASE WHEN jsonb_typeof(mt.features)='array' THEN mt.features ELSE '[]'::jsonb END
			 ) f
			 WHERE g.user_id=$1
			   AND g.revoked_at IS NULL AND g.starts_at <= NOW() AND g.expires_at > NOW()
			   AND f->>'key'=$2 AND COALESCE(f->>'enabled','true') <> 'false'
		)`, userID, strings.TrimSpace(featureKey)).Scan(&allowed)
	return allowed, err
}

func (s *MembershipStore) ListTypes(ctx context.Context) ([]MembershipType, error) {
	rows, err := s.pool.Query(ctx, `SELECT type_key,title,description,features,COALESCE(afdian_plan_id,''),enabled FROM membership_types ORDER BY type_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []MembershipType{}
	for rows.Next() {
		var item MembershipType
		var features []byte
		if err := rows.Scan(&item.Key, &item.Title, &item.Description, &features, &item.AfdianPlan, &item.Enabled); err != nil {
			return nil, err
		}
		item.Features = json.RawMessage(features)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *MembershipStore) UpsertType(ctx context.Context, item MembershipType) error {
	item.Key = strings.TrimSpace(item.Key)
	item.Title = strings.TrimSpace(item.Title)
	item.AfdianPlan = strings.TrimSpace(item.AfdianPlan)
	if item.Key == "" || item.Title == "" {
		return ErrMembershipType
	}
	if len(item.Key) > 64 || len(item.Title) > 100 || len(item.Description) > 1000 || len(item.AfdianPlan) > 128 {
		return ErrMembershipType
	}
	if len(item.Features) == 0 {
		item.Features = json.RawMessage(`[]`)
	}
	if !json.Valid(item.Features) || !validFeatures(item.Features) {
		return ErrMembershipType
	}
	var plan any
	if item.AfdianPlan != "" {
		plan = item.AfdianPlan
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO membership_types(type_key,title,description,features,afdian_plan_id,enabled,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,NOW())
		ON CONFLICT(type_key) DO UPDATE SET title=EXCLUDED.title, description=EXCLUDED.description,
		features=EXCLUDED.features, afdian_plan_id=EXCLUDED.afdian_plan_id, enabled=EXCLUDED.enabled, updated_at=NOW()`,
		item.Key, item.Title, item.Description, item.Features, plan, item.Enabled)
	return err
}

func (s *MembershipStore) ProcessOrder(ctx context.Context, order afdian.Order, source string) (ProcessResult, error) {
	order.OutTradeNo = strings.TrimSpace(order.OutTradeNo)
	order.UserID = strings.TrimSpace(order.UserID)
	order.UserPrivateID = strings.TrimSpace(order.UserPrivateID)
	order.PlanID = strings.TrimSpace(order.PlanID)
	if order.OutTradeNo == "" || len(order.OutTradeNo) > 128 || order.UserID == "" || len(order.UserID) > 128 {
		return ProcessResult{}, errors.New("订单字段无效")
	}
	if order.UserPrivateID != "" && len(order.UserPrivateID) > 128 || order.PlanID != "" && len(order.PlanID) > 128 {
		return ProcessResult{}, errors.New("订单字段无效")
	}
	if order.Month < 0 || order.Month > 1200 || (order.ProductType != 0 && order.ProductType != 1) ||
		order.TotalAmount == "" || order.ShowAmount == "" ||
		!validAmount(order.TotalAmount) || !validAmount(order.ShowAmount) || !validAmount(order.Discount) {
		return ProcessResult{}, errors.New("订单金额或类型无效")
	}
	if len(order.Remark) > 2000 {
		order.Remark = order.Remark[:2000]
	}
	payload, err := json.Marshal(order)
	if err != nil {
		return ProcessResult{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProcessResult{}, err
	}
	defer tx.Rollback(ctx)

	var existingStatus string
	err = tx.QueryRow(ctx, `SELECT process_status FROM membership_events WHERE provider='afdian' AND event_type='order' AND external_order_id=$1 FOR UPDATE`, order.OutTradeNo).Scan(&existingStatus)
	if err == nil && existingStatus == "processed" {
		if err := tx.Commit(ctx); err != nil {
			return ProcessResult{}, err
		}
		return ProcessResult{OrderID: order.OutTradeNo, Status: "processed", Duplicate: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) && err != nil {
		return ProcessResult{}, err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		_, err = tx.Exec(ctx, `INSERT INTO membership_events(provider,event_type,external_order_id,afdian_user_id,afdian_private_id,plan_id,status,month,total_amount,show_amount,product_type,remark,payload,process_status) VALUES('afdian','order',$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'failed')`, order.OutTradeNo, order.UserID, order.UserPrivateID, order.PlanID, order.Status, order.Month, order.TotalAmount, order.ShowAmount, order.ProductType, order.Remark, payload)
	} else {
		_, err = tx.Exec(ctx, `UPDATE membership_events SET afdian_user_id=$2, afdian_private_id=$3, plan_id=$4, status=$5, month=$6, total_amount=$7, show_amount=$8, product_type=$9, remark=$10, payload=$11, process_status='failed', error_message='', processed_at=NULL WHERE provider='afdian' AND event_type='order' AND external_order_id=$1`, order.OutTradeNo, order.UserID, order.UserPrivateID, order.PlanID, order.Status, order.Month, order.TotalAmount, order.ShowAmount, order.ProductType, order.Remark, payload)
	}
	if err != nil {
		return ProcessResult{}, err
	}

	result := ProcessResult{OrderID: order.OutTradeNo, Status: "ignored", Message: "订单未满足授予条件"}
	if order.Status != 2 {
		result.Message = "订单状态不是成功"
	} else {
		var typeKey string
		err = tx.QueryRow(ctx, `SELECT type_key FROM membership_types WHERE enabled=true AND afdian_plan_id=$1`, order.PlanID).Scan(&typeKey)
		if errors.Is(err, pgx.ErrNoRows) {
			result.Status = "ignored"
			result.Message = "未配置会员方案"
		} else if err != nil {
			return ProcessResult{}, err
		} else {
			code, codeErr := extractBindingCode(order.Remark)
			if codeErr != nil {
				result.Status = "unmatched"
				result.Message = "订单备注未找到有效绑定码"
			} else {
				var userID int64
				err = tx.QueryRow(ctx, `SELECT user_id FROM membership_binding_codes WHERE binding_code=$1`, code).Scan(&userID)
				if errors.Is(err, pgx.ErrNoRows) {
					result.Status = "unmatched"
					result.Message = "绑定码不存在"
				} else if err != nil {
					return ProcessResult{}, err
				} else {
					_, err = tx.Exec(ctx, `INSERT INTO membership_grants(user_id,membership_type_key,source,external_ref,expires_at,metadata) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, userID, typeKey, source, order.OutTradeNo, longTermExpiry, json.RawMessage(`{"afdian_user_id":"`+escapeJSON(order.UserID)+`","plan_id":"`+escapeJSON(order.PlanID)+`"}`))
					if err != nil {
						return ProcessResult{}, err
					}
					result.Status = "processed"
					result.UserID = userID
					result.TypeKey = typeKey
					result.Message = "会员已授予"
				}
			}
		}
	}
	_, err = tx.Exec(ctx, `UPDATE membership_events SET process_status=$2, matched_user_id=NULLIF($3,0), error_message=$4, processed_at=NOW() WHERE provider='afdian' AND event_type='order' AND external_order_id=$1`, order.OutTradeNo, result.Status, result.UserID, result.Message)
	if err != nil {
		return ProcessResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ProcessResult{}, err
	}
	return result, nil
}

func validAmount(value string) bool {
	return value == "" || amountPattern.MatchString(value)
}

func validFeatures(raw json.RawMessage) bool {
	value := strings.TrimSpace(string(raw))
	if value == "" || value[0] != '[' {
		return false
	}
	var features []struct {
		Key         string `json:"key"`
		Title       string `json:"title"`
		Description string `json:"description"`
		Enabled     *bool  `json:"enabled"`
	}
	if err := json.Unmarshal(raw, &features); err != nil {
		return false
	}
	for _, feature := range features {
		if !featureKeyPattern.MatchString(strings.TrimSpace(feature.Key)) ||
			len([]rune(feature.Title)) > 200 || len([]rune(feature.Description)) > 1000 {
			return false
		}
	}
	return true
}

func extractBindingCode(remark string) (string, error) {
	matches := bindingCodePattern.FindAllString(remark, -1)
	if len(matches) != 1 {
		return "", membership.ErrInvalidBindingCode
	}
	return membership.NormalizeBindingCode(matches[0])
}

func escapeJSON(value string) string {
	b, _ := json.Marshal(value)
	return strings.Trim(string(b), `"`)
}

func (s *MembershipStore) GenerateActivationCode(ctx context.Context, typeKey, orderID, actor string, expiresAt *time.Time) (string, error) {
	if _, err := s.typeExists(ctx, typeKey); err != nil {
		return "", err
	}
	for attempt := 0; attempt < 5; attempt++ {
		buf := make([]byte, 18)
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf)
		code := "ZN-ACT-" + encoded[:6] + "-" + encoded[6:12] + "-" + encoded[12:]
		canonical := normalizeActivationCode(code)
		hash := s.activationHash(canonical)
		_, err := s.pool.Exec(ctx, `INSERT INTO membership_activation_codes(code_hash,membership_type_key,source_order_id,expires_at,created_by) VALUES($1,$2,$3,$4,$5)`, hash, typeKey, strings.TrimSpace(orderID), expiresAt, strings.TrimSpace(actor))
		if err == nil {
			_, _ = s.pool.Exec(ctx, `INSERT INTO membership_audit_logs(action,source,external_ref,actor,metadata) VALUES('generate_code','admin',$1,$2,$3)`, strings.TrimSpace(orderID), strings.TrimSpace(actor), json.RawMessage(`{"membership_type":"`+escapeJSON(typeKey)+`"}`))
			return code, nil
		}
		if !strings.Contains(err.Error(), "membership_activation_codes_code_hash_key") {
			return "", err
		}
	}
	return "", errors.New("生成激活码失败")
}

type ActivationCodeView struct {
	ID         int64      `json:"id"`
	Membership string     `json:"membership_type"`
	OrderID    string     `json:"order_id"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	UsedAt     *time.Time `json:"used_at,omitempty"`
	UsedBy     *int64     `json:"used_by_user_id,omitempty"`
	DisabledAt *time.Time `json:"disabled_at,omitempty"`
	CreatedBy  string     `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
	State      string     `json:"state"`
}

func (s *MembershipStore) ListActivationCodes(ctx context.Context, state string, limit int) ([]ActivationCodeView, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	where := ""
	switch state {
	case "unused":
		where = "WHERE used_at IS NULL AND disabled_at IS NULL AND (expires_at IS NULL OR expires_at > NOW())"
	case "used":
		where = "WHERE used_at IS NOT NULL"
	case "disabled":
		where = "WHERE disabled_at IS NOT NULL"
	case "expired":
		where = "WHERE used_at IS NULL AND disabled_at IS NULL AND expires_at IS NOT NULL AND expires_at <= NOW()"
	case "":
	default:
		return nil, errors.New("激活码状态无效")
	}
	rows, err := s.pool.Query(ctx, `SELECT id,membership_type_key,source_order_id,expires_at,used_at,used_by_user_id,disabled_at,created_by,created_at FROM membership_activation_codes `+where+` ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []ActivationCodeView{}
	for rows.Next() {
		var item ActivationCodeView
		if err := rows.Scan(&item.ID, &item.Membership, &item.OrderID, &item.ExpiresAt, &item.UsedAt, &item.UsedBy, &item.DisabledAt, &item.CreatedBy, &item.CreatedAt); err != nil {
			return nil, err
		}
		item.State = activationState(item.UsedAt, item.DisabledAt, item.ExpiresAt)
		result = append(result, item)
	}
	return result, rows.Err()
}

func activationState(usedAt, disabledAt, expiresAt *time.Time) string {
	if disabledAt != nil {
		return "disabled"
	}
	if usedAt != nil {
		return "used"
	}
	if expiresAt != nil && !expiresAt.After(time.Now()) {
		return "expired"
	}
	return "unused"
}

func (s *MembershipStore) DisableActivationCode(ctx context.Context, id int64, actor, reason string) error {
	if id <= 0 || strings.TrimSpace(reason) == "" || len([]rune(reason)) > 1000 {
		return errors.New("禁用参数无效")
	}
	var orderID string
	err := s.pool.QueryRow(ctx, `UPDATE membership_activation_codes SET disabled_at=NOW() WHERE id=$1 AND used_at IS NULL AND disabled_at IS NULL RETURNING source_order_id`, id).Scan(&orderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrActivationUsed
	}
	if err != nil {
		return err
	}
	_, _ = s.pool.Exec(ctx, `INSERT INTO membership_audit_logs(action,source,external_ref,actor,reason) VALUES('disable_code','admin',$1,$2,$3)`, orderID, strings.TrimSpace(actor), reason)
	return nil
}
func (s *MembershipStore) RedeemActivation(ctx context.Context, userID int64, rawCode string) error {
	code := normalizeActivationCode(rawCode)
	if code == "" {
		return ErrActivationInvalid
	}
	hash := s.activationHash(code)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var id int64
	var typeKey string
	var expiresAt *time.Time
	var usedAt, disabledAt *time.Time
	err = tx.QueryRow(ctx, `SELECT id,membership_type_key,expires_at,used_at,disabled_at FROM membership_activation_codes WHERE code_hash=$1 FOR UPDATE`, hash).Scan(&id, &typeKey, &expiresAt, &usedAt, &disabledAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrActivationInvalid
	}
	if err != nil {
		return err
	}
	if usedAt != nil || disabledAt != nil || (expiresAt != nil && !expiresAt.After(time.Now())) {
		return ErrActivationUsed
	}
	if _, err := tx.Exec(ctx, `INSERT INTO membership_grants(user_id,membership_type_key,source,external_ref,expires_at,metadata) VALUES($1,$2,'activation',$3,$4,$5) ON CONFLICT DO NOTHING`, userID, typeKey, fmt.Sprintf("activation:%d", id), longTermExpiry, json.RawMessage(`{"activation":true}`)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE membership_activation_codes SET used_at=NOW(), used_by_user_id=$2 WHERE id=$1`, id, userID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return nil
}

func (s *MembershipStore) activationHash(code string) string {
	h := hmac.New(sha256.New, s.activationPepper)
	h.Write([]byte(code))
	return hex.EncodeToString(h.Sum(nil))
}

func normalizeActivationCode(raw string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(raw)) {
		if r == '-' || r == ' ' || r == '\t' || r == '\n' {
			continue
		}
		if (r >= 'A' && r <= 'Z') || (r >= '2' && r <= '7') {
			b.WriteRune(r)
			continue
		}
		return ""
	}
	value := b.String()
	if len(value) < 20 || !strings.HasPrefix(value, "ZNACT") {
		return ""
	}
	return value
}

func (s *MembershipStore) typeExists(ctx context.Context, key string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM membership_types WHERE type_key=$1 AND enabled=true)`, strings.TrimSpace(key)).Scan(&exists)
	if err != nil {
		return false, err
	}
	if !exists {
		return false, ErrMembershipType
	}
	return true, nil
}

func (s *MembershipStore) GrantManual(ctx context.Context, userID int64, typeKey, actor, reason string, expiresAt time.Time) error {
	if userID <= 0 || strings.TrimSpace(reason) == "" || len([]rune(reason)) > 1000 {
		return errors.New("手动授权参数无效")
	}
	if _, err := s.typeExists(ctx, typeKey); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO membership_grants(user_id,membership_type_key,source,external_ref,expires_at,metadata) VALUES($1,$2,'admin',$3,$4,$5)`, userID, typeKey, fmt.Sprintf("admin:%d:%d", userID, time.Now().UnixNano()), expiresAt, json.RawMessage(`{"reason":"`+escapeJSON(reason)+`"}`))
	if err != nil {
		return err
	}
	_, _ = s.pool.Exec(ctx, `INSERT INTO membership_audit_logs(user_id,action,source,actor,reason) VALUES($1,'grant','admin',$2,$3)`, userID, actor, reason)
	return nil
}

func (s *MembershipStore) Revoke(ctx context.Context, userID int64, typeKey, actor, reason string) error {
	if userID <= 0 || strings.TrimSpace(typeKey) == "" || strings.TrimSpace(reason) == "" || len([]rune(reason)) > 1000 {
		return errors.New("撤销参数无效")
	}
	result, err := s.pool.Exec(ctx, `UPDATE membership_grants SET revoked_at=NOW(), updated_at=NOW() WHERE user_id=$1 AND membership_type_key=$2 AND revoked_at IS NULL`, userID, typeKey)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrGrantNotFound
	}
	_, _ = s.pool.Exec(ctx, `INSERT INTO membership_audit_logs(user_id,action,source,actor,reason,metadata) VALUES($1,'revoke','admin',$2,$3,$4)`, userID, actor, reason, json.RawMessage(`{"type_key":"`+escapeJSON(typeKey)+`"}`))
	return nil
}

// RevokeGrant revokes exactly one grant. It is used by the admin panel so that
// an Afdian order or activation grant is never accidentally removed together
// with another grant of the same membership type.
func (s *MembershipStore) RevokeGrant(ctx context.Context, grantID int64, actor, reason string) error {
	if grantID <= 0 || strings.TrimSpace(reason) == "" || len([]rune(reason)) > 1000 {
		return errors.New("撤销参数无效")
	}
	var userID int64
	var typeKey string
	err := s.pool.QueryRow(ctx, `UPDATE membership_grants SET revoked_at=NOW(), updated_at=NOW() WHERE id=$1 AND revoked_at IS NULL RETURNING user_id,membership_type_key`, grantID).Scan(&userID, &typeKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrGrantNotFound
	}
	if err != nil {
		return err
	}
	_, _ = s.pool.Exec(ctx, `INSERT INTO membership_audit_logs(user_id,action,source,external_ref,actor,reason,metadata) VALUES($1,'revoke','admin',$2,$3,$4,$5)`, userID, fmt.Sprintf("grant:%d", grantID), actor, reason, json.RawMessage(`{"type_key":"`+escapeJSON(typeKey)+`"}`))
	return nil
}

// UpdateGrantExpiry changes one grant's expiry without changing its source or
// identity. An expiry in the past is allowed as an audited way to end access
// immediately; revoked grants remain immutable.
func (s *MembershipStore) UpdateGrantExpiry(ctx context.Context, grantID int64, expiresAt time.Time, actor, reason string) error {
	if grantID <= 0 || expiresAt.IsZero() || strings.TrimSpace(reason) == "" || len([]rune(reason)) > 1000 {
		return errors.New("有效期修改参数无效")
	}
	var userID int64
	var typeKey string
	var oldExpiry time.Time
	err := s.pool.QueryRow(ctx, `
		WITH target AS (
			SELECT user_id,membership_type_key,expires_at AS old_expires_at
			  FROM membership_grants WHERE id=$1 AND revoked_at IS NULL
		), updated AS (
			UPDATE membership_grants g SET expires_at=$2, updated_at=NOW()
			 FROM target t WHERE g.id=$1
			 RETURNING t.user_id,t.membership_type_key,t.old_expires_at
		)
		SELECT user_id,membership_type_key,old_expires_at FROM updated`, grantID, expiresAt).Scan(&userID, &typeKey, &oldExpiry)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrGrantNotFound
	}
	if err != nil {
		return err
	}
	_, _ = s.pool.Exec(ctx, `INSERT INTO membership_audit_logs(user_id,action,source,external_ref,actor,reason,metadata) VALUES($1,'update_grant','admin',$2,$3,$4,$5)`, userID, fmt.Sprintf("grant:%d", grantID), actor, reason, json.RawMessage(fmt.Sprintf(`{"type_key":%q,"expires_at":%q}`, typeKey, oldExpiry.Format(time.RFC3339))))
	return nil
}

func (s *MembershipStore) ListEvents(ctx context.Context, limit int) ([]map[string]any, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `SELECT id,external_order_id,afdian_user_id,plan_id,status,month,total_amount,show_amount,product_type,remark,process_status,matched_user_id,error_message,received_at,processed_at FROM membership_events ORDER BY received_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []map[string]any{}
	for rows.Next() {
		var id int64
		var orderID, afdianID, planID, totalAmount, showAmount, remark, processStatus, errorMessage string
		var month, productType, status int
		var matched *int64
		var received, processed interface{}
		if err := rows.Scan(&id, &orderID, &afdianID, &planID, &status, &month, &totalAmount, &showAmount, &productType, &remark, &processStatus, &matched, &errorMessage, &received, &processed); err != nil {
			return nil, err
		}
		result = append(result, map[string]any{"id": id, "order_id": orderID, "afdian_user_id": afdianID, "plan_id": planID, "status": status, "month": month, "total_amount": totalAmount, "show_amount": showAmount, "product_type": productType, "remark": remark, "process_status": processStatus, "matched_user_id": matched, "error_message": errorMessage, "received_at": received, "processed_at": processed})
	}
	return result, rows.Err()
}

func (s *MembershipStore) ListMembers(ctx context.Context, limit int) ([]map[string]any, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT g.id,g.user_id,g.membership_type_key,u.nickname,g.source,g.starts_at,g.expires_at,g.revoked_at,
		       g.revoked_at IS NULL AND g.starts_at <= NOW() AND g.expires_at > NOW() AS active
		  FROM membership_grants g JOIN zonenan_users u ON u.id=g.user_id
		 ORDER BY g.created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []map[string]any{}
	for rows.Next() {
		var grantID, userID int64
		var typeKey, nickname, source string
		var starts, expires, revoked interface{}
		var active bool
		if err := rows.Scan(&grantID, &userID, &typeKey, &nickname, &source, &starts, &expires, &revoked, &active); err != nil {
			return nil, err
		}
		result = append(result, map[string]any{"grant_id": grantID, "user_id": userID, "type": typeKey, "nickname": nickname, "source": source, "starts_at": starts, "expires_at": expires, "revoked_at": revoked, "active": active})
	}
	return result, rows.Err()
}

func (s *MembershipStore) Overview(ctx context.Context) (map[string]any, error) {
	var active, grants, events, unmatched int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM membership_grants WHERE revoked_at IS NULL AND starts_at <= NOW() AND expires_at > NOW()`).Scan(&active); err != nil {
		return nil, err
	}
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM membership_grants`).Scan(&grants); err != nil {
		return nil, err
	}
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM membership_events`).Scan(&events); err != nil {
		return nil, err
	}
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM membership_events WHERE process_status='unmatched'`).Scan(&unmatched); err != nil {
		return nil, err
	}
	return map[string]any{"active_memberships": active, "grant_records": grants, "events": events, "unmatched_events": unmatched}, nil
}
