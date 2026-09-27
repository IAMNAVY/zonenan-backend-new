package maptiles

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

var testPNG = makeTestPNG()

func makeTestPNG() []byte {
	canvas := image.NewRGBA(image.Rect(0, 0, 256, 256))
	canvas.Set(0, 0, color.RGBA{R: 42, G: 110, B: 200, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, canvas); err != nil {
		panic(err)
	}
	return encoded.Bytes()
}

func TestServeTileCachesUpstreamResponseOnDisk(t *testing.T) {
	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		if got := r.URL.Query().Get("tk"); got != "server-secret" {
			t.Fatalf("upstream tk = %q", got)
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(testPNG)
	}))
	defer upstream.Close()

	service := New(Config{
		Key:              "server-secret",
		UpstreamEnabled:  true,
		CacheDir:         t.TempDir(),
		UpstreamTemplate: upstream.URL + "/{layer}",
	})
	router := tileRouter(service)
	x, y := coordinateTile(112.927, 28.179, 18)
	path := tilePath("vec", 18, x, y)

	first := httptest.NewRecorder()
	router.ServeHTTP(first, httptest.NewRequest(http.MethodGet, path, nil))
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d, body = %q", first.Code, first.Body.String())
	}
	if got := first.Header().Get("X-ZoneNaN-Map-Cache"); got != "MISS" {
		t.Fatalf("first cache status = %q", got)
	}

	second := httptest.NewRecorder()
	router.ServeHTTP(second, httptest.NewRequest(http.MethodGet, path, nil))
	if second.Code != http.StatusOK {
		t.Fatalf("second status = %d, body = %q", second.Code, second.Body.String())
	}
	if got := second.Header().Get("X-ZoneNaN-Map-Cache"); got != "HIT" {
		t.Fatalf("second cache status = %q", got)
	}
	if got := upstreamCalls.Load(); got != 1 {
		t.Fatalf("upstream calls = %d, want 1", got)
	}
}

func TestServeTileCoalescesConcurrentCacheMisses(t *testing.T) {
	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamCalls.Add(1)
		time.Sleep(25 * time.Millisecond)
		_, _ = w.Write(testPNG)
	}))
	defer upstream.Close()

	service := New(Config{
		Key:              "server-secret",
		UpstreamEnabled:  true,
		CacheDir:         t.TempDir(),
		UpstreamTemplate: upstream.URL + "/{layer}",
	})
	router := tileRouter(service)
	x, y := coordinateTile(112.927, 28.179, 18)
	path := tilePath("cva", 18, x, y)

	const requests = 12
	var wait sync.WaitGroup
	wait.Add(requests)
	for range requests {
		go func() {
			defer wait.Done()
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
			if response.Code != http.StatusOK {
				t.Errorf("status = %d, body = %q", response.Code, response.Body.String())
			}
		}()
	}
	wait.Wait()
	if got := upstreamCalls.Load(); got != 1 {
		t.Fatalf("upstream calls = %d, want 1", got)
	}
}

func TestServeTileRejectsOutsideRegionBeforeUpstream(t *testing.T) {
	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamCalls.Add(1)
		_, _ = w.Write(testPNG)
	}))
	defer upstream.Close()

	service := New(Config{
		Key:              "server-secret",
		UpstreamEnabled:  true,
		CacheDir:         t.TempDir(),
		UpstreamTemplate: upstream.URL + "/{layer}",
	})
	router := tileRouter(service)
	x, y := coordinateTile(113.35, 28.19, 18)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, tilePath("vec", 18, x, y), nil))

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.Code)
	}
	if got := upstreamCalls.Load(); got != 0 {
		t.Fatalf("upstream calls = %d, want 0", got)
	}
}

