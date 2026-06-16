package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	port := os.Getenv("PORT")
	if port == "" {
		port = "0" // bind to a random available port
	}

	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", ":"+port)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	port = strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	baseURL := "http://localhost:" + port

	s := newServer()
	s.seed()

	mux := http.NewServeMux()

	// OAuth2 client-credentials token endpoint (accepts any client_id/secret, returns static token).
	mux.HandleFunc("/oauth2/token", s.handleToken)

	// Users: /2.0/users (list + create) and /2.0/users/{id} (get/update/delete + /me).
	mux.HandleFunc("/2.0/users/", s.handleUserByID)
	mux.HandleFunc("/2.0/users", s.handleUsers)

	// Groups: /2.0/groups (list) and /2.0/groups/{id}/memberships.
	mux.HandleFunc("/2.0/groups/", s.handleGroupSub)
	mux.HandleFunc("/2.0/groups", s.handleGroups)

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("Box mock server listening on %s", baseURL)
	log.Printf("Connect with:")
	log.Printf("  ./dist/$(go env GOOS)_$(go env GOARCH)/baton-box \\")
	log.Printf("    --base-url %s \\", baseURL)
	log.Printf("    --box-client-id test \\")
	log.Printf("    --box-client-secret test \\")
	log.Printf("    --enterprise-id %s", seedEnterpriseID)

	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// ─── constants ────────────────────────────────────────────────────────────────

const (
	staticToken = "test-bearer-token"

	seedEnterpriseID   = "ent-001"
	seedEnterpriseName = "Acme Corp"

	// Seeded user IDs.
	seedUserAliceID = "u-001" // enterprise admin (role=admin)
	seedUserBobID   = "u-002" // co-admin       (role=coadmin)
	seedUserCarolID = "u-003" // regular user   (role=user, active)
	seedUserDaveID  = "u-004" // inactive user  (role=user, status=inactive)

	// Seeded group IDs.
	seedGroupEngineeringID = "grp-001"
	seedGroupFinanceID     = "grp-002"

	// Box resource type strings and role values.
	statusActive = "active"
	roleMember   = "member"
	roleUser     = "user"
	typeGroup    = "group"

	// JSON field keys used in response maps.
	keyType   = "type"
	keyName   = "name"
	keyRole   = "role"
	keyStatus = "status"
)

// ─── server ───────────────────────────────────────────────────────────────────

type server struct {
	enterprise  mockEnterprise
	users       map[string]*mockUser
	groups      map[string]*mockGroup
	memberships map[string][]*mockMembership // group ID → ordered membership list
	mu          sync.Mutex
	nextUserSeq int // used to generate IDs for created users
}

func newServer() *server {
	return &server{
		users:       make(map[string]*mockUser),
		groups:      make(map[string]*mockGroup),
		memberships: make(map[string][]*mockMembership),
	}
}

// ─── seeded data ──────────────────────────────────────────────────────────────

func (s *server) seed() {
	s.enterprise = mockEnterprise{ID: seedEnterpriseID, Name: seedEnterpriseName}
	s.nextUserSeq = 5 // created users get IDs u-005, u-006, …

	// Users covering all three role values and both status values.
	s.users[seedUserAliceID] = &mockUser{
		ID: seedUserAliceID, Name: "Alice Anderson", Login: "alice@example.com",
		Role: "admin", Status: statusActive,
	}
	s.users[seedUserBobID] = &mockUser{
		ID: seedUserBobID, Name: "Bob Brown", Login: "bob@example.com",
		Role: "coadmin", Status: statusActive,
	}
	s.users[seedUserCarolID] = &mockUser{
		ID: seedUserCarolID, Name: "Carol Clark", Login: "carol@example.com",
		Role: roleUser, Status: statusActive,
	}
	s.users[seedUserDaveID] = &mockUser{
		// inactive — exercises STATUS_DISABLED path in userResource
		ID: seedUserDaveID, Name: "Dave Davis", Login: "dave@example.com",
		Role: roleUser, Status: "inactive",
	}

	s.groups[seedGroupEngineeringID] = &mockGroup{
		ID: seedGroupEngineeringID, Name: "Engineering",
		InvitabilityLevel: "all_managed_users", MemberViewabilityLevel: "all_managed_users",
	}
	s.groups[seedGroupFinanceID] = &mockGroup{
		ID: seedGroupFinanceID, Name: "Finance",
		InvitabilityLevel: "admins_only", MemberViewabilityLevel: "admins_only",
	}

	// Memberships — role "admin" within a group means group admin, not enterprise admin.
	s.memberships[seedGroupEngineeringID] = []*mockMembership{
		{ID: "gm-001", Role: "admin", UserID: seedUserAliceID},
		{ID: "gm-002", Role: roleMember, UserID: seedUserBobID},
		{ID: "gm-003", Role: roleMember, UserID: seedUserCarolID},
	}
	s.memberships[seedGroupFinanceID] = []*mockMembership{
		{ID: "gm-004", Role: roleMember, UserID: seedUserDaveID},
	}
}

