package maptiles

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/sync/singleflight"
)

const (
	minZoom              = 10
	maxZoom              = 18
	maxTileSize          = 2 << 20
	diskBlockSize        = 4 << 10
	defaultCacheMaxBytes = 4 << 30
	uploadTokenTTL       = 5 * time.Minute
	uploadTokenHeader    = "X-ZoneNaN-Map-Upload-Token"
	supportedWest        = 112.820000
	supportedSouth       = 28.065000
	supportedEast        = 113.095000
	supportedNorth       = 28.300000
)

var allowedLayers = map[string]struct{}{
	"vec": {},
	"cva": {},
	"img": {},
	"cia": {},
}

type Config struct {
	Key              string
	UpstreamEnabled  bool
	UploadSigningKey string
	CacheDir         string
	UpstreamTemplate string
	UpstreamParallel int
	CacheMaxBytes    int64
	CacheTrimToBytes int64
	Client           *http.Client
}

// Service serves a persistent, shared, read-through tile cache. Tile bytes are
// never kept in a process-wide memory cache; only duplicate in-flight fetches
// share a result.
type Service struct {
	key              string
	upstreamEnabled  bool
	uploadSigningKey []byte
	cacheDir         string
	upstreamTemplate string
	client           *http.Client
	upstreamSlots    chan struct{}
	requests         singleflight.Group
	cacheMaxBytes    int64
	cacheTrimToBytes int64
	cacheUsage       atomic.Int64
	cacheScan        sync.Once
	cleanupMu        sync.Mutex
}

func New(cfg Config) *Service {
	if cfg.CacheDir == "" {
		cfg.CacheDir = "data/map-tiles"
	}
	if cfg.UpstreamTemplate == "" {
		cfg.UpstreamTemplate = "https://t0.tianditu.gov.cn/{layer}_w/wmts"
	}
	if cfg.UpstreamParallel <= 0 {
		cfg.UpstreamParallel = 4
	}
	if cfg.CacheMaxBytes <= 0 {
		cfg.CacheMaxBytes = defaultCacheMaxBytes
	}
	if cfg.CacheTrimToBytes <= 0 || cfg.CacheTrimToBytes >= cfg.CacheMaxBytes {
		cfg.CacheTrimToBytes = cfg.CacheMaxBytes * 4 / 5
	}
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: 15 * time.Second}
	}
	return &Service{
		key:              strings.TrimSpace(cfg.Key),
		upstreamEnabled:  cfg.UpstreamEnabled,
		uploadSigningKey: []byte(cfg.UploadSigningKey),
		cacheDir:         cfg.CacheDir,
		upstreamTemplate: cfg.UpstreamTemplate,
		client:           cfg.Client,
		upstreamSlots:    make(chan struct{}, cfg.UpstreamParallel),
		cacheMaxBytes:    cfg.CacheMaxBytes,
		cacheTrimToBytes: cfg.CacheTrimToBytes,
	}
}

func (s *Service) ServeTile(w http.ResponseWriter, r *http.Request) {
	s.ensureCacheUsage()

	layer := chi.URLParam(r, "layer")
	zoom, errZ := strconv.Atoi(chi.URLParam(r, "z"))
	x, errX := strconv.Atoi(chi.URLParam(r, "x"))
	y, errY := strconv.Atoi(chi.URLParam(r, "y"))
	if _, ok := allowedLayers[layer]; !ok || errZ != nil || errX != nil || errY != nil || !s.allowsTile(x, y, zoom) {
		http.Error(w, "tile outside supported area", http.StatusNotFound)
		return
	}
	cachePath := filepath.Join(s.cacheDir, layer, strconv.Itoa(zoom), strconv.Itoa(x), strconv.Itoa(y)+".tile")
	if tile, err := os.ReadFile(cachePath); err == nil {
		writeTile(w, tile, "HIT")
		return
	} else if !errors.Is(err, os.ErrNotExist) {
		http.Error(w, "map cache unavailable", http.StatusInternalServerError)
		return
	}

	if s.upstreamEnabled && s.key != "" {
		value, err, _ := s.requests.Do(cachePath, func() (any, error) {
			if tile, readErr := os.ReadFile(cachePath); readErr == nil {
				return tile, nil
			}
			tile, fetchErr := s.fetch(r, layer, zoom, x, y)
			if fetchErr != nil {
				return nil, fetchErr
			}
			if writeErr := writeFileAtomically(cachePath, tile); writeErr != nil {
				return nil, fmt.Errorf("cache tile: %w", writeErr)
			}
			s.accountNewTile(tile)
			return tile, nil
		})
		if err == nil {
			writeTile(w, value.([]byte), "MISS")
			return
		}
		log.Printf("map tile upstream failed layer=%s z=%d x=%d y=%d: %v", layer, zoom, x, y, err)
	}
	s.writeClientFetchMiss(w, layer, zoom, x, y)
}

