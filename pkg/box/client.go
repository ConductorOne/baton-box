package box

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/uhttp"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
)

type Client struct {
	httpClient *uhttp.BaseHttpClient
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
		httpClient: uhttp.NewBaseHttpClient(httpClient),
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
	authURL, err := url.JoinPath(baseURL, "/oauth2/token")
	if err != nil {
		return "", fmt.Errorf("baton-box: building auth url: %w", err)
	}
	data := url.Values{}
	data.Add("client_id", clientID)
	data.Add("client_secret", clientSecret)
	data.Add("grant_type", "client_credentials")
	data.Add("box_subject_type", "enterprise")
	data.Add("box_subject_id", enterpriseId)
	encodedData := data.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, authURL, strings.NewReader(encodedData))
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

// GetUsers returns one page of users from the Box enterprise.
func (c *Client) GetUsers(ctx context.Context, marker string) ([]User, string, annotations.Annotations, error) {
	usersURL, err := url.JoinPath(c.baseURL, pathUsers)
	if err != nil {
		return nil, "", nil, fmt.Errorf("baton-box: building users url: %w", err)
	}

	var res struct {
		markerPage
		Users []User `json:"entries"`
	}
	q := markerQuery(marker)
	q.Set("fields", "role,name,login,status")

	annos, err := c.doRequest(ctx, usersURL, &res, q)
	if err != nil {
		return nil, "", nil, fmt.Errorf("baton-box: failed to get users: %w", err)
	}

	return res.Users, res.NextMarker, annos, nil
}

// GetGroups returns one page of groups from the Box enterprise.
func (c *Client) GetGroups(ctx context.Context, marker string) ([]Group, string, annotations.Annotations, error) {
	groupsURL, err := url.JoinPath(c.baseURL, pathGroups)
	if err != nil {
		return nil, "", nil, fmt.Errorf("baton-box: building groups url: %w", err)
	}

	var res struct {
		markerPage
		Groups []Group `json:"entries"`
	}
	q := markerQuery(marker)
	q.Set("fields", "invitability_level,member_viewability_level,name")

	annos, err := c.doRequest(ctx, groupsURL, &res, q)
	if err != nil {
		return nil, "", nil, fmt.Errorf("baton-box: failed to get groups: %w", err)
	}

	return res.Groups, res.NextMarker, annos, nil
}

// GetGroupMemberships returns one page of memberships for the given group.
func (c *Client) GetGroupMemberships(ctx context.Context, groupId string, marker string) ([]GroupMembership, string, annotations.Annotations, error) {
	membershipsURL, err := url.JoinPath(c.baseURL, pathGroups, groupId, "memberships")
	if err != nil {
		return nil, "", nil, fmt.Errorf("baton-box: building memberships url: %w", err)
	}

	var res struct {
		markerPage
		GroupMembership []GroupMembership `json:"entries"`
	}

	annos, err := c.doRequest(ctx, membershipsURL, &res, markerQuery(marker))
	if err != nil {
		return nil, "", nil, fmt.Errorf("baton-box: failed to get group memberships: %w", err)
	}

	return res.GroupMembership, res.NextMarker, annos, nil
}

// GetCurrentUserWithEnterprise returns current user with enterprise data.
func (c *Client) GetCurrentUserWithEnterprise(ctx context.Context) (User, error) {
	usersURL, err := url.JoinPath(c.baseURL, pathCurrentUser)
	if err != nil {
		return User{}, fmt.Errorf("baton-box: building current user url: %w", err)
	}
	params := url.Values{}
	params.Set("fields", "enterprise,role,name")

	var res User
	if _, err := c.doRequest(ctx, usersURL, &res, params); err != nil {
		return User{}, fmt.Errorf("baton-box: failed to get current user: %w", err)
	}

	return res, nil
}

// GetUser fetches a single Box user by ID.
func (c *Client) GetUser(ctx context.Context, userID string) (*User, error) {
	userURL, err := url.JoinPath(c.baseURL, pathUsers, userID)
	if err != nil {
		return nil, fmt.Errorf("baton-box: building user url: %w", err)
	}
	q := url.Values{}
	q.Set("fields", "id,role,name,login,status")
	var res User
	if _, err := c.doRequest(ctx, userURL, &res, q); err != nil {
		return nil, fmt.Errorf("baton-box: failed to get user %s: %w", userID, err)
	}
	return &res, nil
}