// ─── auth ─────────────────────────────────────────────────────────────────────

func (s *server) authenticate(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("Authorization") != "Bearer "+staticToken {
		writeError(w, http.StatusUnauthorized, "unauthorized", "invalid or missing bearer token")
		return false
	}
	return true
}

// ─── handlers ─────────────────────────────────────────────────────────────────

// handleToken mocks the Box OAuth2 client-credentials token endpoint.
func (s *server) handleToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"access_token": staticToken,
		"token_type":   "Bearer",
		"expires_in":   3600,
	})
}

// handleUsers handles GET /2.0/users (list) and POST /2.0/users (create).
func (s *server) handleUsers(w http.ResponseWriter, r *http.Request) {
	if !s.authenticate(w, r) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.listUsers(w, r)
	case http.MethodPost:
		s.createUser(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *server) listUsers(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	filterTerm := r.URL.Query().Get("filter_term")
	all := make([]*mockUser, 0, len(s.users))
	for _, u := range s.users {
		// Box partial-matches filter_term against name and login; the connector
		// does an exact login check client-side after receiving the results.
		if filterTerm == "" || strings.Contains(u.Login, filterTerm) || strings.Contains(u.Name, filterTerm) {
			all = append(all, u)
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })

	start, end, offset, limit := applyPagination(len(all), r)
	entries := make([]interface{}, 0, end-start)
	for _, u := range all[start:end] {
		entries = append(entries, userToJSON(u, nil))
	}
	writeJSON(w, http.StatusOK, listResponse(entries, len(all), offset, limit))
}

func (s *server) createUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Login  string `json:"login"`
		Name   string `json:"name"`
		Role   string `json:"role"`
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid request body: "+err.Error())
		return
	}
	if body.Login == "" || body.Name == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "login and name are required")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, u := range s.users {
		if u.Login == body.Login {
			writeError(w, http.StatusConflict, "user_login_already_used", "the provided email address is already in use")
			return
		}
	}

	id := fmt.Sprintf("u-%03d", s.nextUserSeq)
	s.nextUserSeq++

	role, status := body.Role, body.Status
	if role == "" {
		role = roleUser
	}
	if status == "" {
		status = statusActive
	}

	u := &mockUser{ID: id, Name: body.Name, Login: body.Login, Role: role, Status: status}
	s.users[id] = u
	log.Printf("created user %s (%s, role=%s)", id, u.Login, u.Role)
	writeJSON(w, http.StatusCreated, userToJSON(u, nil))
}

// handleUserByID handles /2.0/users/{id} including the special /2.0/users/me path.
func (s *server) handleUserByID(w http.ResponseWriter, r *http.Request) {
	if !s.authenticate(w, r) {
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/2.0/users/")

	// /2.0/users/me — called by GetCurrentUserWithEnterprise (Validate + enterprise sync).
	if id == "me" {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.mu.Lock()
		u := s.users[seedUserAliceID]
		ent := s.enterprise
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, userToJSON(u, &ent))
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.mu.Lock()
		u, ok := s.users[id]
		s.mu.Unlock()
		if !ok {
			writeError(w, http.StatusNotFound, "not_found", "user not found: "+id)
			return
		}
		writeJSON(w, http.StatusOK, userToJSON(u, nil))

	case http.MethodPut:
		s.updateUser(w, r, id)

	case http.MethodDelete:
		s.deleteUser(w, r, id)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *server) updateUser(w http.ResponseWriter, r *http.Request, id string) {
	var updates map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid request body: "+err.Error())
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	u, ok := s.users[id]
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "user not found: "+id)
		return
	}

	if v, _ := updates[keyName].(string); v != "" {
		u.Name = v
	}
	if v, _ := updates["login"].(string); v != "" {
		for uid, other := range s.users {
			if uid != id && other.Login == v {
				writeError(w, http.StatusConflict, "user_login_already_used", "the provided email address is already in use")
				return
			}
		}
		u.Login = v
	}
	if v, _ := updates[keyRole].(string); v != "" {
		u.Role = v
	}
	if v, _ := updates[keyStatus].(string); v != "" {
		u.Status = v
	}

	log.Printf("updated user %q", id) //nolint:gosec // test server only: id from URL path, %q escapes special chars
	writeJSON(w, http.StatusOK, userToJSON(u, nil))
}

// deleteUser handles DELETE /2.0/users/{id}?force=true — returns 204 on success, 404 if absent.
func (s *server) deleteUser(w http.ResponseWriter, r *http.Request, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.users[id]; !ok {
		writeError(w, http.StatusNotFound, "not_found", "user not found: "+id)
		return
	}

	delete(s.users, id)
	log.Printf("deleted user %q", id) //nolint:gosec // test server only: id from URL path, %q escapes special chars
	w.WriteHeader(http.StatusNoContent)
}