// ServeUpload accepts a short-lived, coordinate-bound client upload. Existing
// cache entries are immutable: the first validated image wins.
func (s *Service) ServeUpload(w http.ResponseWriter, r *http.Request) {
	s.ensureCacheUsage()
	layer := chi.URLParam(r, "layer")
	zoom, errZ := strconv.Atoi(chi.URLParam(r, "z"))
	x, errX := strconv.Atoi(chi.URLParam(r, "x"))
	y, errY := strconv.Atoi(chi.URLParam(r, "y"))
	if _, ok := allowedLayers[layer]; !ok || errZ != nil || errX != nil || errY != nil || !s.allowsTile(x, y, zoom) {
		http.Error(w, "tile outside supported area", http.StatusNotFound)
		return
	}
	if !s.validUploadToken(r.Header.Get(uploadTokenHeader), layer, zoom, x, y) {
		http.Error(w, "invalid map tile upload token", http.StatusUnauthorized)
		return
	}

	body := http.MaxBytesReader(w, r.Body, maxTileSize)
	tile, err := io.ReadAll(body)
	if err != nil || len(tile) == 0 {
		http.Error(w, "invalid map tile", http.StatusBadRequest)
		return
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(tile))
	if err != nil || config.Width != 256 || config.Height != 256 || (format != "png" && format != "jpeg") {
		http.Error(w, "invalid map tile", http.StatusBadRequest)
		return
	}

	cachePath := filepath.Join(s.cacheDir, layer, strconv.Itoa(zoom), strconv.Itoa(x), strconv.Itoa(y)+".tile")
	_, err, _ = s.requests.Do(cachePath, func() (any, error) {
		if _, statErr := os.Stat(cachePath); statErr == nil {
			return nil, nil
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return nil, statErr
		}
		if writeErr := writeFileAtomically(cachePath, tile); writeErr != nil {
			return nil, writeErr
		}
		s.accountNewTile(tile)
		return nil, nil
	})
	if err != nil {
		http.Error(w, "map cache unavailable", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) accountNewTile(tile []byte) {
	if s.cacheUsage.Add(accountedTileSize(int64(len(tile)))) > s.cacheMaxBytes {
		s.pruneCache()
	}
}

func (s *Service) writeClientFetchMiss(w http.ResponseWriter, layer string, zoom, x, y int) {
	if len(s.uploadSigningKey) == 0 {
		http.Error(w, "map tile upload is not configured", http.StatusServiceUnavailable)
		return
	}
	expires := time.Now().Add(uploadTokenTTL).Unix()
	payload := fmt.Sprintf("%d:%s:%d:%d:%d", expires, layer, zoom, x, y)
	mac := hmac.New(sha256.New, s.uploadSigningKey)
	_, _ = mac.Write([]byte(payload))
	token := base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." +
		base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	w.Header().Set(uploadTokenHeader, token)
	w.Header().Set("Cache-Control", "no-store")
	http.Error(w, "map tile cache miss", http.StatusNotFound)
}

func (s *Service) validUploadToken(token, layer string, zoom, x, y int) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 2 || len(s.uploadSigningKey) == 0 {
		return false
	}
	payload, errPayload := base64.RawURLEncoding.DecodeString(parts[0])
	signature, errSignature := base64.RawURLEncoding.DecodeString(parts[1])
	if errPayload != nil || errSignature != nil {
		return false
	}
	fields := strings.Split(string(payload), ":")
	if len(fields) != 5 {
		return false
	}
	expires, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil || time.Now().Unix() > expires || expires > time.Now().Add(uploadTokenTTL+time.Minute).Unix() {
		return false
	}
	expectedPayload := fmt.Sprintf("%d:%s:%d:%d:%d", expires, layer, zoom, x, y)
	if subtle.ConstantTimeCompare(payload, []byte(expectedPayload)) != 1 {
		return false
	}
	mac := hmac.New(sha256.New, s.uploadSigningKey)
	_, _ = mac.Write(payload)
	return hmac.Equal(signature, mac.Sum(nil))
}

type cachedTile struct {
	path       string
	bytes      int64
	modifiedAt time.Time
}

// ensureCacheUsage scans once after each process start. Accounting rounds every
// tile up to a filesystem block so large collections of small files cannot
// silently exceed the configured budget.
func (s *Service) ensureCacheUsage() {
	s.cacheScan.Do(func() {
		var total int64
		_ = filepath.WalkDir(s.cacheDir, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil || entry.IsDir() || filepath.Ext(path) != ".tile" {
				return nil
			}
			if info, err := entry.Info(); err == nil {
				total += accountedTileSize(info.Size())
			}
			return nil
		})
		s.cacheUsage.Store(total)
		if total > s.cacheMaxBytes {
			s.pruneCache()
		}
	})
}