func TestAllowsTilesAtFixedUrbanLandmarkBounds(t *testing.T) {
	service := New(Config{CacheDir: t.TempDir()})
	for _, location := range []struct {
		name     string
		lon, lat float64
	}{
		{name: "梅溪湖西界", lon: 112.82, lat: 28.17},
		{name: "华谊电影小镇南界", lon: 112.916677, lat: 28.078062},
		{name: "杜家坪东界", lon: 113.08726, lat: 28.143758},
		{name: "秀峰山北界", lon: 112.96326, lat: 28.2988},
	} {
		x, y := coordinateTile(location.lon, location.lat, 18)
		if !service.allowsTile(x, y, 18) {
			t.Fatalf("%s tile should be allowed", location.name)
		}
	}

	for _, location := range []struct {
		lon, lat float64
	}{
		{lon: 112.70, lat: 28.17},
		{lon: 112.95, lat: 27.95},
		{lon: 113.25, lat: 28.17},
		{lon: 112.95, lat: 28.45},
	} {
		x, y := coordinateTile(location.lon, location.lat, 18)
		if service.allowsTile(x, y, 18) {
			t.Fatalf("outside tile at %.4f, %.4f should be rejected", location.lon, location.lat)
		}
	}
}

func TestClientCanFillSignedCacheMiss(t *testing.T) {
	service := New(Config{CacheDir: t.TempDir(), UploadSigningKey: "upload-secret"})
	router := tileRouter(service)
	x, y := coordinateTile(112.927, 28.179, 18)
	path := tilePath("vec", 18, x, y)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("miss status = %d, want 404", response.Code)
	}
	token := response.Header().Get(uploadTokenHeader)
	if token == "" {
		t.Fatal("cache miss did not include upload token")
	}

	upload := httptest.NewRequest(http.MethodPut, path, bytes.NewReader(testPNG))
	upload.Header.Set(uploadTokenHeader, token)
	uploadResponse := httptest.NewRecorder()
	router.ServeHTTP(uploadResponse, upload)
	if uploadResponse.Code != http.StatusNoContent {
		t.Fatalf("upload status = %d, body = %q", uploadResponse.Code, uploadResponse.Body.String())
	}

	hit := httptest.NewRecorder()
	router.ServeHTTP(hit, httptest.NewRequest(http.MethodGet, path, nil))
	if hit.Code != http.StatusOK || hit.Header().Get("X-ZoneNaN-Map-Cache") != "HIT" {
		t.Fatalf("hit status = %d cache = %q", hit.Code, hit.Header().Get("X-ZoneNaN-Map-Cache"))
	}
}

func TestServeTilePrunesOldTilesToLowWatermark(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(testPNG)
	}))
	defer upstream.Close()

	cacheDir := t.TempDir()
	service := New(Config{
		Key:              "server-secret",
		UpstreamEnabled:  true,
		CacheDir:         cacheDir,
		UpstreamTemplate: upstream.URL + "/{layer}",
		CacheMaxBytes:    2 * diskBlockSize,
		CacheTrimToBytes: diskBlockSize,
	})
	router := tileRouter(service)
	for _, lon := range []float64{112.925, 112.929, 112.933} {
		x, y := coordinateTile(lon, 28.179, 18)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, tilePath("vec", 18, x, y), nil))
		if response.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
		}
		time.Sleep(2 * time.Millisecond)
	}

	var tileCount int
	err := filepath.WalkDir(cacheDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr == nil && !entry.IsDir() && filepath.Ext(path) == ".tile" {
			tileCount++
		}
		return walkErr
	})
	if err != nil {
		t.Fatal(err)
	}
	if tileCount != 1 {
		t.Fatalf("cached tile count = %d, want 1", tileCount)
	}
	if got := service.cacheUsage.Load(); got > diskBlockSize {
		t.Fatalf("accounted cache usage = %d, want <= %d", got, diskBlockSize)
	}
}

func tileRouter(service *Service) http.Handler {
	router := chi.NewRouter()
	router.Get("/map/tiles/{layer}/{z}/{x}/{y}.png", service.ServeTile)
	router.Put("/map/tiles/{layer}/{z}/{x}/{y}.png", service.ServeUpload)
	return router
}

func tilePath(layer string, zoom, x, y int) string {
	return "/map/tiles/" + layer + "/" + strconv.Itoa(zoom) + "/" + strconv.Itoa(x) + "/" + strconv.Itoa(y) + ".png"
}

func coordinateTile(lon, lat float64, zoom int) (int, int) {
	scale := math.Exp2(float64(zoom))
	x := int(math.Floor((lon + 180) / 360 * scale))
	latRad := lat * math.Pi / 180
	y := int(math.Floor((1 - math.Asinh(math.Tan(latRad))/math.Pi) / 2 * scale))
	return x, y
}