// CheckAdminAccess verifies the token has manage-users admin scope by requesting a
// single user entry. Box CCG Service Accounts are not assigned a role designation,
// so validating via the role field is incorrect; a 403 here means insufficient scope.
func (c *Client) CheckAdminAccess(ctx context.Context) error {
	usersURL, err := url.JoinPath(c.baseURL, pathUsers)
	if err != nil {
		return fmt.Errorf("baton-box: building users url: %w", err)
	}
	q := markerQuery("")
	q.Set("limit", "1")
	q.Set("fields", "id")
	var res struct {
		markerPage
		Users []User `json:"entries"`
	}
	_, err = c.doRequest(ctx, usersURL, &res, q)
	return err
}

// GetGroup returns Box group details.
func (c *Client) GetGroup(ctx context.Context, groupId string) (Group, error) {
	groupURL, err := url.JoinPath(c.baseURL, pathGroups, groupId)
	if err != nil {
		return Group{}, fmt.Errorf("baton-box: building group url: %w", err)
	}

	var res Group
	params := url.Values{}
	params.Set("fields", "invitability_level,member_viewability_level,name")

	if _, err := c.doRequest(ctx, groupURL, &res, params); err != nil {
		return Group{}, fmt.Errorf("baton-box: failed to get group: %w", err)
	}

	return res, nil
}

// GetUserByLogin fetches a single user by exact login (email) match via GET /2.0/users?filter_term=.
// Returns nil, nil when no user with that login exists.
func (c *Client) GetUserByLogin(ctx context.Context, login string) (*User, error) {
	usersURL, err := url.JoinPath(c.baseURL, pathUsers)
	if err != nil {
		return nil, fmt.Errorf("baton-box: building users url: %w", err)
	}
	q := markerQuery("")
	q.Set("filter_term", login)
	q.Set("fields", "id,role,name,login,status")

	var res struct {
		markerPage
		Users []User `json:"entries"`
	}

	if _, err := c.doRequest(ctx, usersURL, &res, q); err != nil {
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
	userURL, err := url.JoinPath(c.baseURL, pathUsers)
	if err != nil {
		return nil, fmt.Errorf("baton-box: building users url: %w", err)
	}
	var res User
	if err := c.doWrite(ctx, http.MethodPost, userURL, input, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// UpdateUser updates a Box user's properties via PUT /2.0/users/{user_id}.
func (c *Client) UpdateUser(ctx context.Context, userID string, updates map[string]interface{}) (*User, error) {
	userURL, err := url.JoinPath(c.baseURL, pathUsers, userID)
	if err != nil {
		return nil, fmt.Errorf("baton-box: building user url: %w", err)
	}
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
	userURL, err := url.JoinPath(c.baseURL, pathUsers, userID)
	if err != nil {
		return fmt.Errorf("baton-box: building user url: %w", err)
	}
	q := url.Values{}
	q.Set("force", "true")
	q.Set("notify", "false")
	return c.doWrite(ctx, http.MethodDelete, userURL+"?"+q.Encode(), nil, nil)
}

// ActivateUser sets a Box user's status back to active.
func (c *Client) ActivateUser(ctx context.Context, userID string) error {
	_, err := c.UpdateUser(ctx, userID, map[string]interface{}{"status": "active"})
	return err
}

// doRequest performs a GET request using uhttp, captures rate-limit annotations, and
// decodes the JSON response into res. Returns annotations even when an error occurs so
// callers can still apply rate-limit backpressure.
func (c *Client) doRequest(ctx context.Context, rawURL string, res interface{}, params url.Values) (annotations.Annotations, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("baton-box: parsing url: %w", err)
	}
	if params != nil {
		u.RawQuery = params.Encode()
	}

	req, err := c.httpClient.NewRequest(ctx, http.MethodGet, u,
		uhttp.WithBearerToken(c.token),
		uhttp.WithAcceptJSONHeader(),
	)
	if err != nil {
		return nil, err
	}

	var rl v2.RateLimitDescription
	resp, err := c.httpClient.Do(req, uhttp.WithRatelimitData(&rl))

	var annos annotations.Annotations
	annos.WithRateLimiting(&rl)

	if err != nil {
		return annos, err
	}
	defer resp.Body.Close()

	if res != nil {
		if decErr := json.NewDecoder(resp.Body).Decode(res); decErr != nil {
			return annos, fmt.Errorf("baton-box: decoding response: %w", decErr)
		}
	}

	return annos, nil
}

// doWrite performs a POST, PUT, or DELETE request with a JSON body, decoding the
// response into res when non-nil. Uses the underlying HTTP client directly so that
// Box API error codes (e.g. "user_login_already_used") are preserved as *APIError
// for callers to inspect with errors.As.
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

	resp, err := c.httpClient.HttpClient.Do(req)
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
