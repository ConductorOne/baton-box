package connector

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/conductorone/baton-box/pkg/box"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// newDeleteTest starts a server that answers DELETE /2.0/users/{id} with the given
// status and Box error code (204 means success) and records the force query param.
func newDeleteTest(t *testing.T, forceDelete bool, respStatus int, respCode string) (*userResourceType, *string) {
	t.Helper()
	var gotForce string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		gotForce = r.URL.Query().Get("force")
		if respStatus == http.StatusNoContent {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(respStatus)
		_ = json.NewEncoder(w).Encode(map[string]string{"code": respCode, "message": "refused"})
	}))
	t.Cleanup(srv.Close)

	client, err := box.NewClient(srv.Client(), "test-token", srv.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return userBuilder(client, forceDelete), &gotForce
}

func userID(id string) *v2.ResourceId {
	return v2.ResourceId_builder{ResourceType: resourceTypeUser.GetId(), Resource: id}.Build()
}

func TestDelete_ForceParam(t *testing.T) {
	for _, tt := range []struct {
		name        string
		forceDelete bool
		want        string
	}{
		{"force by default", true, "true"},
		{"no force when disabled", false, "false"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			o, gotForce := newDeleteTest(t, tt.forceDelete, http.StatusNoContent, "")
			if _, err := o.Delete(context.Background(), userID("u1"), nil); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if *gotForce != tt.want {
				t.Errorf("force = %q, want %q", *gotForce, tt.want)
			}
		})
	}
}

func TestDelete_NotFoundIsSuccess(t *testing.T) {
	for _, forceDelete := range []bool{true, false} {
		o, _ := newDeleteTest(t, forceDelete, http.StatusNotFound, "not_found")
		if _, err := o.Delete(context.Background(), userID("u1"), nil); err != nil {
			t.Errorf("forceDelete=%v: unexpected error: %v", forceDelete, err)
		}
	}
}

func TestDelete_RefusedWithoutForce(t *testing.T) {
	for _, respStatus := range []int{http.StatusBadRequest, http.StatusConflict, http.StatusPreconditionFailed} {
		o, _ := newDeleteTest(t, false, respStatus, "refused")
		_, err := o.Delete(context.Background(), userID("u1"), nil)
		if got := status.Code(err); got != codes.FailedPrecondition {
			t.Errorf("status %d: code = %v, want FailedPrecondition (err: %v)", respStatus, got, err)
		}
	}
}

func TestDelete_OtherErrorsKeepTheirMeaning(t *testing.T) {
	for _, tt := range []struct {
		name        string
		forceDelete bool
		respStatus  int
		wantCode    codes.Code
	}{
		{"rate limited without force", false, http.StatusTooManyRequests, codes.Unavailable},
		{"permission error without force", false, http.StatusForbidden, codes.PermissionDenied},
		{"unauthenticated without force", false, http.StatusUnauthorized, codes.Unauthenticated},
		{"server error without force", false, http.StatusInternalServerError, codes.Unavailable},
		{"client error with force", true, http.StatusBadRequest, codes.InvalidArgument},
		{"rate limited with force", true, http.StatusTooManyRequests, codes.Unavailable},
	} {
		t.Run(tt.name, func(t *testing.T) {
			o, _ := newDeleteTest(t, tt.forceDelete, tt.respStatus, "refused")
			_, err := o.Delete(context.Background(), userID("u1"), nil)
			if got := status.Code(err); got != tt.wantCode {
				t.Errorf("code = %v, want %v (err: %v)", got, tt.wantCode, err)
			}
		})
	}
}
