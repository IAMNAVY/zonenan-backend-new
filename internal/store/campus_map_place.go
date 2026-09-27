package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"zonenan-backend/internal/db"
)

var campusMapPlaceTypes = map[string]struct{}{
	"teaching": {}, "library": {}, "dormitory": {}, "dining": {},
	"sports": {}, "landscape": {}, "parking": {}, "gate": {},
	"transport": {}, "service": {},
}

var campusMapCategories = map[string]struct{}{
	"teaching_research": {}, "transportation": {}, "life_service": {},
	"culture_sports": {}, "medical_health": {}, "government_service": {},
	"landscape": {},
}

var campusMapTypesByCategory = map[string]map[string]struct{}{
	"teaching_research":  stringSet("teaching_research", "teaching_building", "college", "library", "research_experiment", "study_space"),
	"transportation":     stringSet("transportation", "metro", "public_bus", "shuttle_bus", "campus_bus", "parking", "entrance"),
	"life_service":       stringSet("life_service", "comprehensive_service", "dining_hall", "dormitory", "restroom", "supermarket", "express", "merchant", "charging_station", "printing_service"),
	"culture_sports":     stringSet("sports_venue", "cultural_venue"),
	"medical_health":     stringSet("medical_health", "campus_hospital", "medical_station", "aed"),
	"government_service": stringSet("government_service"),
	"landscape":          stringSet("landscape"),
}

