package box

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/conductorone/baton-sdk/pkg/uhttp"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
)

type Client struct {
	httpClient *http.Client
	token      string
	baseURL    string
}

const (
	defaultBaseURL = "https://api.box.com"
	defaultLimit   = 200

	pathUsers       = "/2.0/users"
	pathGroups      = "/2.0/groups"
	pathCurrentUser = "/2.0/users/me"
)

// markerPage holds the marker-pagination fields returned by Box list endpoints.
// Box rejects offset values above 10,000; marker-based pagination has no such limit.
type markerPage struct {
	NextMarker string `json:"next_marker"`
}

// markerQuery builds query params for marker-based pagination (usemarker=true).
func markerQuery(marker string) url.Values {
	q := url.Values{}
	q.Set("usemarker", "true")
	q.Set("limit", strconv.Itoa(defaultLimit))
	if marker != "" {
		q.Set("marker", marker)
	}
	return q
}

// APIError carries a structured Box API error so callers can check error codes with errors.As.
type APIError struct {
	Message string
	Code    string
	Status  int
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s (code: %s, status: %d)", e.Message, e.Code, e.Status)
}

func NewClient(httpClient *http.Client, token string, baseURL string) (*Client, error) {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	if _, err := url.Parse(baseURL); err != nil {
		return nil, fmt.Errorf("baton-box: invalid base URL %q: %w", baseURL, err)
	}
	return &Client{
		httpClient: httpClient,
		token:      token,
		baseURL:    strings.TrimRight(baseURL, "/"),
	}, nil
}

