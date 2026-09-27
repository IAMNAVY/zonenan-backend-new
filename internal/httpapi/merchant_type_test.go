package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"zonenan-backend/internal/merchantauth"
)

func TestRequireMerchantTypesRejectsRestaurantFromApartment(t *testing.T) {
	handler := requireMerchantTypes("commercial_apartment")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "/merchant-api/v1/apartment", nil)
	request = request.WithContext(contextWithMerchant(request, &merchantauth.Principal{MerchantType: "restaurant"}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("restaurant apartment access status = %d, want 403", recorder.Code)
	}
}

func TestRequireMerchantTypesAllowsCommercialApartment(t *testing.T) {
	handler := requireMerchantTypes("commercial_apartment")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "/merchant-api/v1/apartment", nil)
	request = request.WithContext(contextWithMerchant(request, &merchantauth.Principal{MerchantType: "commercial_apartment"}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("commercial apartment access status = %d, want 204", recorder.Code)
	}
}