// pruneCache uses insertion/modification time as a durable FIFO approximation.
// Cache hits remain read-only, avoiding a disk metadata write for every tile.
func (s *Service) pruneCache() {
	s.cleanupMu.Lock()
	defer s.cleanupMu.Unlock()
	if s.cacheUsage.Load() <= s.cacheMaxBytes {
		return
	}

	var tiles []cachedTile
	var total int64
	_ = filepath.WalkDir(s.cacheDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || filepath.Ext(path) != ".tile" {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		bytes := accountedTileSize(info.Size())
		total += bytes
		tiles = append(tiles, cachedTile{path: path, bytes: bytes, modifiedAt: info.ModTime()})
		return nil
	})
	sort.Slice(tiles, func(i, j int) bool {
		return tiles[i].modifiedAt.Before(tiles[j].modifiedAt)
	})
	for _, tile := range tiles {
		if total <= s.cacheTrimToBytes {
			break
		}
		if err := os.Remove(tile.path); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				log.Printf("map tile cache: remove %s: %v", tile.path, err)
			}
			continue
		}
		total -= tile.bytes
	}
	s.cacheUsage.Store(total)
}

func accountedTileSize(bytes int64) int64 {
	if bytes <= 0 {
		return diskBlockSize
	}
	return (bytes + diskBlockSize - 1) / diskBlockSize * diskBlockSize
}

func (s *Service) fetch(r *http.Request, layer string, zoom, x, y int) ([]byte, error) {
	select {
	case s.upstreamSlots <- struct{}{}:
		defer func() { <-s.upstreamSlots }()
	case <-r.Context().Done():
		return nil, r.Context().Err()
	}

	base := strings.ReplaceAll(s.upstreamTemplate, "{layer}", layer)
	uri, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	query := uri.Query()
	query.Set("SERVICE", "WMTS")
	query.Set("REQUEST", "GetTile")
	query.Set("VERSION", "1.0.0")
	query.Set("LAYER", layer)
	query.Set("STYLE", "default")
	query.Set("TILEMATRIXSET", "w")
	query.Set("FORMAT", "tiles")
	query.Set("TILEMATRIX", strconv.Itoa(zoom))
	query.Set("TILEROW", strconv.Itoa(y))
	query.Set("TILECOL", strconv.Itoa(x))
	query.Set("tk", s.key)
	uri.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, uri.String(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "ZoneNaN-MapCache/1.0")
	response, err := s.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream status %d", response.StatusCode)
	}
	tile, err := io.ReadAll(io.LimitReader(response.Body, maxTileSize+1))
	if err != nil {
		return nil, err
	}
	if len(tile) == 0 || len(tile) > maxTileSize || !strings.HasPrefix(http.DetectContentType(tile), "image/") {
		return nil, errors.New("upstream returned an invalid tile")
	}
	return tile, nil
}

func writeFileAtomically(path string, contents []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".tile-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o640); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func writeTile(w http.ResponseWriter, tile []byte, cacheStatus string) {
	w.Header().Set("Content-Type", http.DetectContentType(tile))
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("X-ZoneNaN-Map-Cache", cacheStatus)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(tile)
}

type geoRect struct {
	west  float64
	south float64
	east  float64
	north float64
}

func (s *Service) allowsTile(x, y, zoom int) bool {
	if zoom < minZoom || zoom > maxZoom || x < 0 || y < 0 {
		return false
	}
	scale := math.Exp2(float64(zoom))
	if float64(x) >= scale || float64(y) >= scale {
		return false
	}
	rect := geoRect{
		west:  float64(x)/scale*360 - 180,
		south: tileLatitude(y+1, scale),
		east:  float64(x+1)/scale*360 - 180,
		north: tileLatitude(y, scale),
	}
	return rect.west <= supportedEast && rect.east >= supportedWest &&
		rect.south <= supportedNorth && rect.north >= supportedSouth
}

func tileLatitude(y int, scale float64) float64 {
	value := math.Pi * (1 - 2*float64(y)/scale)
	return math.Atan(math.Sinh(value)) * 180 / math.Pi
}