// RequestAccessToken creates bearer token needed to use the Box API.
func RequestAccessToken(ctx context.Context, clientID string, clientSecret string, enterpriseId string, baseURL string) (string, error) {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	httpClient, err := uhttp.NewClient(ctx, uhttp.WithLogger(true, ctxzap.Extract(ctx)))
	if err != nil {
		return "", err
	}
	authUrl := fmt.Sprint(baseURL, "/oauth2/token")
	data := url.Values{}
	data.Add("client_id", clientID)
	data.Add("client_secret", clientSecret)
	data.Add("grant_type", "client_credentials")
	data.Add("box_subject_type", "enterprise")
	data.Add("box_subject_id", enterpriseId)
	encodedData := data.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, authUrl, strings.NewReader(encodedData))
	if err != nil {
		return "", err
	}

	req.Header.Add("accept", "application/json")
	req.Header.Add("content-type", "application/x-www-form-urlencoded")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", err
		}
		return "", fmt.Errorf("failed to get access token: %s status: %s", string(body), resp.Status)
	}

	var res struct {
		AccessToken string `json:"access_token"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", err
	}
	return res.AccessToken, nil
}

// GetUsers returns all users from Box enterprise.
func (c *Client) GetUsers(ctx context.Context) ([]User, error) {
	var allUsers []User
	var marker string
	usersURL := c.baseURL + pathUsers

	for {
		var res struct {
			markerPage
			Users []User `json:"entries"`
		}
		q := markerQuery(marker)
		q.Set("fields", "role,name,login,status")

		if err := c.doRequest(ctx, usersURL, &res, q); err != nil {
			return nil, fmt.Errorf("baton-box: failed to get users: %w", err)
		}

		allUsers = append(allUsers, res.Users...)
		if res.NextMarker == "" {
			break
		}
		marker = res.NextMarker
	}

	return allUsers, nil
}

// GetGroups returns all groups from Box enterprise.
func (c *Client) GetGroups(ctx context.Context) ([]Group, error) {
	var allGroups []Group
	var marker string
	groupsURL := c.baseURL + pathGroups

	for {
		var res struct {
			markerPage
			Groups []Group `json:"entries"`
		}
		q := markerQuery(marker)
		q.Set("fields", "invitability_level,member_viewability_level,name")

		if err := c.doRequest(ctx, groupsURL, &res, q); err != nil {
			return nil, fmt.Errorf("baton-box: failed to get groups: %w", err)
		}

		allGroups = append(allGroups, res.Groups...)
		if res.NextMarker == "" {
			break
		}
		marker = res.NextMarker
	}

	return allGroups, nil
}

// GetGroupMemberships returns all group memberships from Box enterprise.
func (c *Client) GetGroupMemberships(ctx context.Context, groupId string) ([]GroupMembership, error) {
	var allMemberships []GroupMembership
	var marker string
	membershipsURL := fmt.Sprintf("%s%s/%s/memberships", c.baseURL, pathGroups, groupId)

	for {
		var res struct {
			markerPage
			GroupMembership []GroupMembership `json:"entries"`
		}
		if err := c.doRequest(ctx, membershipsURL, &res, markerQuery(marker)); err != nil {
			return nil, fmt.Errorf("baton-box: failed to get group memberships: %w", err)
		}

		allMemberships = append(allMemberships, res.GroupMembership...)
		if res.NextMarker == "" {
			break
		}
		marker = res.NextMarker
	}

	return allMemberships, nil
}

// GetCurrentUserWithEnterprise returns current user with enterprise data.
func (c *Client) GetCurrentUserWithEnterprise(ctx context.Context) (User, error) {
	usersUrl := c.baseURL + pathCurrentUser
	params := url.Values{}
	params.Set("fields", "enterprise,role,name")

	var res User
	if err := c.doRequest(ctx, usersUrl, &res, params); err != nil {
		return User{}, fmt.Errorf("baton-box: failed to get current user: %w", err)
	}

	return res, nil
}

// GetUser fetches a single Box user by ID.
func (c *Client) GetUser(ctx context.Context, userID string) (*User, error) {
	userURL := fmt.Sprintf("%s%s/%s", c.baseURL, pathUsers, userID)
	q := url.Values{}
	q.Set("fields", "id,role,name,login,status")
	var res User
	if err := c.doRequest(ctx, userURL, &res, q); err != nil {
		return nil, fmt.Errorf("baton-box: failed to get user %s: %w", userID, err)
	}
	return &res, nil
}

// CheckAdminAccess verifies the token has manage-users admin scope by requesting a
// single user entry. Box CCG Service Accounts are not assigned a role designation,
// so validating via the role field is incorrect; a 403 here means insufficient scope.
func (c *Client) CheckAdminAccess(ctx context.Context) error {
	q := markerQuery("")
	q.Set("limit", "1")
	q.Set("fields", "id")
	var res struct {
		markerPage
		Users []User `json:"entries"`
	}
	return c.doRequest(ctx, c.baseURL+pathUsers, &res, q)
}

// GetGroup returns Box group details.
func (c *Client) GetGroup(ctx context.Context, groupId string) (Group, error) {
	usersUrl := fmt.Sprintf("%s%s/%s", c.baseURL, pathGroups, groupId)

	var res Group
	params := url.Values{}
	params.Set("fields", "invitability_level,member_viewability_level,name")

	if err := c.doRequest(ctx, usersUrl, &res, params); err != nil {
		return Group{}, fmt.Errorf("baton-box: failed to get group: %w", err)
	}

	return res, nil
}

// GetUserByLogin fetches a single user by exact login (email) match via GET /2.0/users?filter_term=.
// Returns nil, nil when no user with that login exists.
func (c *Client) GetUserByLogin(ctx context.Context, login string) (*User, error) {
	usersURL := c.baseURL + pathUsers
	q := markerQuery("")
	q.Set("filter_term", login)
	q.Set("fields", "id,role,name,login,status")

	var res struct {
		markerPage
		Users []User `json:"entries"`
	}

	if err := c.doRequest(ctx, usersURL, &res, q); err != nil {
		return nil, fmt.Errorf("baton-box: failed to get user by login: %w", err)
	}

	for i := range res.Users {
		if res.Users[i].Login == login {
			return &res.Users[i], nil
		}
	}
	return nil, nil
}

// CreateUser creates a new managed Box user via POST /2.0/users.
func (c *Client) CreateUser(ctx context.Context, input CreateUserInput) (*User, error) {
	userURL := c.baseURL + pathUsers
	var res User
	if err := c.doWrite(ctx, http.MethodPost, userURL, input, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// UpdateUser updates a Box user's properties via PUT /2.0/users/{user_id}.
func (c *Client) UpdateUser(ctx context.Context, userID string, updates map[string]interface{}) (*User, error) {
	userURL := fmt.Sprintf("%s%s/%s", c.baseURL, pathUsers, userID)
	var res User
	if err := c.doWrite(ctx, http.MethodPut, userURL, updates, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// DeactivateUser sets a Box user's status to inactive (reversible).
func (c *Client) DeactivateUser(ctx context.Context, userID string) error {
	_, err := c.UpdateUser(ctx, userID, map[string]interface{}{"status": "inactive"})
	return err
}

// DeleteUser permanently deletes a Box user via DELETE /2.0/users/{id}.
// force=true removes the user even when they still have content.
// notify=false suppresses the email notification sent to the user.
func (c *Client) DeleteUser(ctx context.Context, userID string) error {
	q := url.Values{}
	q.Set("force", "true")
	q.Set("notify", "false")
	userURL := fmt.Sprintf("%s%s/%s?%s", c.baseURL, pathUsers, userID, q.Encode())
	return c.doWrite(ctx, http.MethodDelete, userURL, nil, nil)
}

// ActivateUser sets a Box user's status back to active.
func (c *Client) ActivateUser(ctx context.Context, userID string) error {
	_, err := c.UpdateUser(ctx, userID, map[string]interface{}{"status": "active"})
	return err
}

// doWrite performs a POST or PUT request with a JSON body, decoding the response into res when non-nil.
func (c *Client) doWrite(ctx context.Context, method, rawURL string, body interface{}, res interface{}) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, method, rawURL, bytes.NewReader(data))
	if err != nil {
		return err
	}

	req.Header.Set("accept", "application/json")
	req.Header.Set("content-type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.token))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var errResp struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		}
		if decErr := json.NewDecoder(resp.Body).Decode(&errResp); decErr != nil {
			return fmt.Errorf("baton-box: request failed (status %d, decode error: %w)", resp.StatusCode, decErr)
		}
		return &APIError{Message: errResp.Message, Code: errResp.Code, Status: resp.StatusCode}
	}

	if res != nil && resp.StatusCode != http.StatusNoContent {
		if err := json.NewDecoder(resp.Body).Decode(res); err != nil {
			return err
		}
	}

	return nil
}

func (c *Client) doRequest(ctx context.Context, url string, res interface{}, params url.Values) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	if params != nil {
		req.URL.RawQuery = params.Encode()
	}

	req.Header.Add("accept", "application/json")
	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", c.token))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		}
		if decErr := json.NewDecoder(resp.Body).Decode(&errResp); decErr != nil {
			return fmt.Errorf("baton-box: request failed (status %d, decode error: %w)", resp.StatusCode, decErr)
		}
		return &APIError{Message: errResp.Message, Code: errResp.Code, Status: resp.StatusCode}
	}

	return json.NewDecoder(resp.Body).Decode(res)
}