// handleGroups handles GET /2.0/groups.
func (s *server) handleGroups(w http.ResponseWriter, r *http.Request) {
	if !s.authenticate(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	all := make([]*mockGroup, 0, len(s.groups))
	for _, g := range s.groups {
		all = append(all, g)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })

	start, end, offset, limit := applyPagination(len(all), r)
	entries := make([]interface{}, 0, end-start)
	for _, g := range all[start:end] {
		entries = append(entries, groupToJSON(g))
	}
	writeJSON(w, http.StatusOK, listResponse(entries, len(all), offset, limit))
}

// handleGroupSub handles /2.0/groups/{id} and /2.0/groups/{id}/memberships.
func (s *server) handleGroupSub(w http.ResponseWriter, r *http.Request) {
	if !s.authenticate(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/2.0/groups/")
	parts := strings.SplitN(path, "/", 2)
	groupID := parts[0]

	if len(parts) == 2 && parts[1] == "memberships" {
		s.listMemberships(w, r, groupID)
		return
	}

	s.mu.Lock()
	g, ok := s.groups[groupID]
	s.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "group not found: "+groupID)
		return
	}
	writeJSON(w, http.StatusOK, groupToJSON(g))
}

func (s *server) listMemberships(w http.ResponseWriter, r *http.Request, groupID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.groups[groupID]; !ok {
		writeError(w, http.StatusNotFound, "not_found", "group not found: "+groupID)
		return
	}

	mems := s.memberships[groupID]
	start, end, offset, limit := applyPagination(len(mems), r)
	entries := make([]interface{}, 0, end-start)
	for _, m := range mems[start:end] {
		u := s.users[m.UserID]
		if u == nil {
			continue
		}
		entries = append(entries, membershipToJSON(m, u, s.groups[groupID]))
	}
	writeJSON(w, http.StatusOK, listResponse(entries, len(mems), offset, limit))
}

// ─── JSON serialisers ─────────────────────────────────────────────────────────

func userToJSON(u *mockUser, ent *mockEnterprise) map[string]interface{} {
	m := map[string]interface{}{
		"id":    u.ID,
		keyType: roleUser,
		keyName: u.Name,
		"login": u.Login,
		keyRole: u.Role,
		keyStatus: u.Status,
	}
	if ent != nil {
		m["enterprise"] = map[string]interface{}{
			"id":   ent.ID,
			keyType: "enterprise",
			keyName: ent.Name,
		}
	}
	return m
}

func groupToJSON(g *mockGroup) map[string]interface{} {
	return map[string]interface{}{
		"id":                       g.ID,
		keyType:                    typeGroup,
		keyName:                    g.Name,
		"invitability_level":       g.InvitabilityLevel,
		"member_viewability_level": g.MemberViewabilityLevel,
	}
}

func membershipToJSON(m *mockMembership, u *mockUser, g *mockGroup) map[string]interface{} {
	return map[string]interface{}{
		"id":      m.ID,
		keyType:   "group_membership",
		keyRole:   m.Role,
		roleUser: map[string]interface{}{
			"id":    u.ID,
			keyType: roleUser,
			keyName: u.Name,
			"login": u.Login,
			keyRole: u.Role,
			keyStatus: u.Status,
		},
		typeGroup: map[string]interface{}{
			"id":   g.ID,
			keyType: typeGroup,
			keyName: g.Name,
		},
	}
}

// ─── pagination ───────────────────────────────────────────────────────────────

// applyPagination reads offset/limit query params and returns start, end, offset, limit.
// Box uses 0-based offset; the connector terminates when totalReturned >= total_count.
func applyPagination(total int, r *http.Request) (int, int, int, int) {
	q := r.URL.Query()
	limit := 200
	offset := 0
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if v := q.Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	start := offset
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}
	return start, end, offset, limit
}

func listResponse(entries []interface{}, total, offset, limit int) map[string]interface{} {
	return map[string]interface{}{
		"total_count": total,
		"limit":       limit,
		"offset":      offset,
		"entries":     entries,
	}
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes a Box-style error body.  The code field is what the connector
// inspects via strings.Contains(err.Error(), "code: <value>").
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]interface{}{
		keyType:   "error",
		"code":    code,
		"message": message,
		keyStatus: status,
	})
}

// ─── data types ───────────────────────────────────────────────────────────────

type mockEnterprise struct {
	ID   string
	Name string
}

type mockUser struct {
	ID     string
	Name   string
	Login  string
	Role   string
	Status string
}

type mockGroup struct {
	ID                     string
	Name                   string
	InvitabilityLevel      string
	MemberViewabilityLevel string
}

type mockMembership struct {
	ID     string
	Role   string
	UserID string
}
