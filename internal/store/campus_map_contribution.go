package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"zonenan-backend/internal/db"
)

type CampusMapContribution struct {
	ID            int64           `json:"id"`
	UserID        int64           `json:"user_id"`
	UserNickname  string          `json:"user_nickname,omitempty"`
	Kind          string          `json:"kind"`
	PlaceID       *int64          `json:"place_id,omitempty"`
	ProposedPlace *CampusMapPlace `json:"proposed_place,omitempty"`
	Message       string          `json:"message"`
	Status        string          `json:"status"`
	ReviewNote    string          `json:"review_note"`
	ReviewedAt    *time.Time      `json:"reviewed_at,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
	Corroboration int             `json:"corroboration_count,omitempty"`
	PendingCount  int             `json:"user_pending_count,omitempty"`
	ApprovedCount int             `json:"user_approved_count,omitempty"`
	RejectedCount int             `json:"user_rejected_count,omitempty"`
}

type CampusMapContributionStore struct{ pool *db.Pool }

func NewCampusMapContributionStore(pool *db.Pool) *CampusMapContributionStore {
	return &CampusMapContributionStore{pool: pool}
}

func normalizeContribution(item CampusMapContribution) (CampusMapContribution, error) {
	item.Kind = strings.TrimSpace(item.Kind)
	item.Message = strings.TrimSpace(item.Message)
	if utf8.RuneCountInString(item.Message) > 2000 {
		return item, errors.New("说明不能超过 2000 个字符")
	}
	switch item.Kind {
	case "new_place":
		if item.ProposedPlace == nil {
			return item, errors.New("新增地点缺少地点信息")
		}
		place := NormalizeCampusMapPlace(*item.ProposedPlace)
		place.ID = 0
		place.Active = true
		if err := ValidateCampusMapPlace(place); err != nil {
			return item, err
		}
		item.ProposedPlace = &place
		item.PlaceID = nil
	case "correction":
		if item.PlaceID == nil || *item.PlaceID <= 0 {
			return item, errors.New("纠错缺少有效地点 ID")
		}
		if item.Message == "" {
			return item, errors.New("请说明地点信息哪里有误")
		}
		item.ProposedPlace = nil
	default:
		return item, errors.New("上报类型无效")
	}
	return item, nil
}

func contributionFingerprint(item CampusMapContribution) string {
	normalizeText := func(value string) string {
		return strings.ToLower(strings.Join(strings.Fields(value), " "))
	}
	parts := []string{item.Kind}
	if item.Kind == "correction" && item.PlaceID != nil {
		parts = append(parts, fmt.Sprintf("place:%d", *item.PlaceID), normalizeText(item.Message))
	} else if item.ProposedPlace != nil {
		place := item.ProposedPlace
		types := append([]string(nil), place.Types...)
		sort.Strings(types)
		parts = append(parts,
			place.CampusID,
			normalizeText(place.Name),
			fmt.Sprintf("%.6f", place.Latitude),
			fmt.Sprintf("%.6f", place.Longitude),
			place.Category,
			strings.Join(types, ","),
		)
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x1f")))
	return fmt.Sprintf("%x", sum[:])
}

func (s *CampusMapContributionStore) Submit(ctx context.Context, userID int64, item CampusMapContribution) (CampusMapContribution, error) {
	if userID <= 0 {
		return CampusMapContribution{}, errors.New("用户 ID 无效")
	}
	item.UserID = userID
	var err error
	item, err = normalizeContribution(item)
	if err != nil {
		return CampusMapContribution{}, err
	}
	fingerprint := contributionFingerprint(item)
	var duplicate bool
	if err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM campus_map_contributions
			 WHERE user_id=$1 AND fingerprint=$2 AND status='pending'
		)`, userID, fingerprint).Scan(&duplicate); err != nil {
		return CampusMapContribution{}, err
	}
	if duplicate {
		return CampusMapContribution{}, errors.New("相同内容已在审核中，请勿重复提交")
	}
	var proposed []byte
	var proposedJSON any
	if item.ProposedPlace != nil {
		proposed, err = json.Marshal(item.ProposedPlace)
		if err != nil {
			return CampusMapContribution{}, err
		}
		proposedJSON = string(proposed)
	}
	err = s.pool.QueryRow(ctx, `
		INSERT INTO campus_map_contributions(user_id, kind, place_id, proposed_place, message, fingerprint)
		VALUES($1,$2,$3,$4,$5,$6)
		RETURNING id, status, review_note, reviewed_at, created_at, updated_at`,
		userID, item.Kind, item.PlaceID, proposedJSON, item.Message, fingerprint,
	).Scan(&item.ID, &item.Status, &item.ReviewNote, &item.ReviewedAt, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

func (s *CampusMapContributionStore) List(ctx context.Context, status string) ([]CampusMapContribution, error) {
	status = strings.TrimSpace(status)
	rows, err := s.pool.Query(ctx, `
		SELECT c.id, c.user_id, u.nickname, c.kind, c.place_id, c.proposed_place,
		       c.message, c.status, c.review_note, c.reviewed_at, c.created_at, c.updated_at,
		       (SELECT COUNT(*) FROM campus_map_contributions x WHERE x.fingerprint=c.fingerprint AND x.fingerprint<>''),
		       (SELECT COUNT(*) FROM campus_map_contributions x WHERE x.user_id=c.user_id AND x.status='pending'),
		       (SELECT COUNT(*) FROM campus_map_contributions x WHERE x.user_id=c.user_id AND x.status='approved'),
		       (SELECT COUNT(*) FROM campus_map_contributions x WHERE x.user_id=c.user_id AND x.status='rejected')
		  FROM campus_map_contributions c
		  JOIN zonenan_users u ON u.id=c.user_id
		 WHERE ($1='' OR c.status=$1)
		 ORDER BY CASE c.status WHEN 'pending' THEN 0 ELSE 1 END,
		       (SELECT COUNT(*) FROM campus_map_contributions x
		         WHERE x.fingerprint=c.fingerprint AND x.fingerprint<>'') DESC,
		       CASE WHEN (SELECT COUNT(*) FROM campus_map_contributions x
		                        WHERE x.user_id=c.user_id AND x.status IN ('approved','rejected'))=0
		            THEN 0.5
		            ELSE (SELECT COUNT(*) FROM campus_map_contributions x
		                   WHERE x.user_id=c.user_id AND x.status='approved')::float /
		                 (SELECT COUNT(*) FROM campus_map_contributions x
		                   WHERE x.user_id=c.user_id AND x.status IN ('approved','rejected'))
		       END DESC,
		       c.created_at DESC`, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []CampusMapContribution{}
	for rows.Next() {
		var item CampusMapContribution
		var proposed []byte
		if err := rows.Scan(&item.ID, &item.UserID, &item.UserNickname, &item.Kind, &item.PlaceID,
			&proposed, &item.Message, &item.Status, &item.ReviewNote, &item.ReviewedAt,
			&item.CreatedAt, &item.UpdatedAt, &item.Corroboration, &item.PendingCount,
			&item.ApprovedCount, &item.RejectedCount); err != nil {
			return nil, err
		}
		if len(proposed) > 0 {
			var place CampusMapPlace
			if err := json.Unmarshal(proposed, &place); err != nil {
				return nil, err
			}
			item.ProposedPlace = &place
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// Review atomically publishes an approved new place and closes the review.
// Corrections are marked handled after the administrator has applied any
// necessary edit using the normal official-place editor.
func (s *CampusMapContributionStore) Review(ctx context.Context, id int64, decision, note string, editedPlace *CampusMapPlace) error {
	decision = strings.TrimSpace(decision)
	note = strings.TrimSpace(note)
	if id <= 0 || (decision != "approved" && decision != "rejected") {
		return errors.New("审核参数无效")
	}
	if utf8.RuneCountInString(note) > 1000 {
		return errors.New("审核备注不能超过 1000 个字符")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var kind string
	var proposed []byte
	err = tx.QueryRow(ctx, `
		SELECT kind, proposed_place FROM campus_map_contributions
		 WHERE id=$1 AND status='pending' FOR UPDATE`, id).Scan(&kind, &proposed)
	if err != nil {
		return err
	}
	if editedPlace != nil && (decision != "approved" || kind != "new_place") {
		return errors.New("只有通过新增地点上报时可以提交编辑后的地点")
	}
	if decision == "approved" && kind == "new_place" {
		var place CampusMapPlace
		if editedPlace != nil {
			place = *editedPlace
		} else {
			if err := json.Unmarshal(proposed, &place); err != nil {
				return err
			}
		}
		place = NormalizeCampusMapPlace(place)
		place.ID = 0
		place.Active = true
		if err := ValidateCampusMapPlace(place); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO campus_map_places(campus_id,name,latitude,longitude,coordinate_system,
			 place_type,category,place_types,address,description,aliases,sort,active)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,TRUE)`,
			place.CampusID, place.Name, place.Latitude, place.Longitude, place.CoordinateSystem,
			place.Type, place.Category, place.Types, place.Address, place.Description, place.Aliases, place.Sort)
		if err != nil {
			return err
		}
		if err := bumpCampusMapRevision(ctx, tx); err != nil {
			return err
		}
	}
	command, err := tx.Exec(ctx, `
		UPDATE campus_map_contributions
		   SET status=$2, review_note=$3, reviewed_at=NOW(), updated_at=NOW()
		 WHERE id=$1 AND status='pending'`, id, decision, note)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return errors.New("上报不存在或已审核")
	}
	return tx.Commit(ctx)
}
