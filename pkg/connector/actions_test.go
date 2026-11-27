package connector

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/conductorone/baton-box/pkg/box"
	"github.com/conductorone/baton-sdk/pkg/uhttp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

// transferMock is a minimal Box API for the transfer action: GET /2.0/users/{id}
// and PUT /2.0/users/{id}/folders/0.
type transferMock struct {
	users          map[string]string // user ID → status
	transferStatus int               // when non-zero, PUT responds with this error status
	transferCode   string
	transferDelay  time.Duration // how long PUT waits before answering

	// mu guards rec: the server goroutine writes it while the test reads it.
	mu  sync.Mutex
	rec transferRecord
}

// transferRecord is what the mock recorded from the last transfer request.
type transferRecord struct {
	calls     int
	sourceID  string
	ownedByID string
	notify    string
}

func (m *transferMock) recorded() transferRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rec
}

func (m *transferMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	path := strings.TrimPrefix(r.URL.Path, "/2.0/users/")

	if sourceID, ok := strings.CutSuffix(path, "/folders/0"); ok && r.Method == http.MethodPut {
		var body struct {
			OwnedBy struct {
				ID string `json:"id"`
			} `json:"owned_by"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		m.mu.Lock()
		m.rec = transferRecord{
			calls:     m.rec.calls + 1,
			sourceID:  sourceID,
			ownedByID: body.OwnedBy.ID,
			notify:    r.URL.Query().Get("notify"),
		}
		m.mu.Unlock()

		if m.transferDelay > 0 {
			select {
			case <-time.After(m.transferDelay):
			case <-r.Context().Done():
				return
			}
		}
		if m.transferStatus != 0 {
			w.WriteHeader(m.transferStatus)
			_ = json.NewEncoder(w).Encode(map[string]string{"code": m.transferCode, "message": "transfer refused"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"id": "folder-123", "type": "folder", "name": "Source User's Files and Folders",
		})
		return
	}

	if r.Method == http.MethodGet {
		st, ok := m.users[path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"code": "not_found", "message": "not found"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"id": path, "type": "user", "name": "Name " + path, "status": st})
		return
	}

	w.WriteHeader(http.StatusMethodNotAllowed)
}

func newTransferTest(t *testing.T, m *transferMock, opts ...box.ClientOption) *userResourceType {
	t.Helper()
	srv := httptest.NewServer(m)
	t.Cleanup(srv.Close)
	client, err := box.NewClient(srv.Client(), "test-token", srv.URL, opts...)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return userBuilder(client, true)
}

// shortenTransferWait sets transferWaitTimeout for one test.
func shortenTransferWait(t *testing.T, d time.Duration) {
	t.Helper()
	orig := transferWaitTimeout
	transferWaitTimeout = d
	t.Cleanup(func() { transferWaitTimeout = orig })
}

// assertInProgress checks an in-progress transfer result.
func assertInProgress(t *testing.T, ret *structpb.Struct) {
	t.Helper()
	if got := ret.Fields[retStatusKey].GetStringValue(); got != transferStatusInProgress {
		t.Errorf("status = %q, want %q", got, transferStatusInProgress)
	}
	if !ret.Fields[retSuccessKey].GetBoolValue() {
		t.Errorf("success = false, want true")
	}
	if got := ret.Fields[retFolderIDKey].GetStringValue(); got != "" {
		t.Errorf("folder_id = %q, want empty while in progress", got)
	}
	if got := ret.Fields[retFolderNameKey].GetStringValue(); got != "Name src's Files and Folders" {
		t.Errorf("folder_name = %q, want the name Box will give the folder", got)
	}
}

func transferArgs(t *testing.T, fields map[string]interface{}) *structpb.Struct {
	t.Helper()
	s, err := structpb.NewStruct(fields)
	if err != nil {
		t.Fatalf("NewStruct: %v", err)
	}
	return s
}

func TestTransferUserFiles_Success(t *testing.T) {
	m := &transferMock{users: map[string]string{"src": "active", "dst": "active"}}
	o := newTransferTest(t, m)

	ret, _, err := o.transferUserFilesActionHandler(context.Background(), transferArgs(t, map[string]interface{}{
		fieldUserID:     "src",
		argTargetUserID: " dst ",
		argNotify:       true,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rec := m.recorded()
	if rec.calls != 1 || rec.sourceID != "src" || rec.ownedByID != "dst" {
		t.Fatalf("unexpected transfer request: calls=%d source=%q owned_by=%q", rec.calls, rec.sourceID, rec.ownedByID)
	}
	if rec.notify != "true" {
		t.Errorf("notify query param = %q, want %q", rec.notify, "true")
	}
	if !ret.Fields[retSuccessKey].GetBoolValue() {
		t.Errorf("success = false, want true")
	}
	if got := ret.Fields[retStatusKey].GetStringValue(); got != transferStatusCompleted {
		t.Errorf("status = %q, want %q", got, transferStatusCompleted)
	}
	if got := ret.Fields[retFolderIDKey].GetStringValue(); got != "folder-123" {
		t.Errorf("folder_id = %q, want %q", got, "folder-123")
	}
	if got := ret.Fields[retFolderNameKey].GetStringValue(); got != "Source User's Files and Folders" {
		t.Errorf("folder_name = %q", got)
	}
}

func TestTransferUserFiles_NotifyDefaultsToFalse(t *testing.T) {
	m := &transferMock{users: map[string]string{"src": "active", "dst": "active"}}
	o := newTransferTest(t, m)

	if _, _, err := o.transferUserFilesActionHandler(context.Background(), transferArgs(t, map[string]interface{}{
		fieldUserID:     "src",
		argTargetUserID: "dst",
	})); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := m.recorded().notify; got != "false" {
		t.Errorf("notify query param = %q, want %q", got, "false")
	}
}

func TestTransferUserFiles_InvalidInput(t *testing.T) {
	tests := []struct {
		name     string
		args     map[string]interface{}
		wantCode codes.Code
	}{
		{"missing source", map[string]interface{}{argTargetUserID: "dst"}, codes.InvalidArgument},
		{"missing target", map[string]interface{}{fieldUserID: "src"}, codes.InvalidArgument},
		{"blank target", map[string]interface{}{fieldUserID: "src", argTargetUserID: "  "}, codes.InvalidArgument},
		{"same user", map[string]interface{}{fieldUserID: "src", argTargetUserID: "src"}, codes.InvalidArgument},
		{"notify not bool", map[string]interface{}{fieldUserID: "src", argTargetUserID: "dst", argNotify: "yes"}, codes.InvalidArgument},
		{"source not found", map[string]interface{}{fieldUserID: "ghost", argTargetUserID: "dst"}, codes.InvalidArgument},
		{"target not found", map[string]interface{}{fieldUserID: "src", argTargetUserID: "ghost"}, codes.InvalidArgument},
		{"target inactive", map[string]interface{}{fieldUserID: "src", argTargetUserID: "inactive"}, codes.FailedPrecondition},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &transferMock{users: map[string]string{"src": "active", "dst": "active", "inactive": "inactive"}}
			o := newTransferTest(t, m)

			_, _, err := o.transferUserFilesActionHandler(context.Background(), transferArgs(t, tt.args))
			if got := status.Code(err); got != tt.wantCode {
				t.Fatalf("code = %v, want %v (err: %v)", got, tt.wantCode, err)
			}
			if calls := m.recorded().calls; calls != 0 {
				t.Errorf("transfer was called %d times, want 0", calls)
			}
		})
	}
}

func TestTransferUserFiles_APIError(t *testing.T) {
	tests := []struct {
		name       string
		respStatus int
		respCode   string
		wantCode   codes.Code
	}{
		{"information barrier", http.StatusForbidden, "forbidden_by_policy", codes.PermissionDenied},
		{"rate limited", http.StatusTooManyRequests, "rate_limit_exceeded", codes.Unavailable},
		{"server error", http.StatusInternalServerError, "internal_server_error", codes.Unavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &transferMock{
				users:          map[string]string{"src": "active", "dst": "active"},
				transferStatus: tt.respStatus,
				transferCode:   tt.respCode,
			}
			o := newTransferTest(t, m)

			ret, _, err := o.transferUserFilesActionHandler(context.Background(), transferArgs(t, map[string]interface{}{
				fieldUserID:     "src",
				argTargetUserID: "dst",
			}))
			if err == nil {
				t.Fatalf("expected an error, got result %v", ret)
			}
			if got := status.Code(err); got != tt.wantCode {
				t.Errorf("code = %v, want %v (err: %v)", got, tt.wantCode, err)
			}
			if !strings.Contains(err.Error(), tt.respCode) {
				t.Errorf("error %q does not carry the Box error code", err)
			}
		})
	}
}

func TestTransferUserFiles_StillRunningAfterWaitWindow(t *testing.T) {
	shortenTransferWait(t, 50*time.Millisecond)

	m := &transferMock{
		users:         map[string]string{"src": "active", "dst": "active"},
		transferDelay: 2 * time.Second,
	}
	o := newTransferTest(t, m)

	start := time.Now()
	ret, _, err := o.transferUserFilesActionHandler(context.Background(), transferArgs(t, map[string]interface{}{
		fieldUserID:     "src",
		argTargetUserID: "dst",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("handler waited %v, want it to stop at the wait window", elapsed)
	}
	if calls := m.recorded().calls; calls != 1 {
		t.Errorf("transfer calls = %d, want 1", calls)
	}
	assertInProgress(t, ret)
}

// A response-header timeout in the HTTP transport, like the 60s one uhttp.NewClient
// sets, means Box has the request and has not answered, so it is in progress too.
func TestTransferUserFiles_HeaderTimeoutIsInProgress(t *testing.T) {
	shortenTransferWait(t, 5*time.Second)

	m := &transferMock{
		users:         map[string]string{"src": "active", "dst": "active"},
		transferDelay: 2 * time.Second,
	}
	headerTimeoutClient := &http.Client{Transport: &http.Transport{ResponseHeaderTimeout: 50 * time.Millisecond}}
	o := newTransferTest(t, m, box.WithTransferHTTPClient(headerTimeoutClient))

	start := time.Now()
	ret, _, err := o.transferUserFilesActionHandler(context.Background(), transferArgs(t, map[string]interface{}{
		fieldUserID:     "src",
		argTargetUserID: "dst",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("handler waited %v, want it to stop at the header timeout", elapsed)
	}
	assertInProgress(t, ret)
}

func TestTransferUserFiles_GatewayTimeoutIsInProgress(t *testing.T) {
	m := &transferMock{
		users:          map[string]string{"src": "active", "dst": "active"},
		transferStatus: http.StatusGatewayTimeout,
		transferCode:   "gateway_timeout",
	}
	o := newTransferTest(t, m)

	ret, _, err := o.transferUserFilesActionHandler(context.Background(), transferArgs(t, map[string]interface{}{
		fieldUserID:     "src",
		argTargetUserID: "dst",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertInProgress(t, ret)
}

// The production wiring: uhttp.NewClient for every call except the transfer request,
// which goes through box.NewTransferHTTPClient.
func TestTransferUserFiles_ProductionClients(t *testing.T) {
	ctx := context.Background()
	newProdTest := func(t *testing.T, m *transferMock) *userResourceType {
		t.Helper()
		srv := httptest.NewServer(m)
		t.Cleanup(srv.Close)
		httpClient, err := uhttp.NewClient(ctx)
		if err != nil {
			t.Fatalf("uhttp.NewClient: %v", err)
		}
		client, err := box.NewClient(httpClient, "test-token", srv.URL, box.WithTransferHTTPClient(box.NewTransferHTTPClient()))
		if err != nil {
			t.Fatalf("NewClient: %v", err)
		}
		return userBuilder(client, true)
	}
	args := map[string]interface{}{fieldUserID: "src", argTargetUserID: "dst"}

	t.Run("completed", func(t *testing.T) {
		m := &transferMock{users: map[string]string{"src": "active", "dst": "active"}}
		ret, _, err := newProdTest(t, m).transferUserFilesActionHandler(ctx, transferArgs(t, args))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := ret.Fields[retStatusKey].GetStringValue(); got != transferStatusCompleted {
			t.Errorf("status = %q, want %q", got, transferStatusCompleted)
		}
	})

	t.Run("still running", func(t *testing.T) {
		shortenTransferWait(t, 100*time.Millisecond)
		m := &transferMock{users: map[string]string{"src": "active", "dst": "active"}, transferDelay: 2 * time.Second}
		ret, _, err := newProdTest(t, m).transferUserFilesActionHandler(ctx, transferArgs(t, args))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertInProgress(t, ret)
	})
}

func TestTransferUserFiles_CancelledContextIsAnError(t *testing.T) {
	m := &transferMock{
		users:         map[string]string{"src": "active", "dst": "active"},
		transferDelay: 2 * time.Second,
	}
	o := newTransferTest(t, m)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, _, err := o.transferUserFilesActionHandler(ctx, transferArgs(t, map[string]interface{}{
		fieldUserID:     "src",
		argTargetUserID: "dst",
	}))
	if err == nil {
		t.Fatal("expected an error when the caller's context expires before the wait window")
	}
}