func stringSet(values ...string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func legacyCampusMapCategory(placeType string) string {
	switch placeType {
	case "teaching", "library":
		return "teaching_research"
	case "dormitory", "dining", "service":
		return "life_service"
	case "sports":
		return "culture_sports"
	case "landscape":
		return "landscape"
	case "parking", "gate", "transport":
		return "transportation"
	default:
		return ""
	}
}

func legacyCampusMapSubtype(placeType string) string {
	switch placeType {
	case "teaching":
		return "teaching_building"
	case "library", "dormitory", "parking", "landscape":
		return placeType
	case "dining":
		return "dining_hall"
	case "sports":
		return "sports_venue"
	case "gate":
		return "transportation"
	case "transport":
		return "transportation"
	case "service":
		return "life_service"
	default:
		return ""
	}
}

func legacyCampusMapType(category string, types []string) string {
	contains := func(want string) bool {
		for _, value := range types {
			if value == want {
				return true
			}
		}
		return false
	}
	switch category {
	case "teaching_research":
		if contains("library") {
			return "library"
		}
		return "teaching"
	case "transportation":
		if contains("parking") {
			return "parking"
		}
		if contains("entrance") {
			return "gate"
		}
		return "transport"
	case "life_service":
		if contains("dining_hall") {
			return "dining"
		}
		if contains("dormitory") {
			return "dormitory"
		}
		return "service"
	case "culture_sports":
		return "sports"
	case "landscape":
		return "landscape"
	default:
		return "service"
	}
}

var campusMapCampusIDs = map[string]struct{}{
	"xiaoxiang": {}, "south": {}, "main": {}, "railway": {},
	"xiangya_new": {}, "xiangya_old": {},
}

type CampusMapPlace struct {
	ID               int64     `json:"id"`
	CampusID         string    `json:"campus_id"`
	Name             string    `json:"name"`
	Latitude         float64   `json:"latitude"`
	Longitude        float64   `json:"longitude"`
	CoordinateSystem string    `json:"coordinate_system"`
	Type             string    `json:"type"`
	Category         string    `json:"category"`
	Types            []string  `json:"types"`
	Address          string    `json:"address,omitempty"`
	Description      string    `json:"description,omitempty"`
	Aliases          []string  `json:"aliases"`
	Sort             int       `json:"sort"`
	Active           bool      `json:"active"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type CampusMapManifest struct {
	SchemaVersion int       `json:"schema_version"`
	DataVersion   string    `json:"data_version"`
	PlaceCount    int       `json:"place_count"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func NormalizeCampusMapPlace(place CampusMapPlace) CampusMapPlace {
	place.CampusID = strings.TrimSpace(place.CampusID)
	place.Name = strings.TrimSpace(place.Name)
	place.CoordinateSystem = strings.ToUpper(strings.TrimSpace(place.CoordinateSystem))
	if place.CoordinateSystem == "" {
		place.CoordinateSystem = "CGCS2000"
	}
	place.Type = strings.ToLower(strings.TrimSpace(place.Type))
	place.Category = strings.ToLower(strings.TrimSpace(place.Category))
	if place.Category == "" {
		place.Category = legacyCampusMapCategory(place.Type)
	}
	placeTypes := make([]string, 0, len(place.Types)+1)
	typeSeen := make(map[string]struct{}, len(place.Types)+1)
	for _, raw := range place.Types {
		value := strings.ToLower(strings.TrimSpace(raw))
		switch value {
		case "road":
			value = "transportation"
		case "shuttle_stop", "shuttle_route":
			value = "campus_bus"
		case "culture_sports":
			value = "sports_venue"
		}
		if value == "" {
			continue
		}
		if _, exists := typeSeen[value]; exists {
			continue
		}
		typeSeen[value] = struct{}{}
		placeTypes = append(placeTypes, value)
	}
	if len(placeTypes) == 0 {
		if value := legacyCampusMapSubtype(place.Type); value != "" {
			placeTypes = append(placeTypes, value)
		}
	}
	place.Types = placeTypes
	place.Type = legacyCampusMapType(place.Category, place.Types)
	place.Address = strings.TrimSpace(place.Address)
	place.Description = strings.TrimSpace(place.Description)
	aliases := make([]string, 0, len(place.Aliases))
	seen := make(map[string]struct{}, len(place.Aliases))
	for _, raw := range place.Aliases {
		alias := strings.TrimSpace(raw)
		if alias == "" {
			continue
		}
		if _, exists := seen[alias]; exists {
			continue
		}
		seen[alias] = struct{}{}
		aliases = append(aliases, alias)
	}
	place.Aliases = aliases
	return place
}

func ValidateCampusMapPlace(place CampusMapPlace) error {
	place = NormalizeCampusMapPlace(place)
	if place.CampusID == "" || len(place.CampusID) > 32 {
		return errors.New("校区 ID 无效")
	}
	for _, r := range place.CampusID {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return errors.New("校区 ID 只能包含小写字母、数字和下划线")
		}
	}
	if _, ok := campusMapCampusIDs[place.CampusID]; !ok {
		return errors.New("校区 ID 不在客户端内置校区列表中")
	}
	if place.Name == "" || utf8.RuneCountInString(place.Name) > 120 {
		return errors.New("地点名称不能为空且不能超过 120 个字符")
	}
	if math.IsNaN(place.Latitude) || math.IsInf(place.Latitude, 0) ||
		math.IsNaN(place.Longitude) || math.IsInf(place.Longitude, 0) ||
		place.Latitude < -90 || place.Latitude > 90 ||
		place.Longitude < -180 || place.Longitude > 180 {
		return errors.New("经纬度超出有效范围")
	}
	if place.CoordinateSystem != "CGCS2000" && place.CoordinateSystem != "WGS84" {
		return errors.New("坐标系仅支持 CGCS2000 或 WGS84")
	}
	if _, ok := campusMapCategories[place.Category]; !ok {
		return errors.New("地点一级分类无效")
	}
	if len(place.Types) > 16 {
		return errors.New("地点二级类型不能超过 16 个")
	}
	allowed := campusMapTypesByCategory[place.Category]
	for _, placeType := range place.Types {
		if _, ok := allowed[placeType]; !ok {
			return errors.New("地点二级类型与一级分类不匹配")
		}
	}
	if _, ok := campusMapPlaceTypes[place.Type]; !ok {
		return errors.New("地点兼容类型无效")
	}
	if utf8.RuneCountInString(place.Address) > 500 || utf8.RuneCountInString(place.Description) > 4000 {
		return errors.New("地址或描述过长")
	}
	if len(place.Aliases) > 20 {
		return errors.New("地点别名不能超过 20 个")
	}
	for _, alias := range place.Aliases {
		if utf8.RuneCountInString(alias) > 80 {
			return errors.New("单个地点别名不能超过 80 个字符")
		}
	}
	return nil
}

type CampusMapPlaceStore struct{ pool *db.Pool }

func NewCampusMapPlaceStore(pool *db.Pool) *CampusMapPlaceStore {
	return &CampusMapPlaceStore{pool: pool}
}

func (s *CampusMapPlaceStore) Manifest(ctx context.Context) (CampusMapManifest, error) {
	var revision int64
	var manifest CampusMapManifest
	err := s.pool.QueryRow(ctx, `
		SELECT revision, active_count, updated_at
		  FROM campus_map_metadata
		 WHERE singleton=TRUE`).Scan(&revision, &manifest.PlaceCount, &manifest.UpdatedAt)
	if err != nil {
		return CampusMapManifest{}, err
	}
	manifest.SchemaVersion = 3
	manifest.DataVersion = campusMapDataVersion(revision)
	return manifest, nil
}

func campusMapDataVersion(revision int64) string {
	sum := sha256.Sum256([]byte("campus-map-v3:" + strconv.FormatInt(revision, 10)))
	return hex.EncodeToString(sum[:])
}

func (s *CampusMapPlaceStore) Active(ctx context.Context, campusID string) ([]CampusMapPlace, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, campus_id, name, latitude, longitude, coordinate_system,
		       place_type, category, place_types, address, description, aliases,
		       sort, active, created_at, updated_at
		  FROM campus_map_places
		 WHERE active = TRUE AND ($1 = '' OR campus_id = $1)
		 ORDER BY campus_id, sort DESC, name, id`, strings.TrimSpace(campusID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCampusMapPlaces(rows)
}

func (s *CampusMapPlaceStore) All(ctx context.Context, campusID string) ([]CampusMapPlace, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, campus_id, name, latitude, longitude, coordinate_system,
		       place_type, category, place_types, address, description, aliases,
		       sort, active, created_at, updated_at
		  FROM campus_map_places
		 WHERE ($1 = '' OR campus_id = $1)
		 ORDER BY campus_id, active DESC, sort DESC, name, id`, strings.TrimSpace(campusID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCampusMapPlaces(rows)
}

func scanCampusMapPlaces(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]CampusMapPlace, error) {
	places := []CampusMapPlace{}
	for rows.Next() {
		var place CampusMapPlace
		if err := rows.Scan(
			&place.ID, &place.CampusID, &place.Name, &place.Latitude,
			&place.Longitude, &place.CoordinateSystem, &place.Type, &place.Category,
			&place.Types,
			&place.Address, &place.Description, &place.Aliases, &place.Sort,
			&place.Active, &place.CreatedAt, &place.UpdatedAt,
		); err != nil {
			return nil, err
		}
		places = append(places, place)
	}
	return places, rows.Err()
}

func (s *CampusMapPlaceStore) Upsert(ctx context.Context, place CampusMapPlace) (CampusMapPlace, error) {
	place = NormalizeCampusMapPlace(place)
	if err := ValidateCampusMapPlace(place); err != nil {
		return CampusMapPlace{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CampusMapPlace{}, err
	}
	defer tx.Rollback(ctx)
	if place.ID == 0 {
		err = tx.QueryRow(ctx, `
			INSERT INTO campus_map_places(
				campus_id, name, latitude, longitude, coordinate_system, place_type,
				category, place_types, address, description, aliases, sort, active)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
			RETURNING id, campus_id, name, latitude, longitude, coordinate_system,
			          place_type, category, place_types, address, description, aliases,
			          sort, active, created_at, updated_at`,
			place.CampusID, place.Name, place.Latitude, place.Longitude,
			place.CoordinateSystem, place.Type, place.Category, place.Types,
			place.Address, place.Description, place.Aliases, place.Sort, place.Active,
		).Scan(
			&place.ID, &place.CampusID, &place.Name, &place.Latitude,
			&place.Longitude, &place.CoordinateSystem, &place.Type, &place.Category,
			&place.Types,
			&place.Address, &place.Description, &place.Aliases, &place.Sort,
			&place.Active, &place.CreatedAt, &place.UpdatedAt,
		)
		if err != nil {
			return CampusMapPlace{}, err
		}
	} else {
		err = tx.QueryRow(ctx, `
			UPDATE campus_map_places
			   SET campus_id=$2, name=$3, latitude=$4, longitude=$5,
			       coordinate_system=$6, place_type=$7, category=$8, place_types=$9,
			       address=$10, description=$11, aliases=$12, sort=$13,
			       active=$14, updated_at=NOW()
			 WHERE id=$1
			RETURNING id, campus_id, name, latitude, longitude, coordinate_system,
			          place_type, category, place_types, address, description, aliases,
			          sort, active, created_at, updated_at`,
			place.ID, place.CampusID, place.Name, place.Latitude, place.Longitude,
			place.CoordinateSystem, place.Type, place.Category, place.Types,
			place.Address, place.Description, place.Aliases, place.Sort, place.Active,
		).Scan(
			&place.ID, &place.CampusID, &place.Name, &place.Latitude,
			&place.Longitude, &place.CoordinateSystem, &place.Type, &place.Category,
			&place.Types,
			&place.Address, &place.Description, &place.Aliases, &place.Sort,
			&place.Active, &place.CreatedAt, &place.UpdatedAt,
		)
		if err != nil {
			return CampusMapPlace{}, err
		}
	}
	if err := bumpCampusMapRevision(ctx, tx); err != nil {
		return CampusMapPlace{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CampusMapPlace{}, err
	}
	return place, nil
}

func (s *CampusMapPlaceStore) Deactivate(ctx context.Context, id int64) error {
	if id <= 0 {
		return errors.New("地点 ID 无效")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	command, err := tx.Exec(ctx, `
		UPDATE campus_map_places SET active=FALSE, updated_at=NOW() WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return errors.New("地点不存在")
	}
	if err := bumpCampusMapRevision(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func bumpCampusMapRevision(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `
		UPDATE campus_map_metadata
		   SET revision=revision+1,
		       active_count=(SELECT COUNT(*) FROM campus_map_places WHERE active=TRUE),
		       updated_at=NOW()
		 WHERE singleton=TRUE`)
	return err
}
