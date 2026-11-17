package connector

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/conductorone/baton-box/pkg/box"
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

	// Recorded from the last transfer request.
	transferCalls  int
	gotSourceID    string
	gotOwnedByID   string
	gotNotifyParam string
}

func (m *transferMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	path := strings.TrimPrefix(r.URL.Path, "/2.0/users/")

	if sourceID, ok := strings.CutSuffix(path, "/folders/0"); ok && r.Method == http.MethodPut {
		m.transferCalls++
		m.gotSourceID = sourceID
		m.gotNotifyParam = r.URL.Query().Get("notify")
		var body struct {
			OwnedBy struct {
				ID string `json:"id"`
			} `json:"owned_by"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		m.gotOwnedByID = body.OwnedBy.ID

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

func newTransferTest(t *testing.T, m *transferMock) *userResourceType {
	t.Helper()
	srv := httptest.NewServer(m)
	t.Cleanup(srv.Close)
	client, err := box.NewClient(srv.Client(), "test-token", srv.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return userBuilder(client, true)
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

	if m.transferCalls != 1 || m.gotSourceID != "src" || m.gotOwnedByID != "dst" {
		t.Fatalf("unexpected transfer request: calls=%d source=%q owned_by=%q", m.transferCalls, m.gotSourceID, m.gotOwnedByID)
	}
	if m.gotNotifyParam != "true" {
		t.Errorf("notify query param = %q, want %q", m.gotNotifyParam, "true")
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
	if m.gotNotifyParam != "false" {
		t.Errorf("notify query param = %q, want %q", m.gotNotifyParam, "false")
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
			if m.transferCalls != 0 {
				t.Errorf("transfer was called %d times, want 0", m.transferCalls)
			}
		})
	}
}

func TestTransferUserFiles_APIError(t *testing.T) {
	m := &transferMock{
		users:          map[string]string{"src": "active", "dst": "active"},
		transferStatus: http.StatusForbidden,
		transferCode:   "forbidden_by_policy",
	}
	o := newTransferTest(t, m)

	ret, _, err := o.transferUserFilesActionHandler(context.Background(), transferArgs(t, map[string]interface{}{
		fieldUserID:     "src",
		argTargetUserID: "dst",
	}))
	if err == nil {
		t.Fatalf("expected an error, got result %v", ret)
	}
	if !strings.Contains(err.Error(), "forbidden_by_policy") {
		t.Errorf("error %q does not carry the Box error code", err)
	}
}

func TestTransferUserFiles_StillRunningAfterWaitWindow(t *testing.T) {
	orig := transferWaitTimeout
	transferWaitTimeout = 50 * time.Millisecond
	t.Cleanup(func() { transferWaitTimeout = orig })

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
	if m.transferCalls != 1 {
		t.Errorf("transfer calls = %d, want 1", m.transferCalls)
	}
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
