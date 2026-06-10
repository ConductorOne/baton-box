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
	defaultOffset  = 0
	defaultLimit   = 200
)

type paginationData struct {
	Limit      int `json:"limit"`
	Offset     int `json:"offset"`
	TotalCount int `json:"total_count"`
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

func NewClient(httpClient *http.Client, token string, baseURL string) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		httpClient: httpClient,
		token:      token,
		baseURL:    baseURL,
	}
}

// returns query params with pagination options.
func paginationQuery(offset int, limit int) url.Values {
	q := url.Values{}
	stringOffset := strconv.Itoa(offset)
	stringLimit := strconv.Itoa(limit)

	q.Add("offset", stringOffset)
	q.Add("limit", stringLimit)
	return q
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
	offset := defaultOffset
	totalReturned := 0
	usersUrl := fmt.Sprint(c.baseURL, "/2.0/users")

	var res struct {
		paginationData
		Users []User `json:"entries"`
	}

	for {
		q := paginationQuery(offset, defaultLimit)
		q.Set("fields", "role,name,login,status")

		if err := c.doRequest(ctx, usersUrl, &res, q); err != nil {
			return nil, fmt.Errorf("failed to get users: %w", err)
		}

		allUsers = append(allUsers, res.Users...)

		totalReturned += res.Limit
		if totalReturned >= res.TotalCount {
			break
		}

		offset += res.Limit
	}

	return allUsers, nil
}

// GetGroups returns all groups from Box enterprise.
func (c *Client) GetGroups(ctx context.Context) ([]Group, error) {
	var allGroups []Group
	offset := defaultOffset
	totalReturned := 0
	usersUrl := fmt.Sprint(c.baseURL, "/2.0/groups")

	var res struct {
		paginationData
		Groups []Group `json:"entries"`
	}

	for {
		q := paginationQuery(offset, defaultLimit)
		q.Set("fields", "invitability_level,member_viewability_level,name")

		if err := c.doRequest(ctx, usersUrl, &res, q); err != nil {
			return nil, fmt.Errorf("failed to get groups: %w", err)
		}

		allGroups = append(allGroups, res.Groups...)

		totalReturned += res.Limit
		if totalReturned >= res.TotalCount {
			break
		}

		offset += res.Limit
	}

	return allGroups, nil
}

// GetGroupMemberships returns all group memberships from Box enterprise.
func (c *Client) GetGroupMemberships(ctx context.Context, groupId string) ([]GroupMembership, error) {
	var allGroupMemberships []GroupMembership
	offset := defaultOffset
	totalReturned := 0
	usersUrl := fmt.Sprintf("%s/2.0/groups/%s/memberships", c.baseURL, groupId)

	var res struct {
		paginationData
		GroupMembership []GroupMembership `json:"entries"`
	}

	for {
		q := paginationQuery(offset, defaultLimit)
		if err := c.doRequest(ctx, usersUrl, &res, q); err != nil {
			return nil, fmt.Errorf("failed to get group memberships: %w", err)
		}

		allGroupMemberships = append(allGroupMemberships, res.GroupMembership...)

		totalReturned += res.Limit
		if totalReturned >= res.TotalCount {
			break
		}

		offset += res.Limit
	}

	return allGroupMemberships, nil
}

// GetCurrentUserWithEnterprise returns current user with enterprise data.
func (c *Client) GetCurrentUserWithEnterprise(ctx context.Context) (User, error) {
	usersUrl := fmt.Sprint(c.baseURL, "/2.0/users/me")
	params := url.Values{}
	params.Set("fields", "enterprise,role,name")

	var res User
	if err := c.doRequest(ctx, usersUrl, &res, params); err != nil {
		return User{}, fmt.Errorf("failed to get current user: %w", err)
	}

	return res, nil
}

// GetGroup returns Box group details.
func (c *Client) GetGroup(ctx context.Context, groupId string) (Group, error) {
	usersUrl := fmt.Sprint(c.baseURL, "/2.0/groups/", groupId)

	var res Group
	params := url.Values{}
	params.Set("fields", "invitability_level,member_viewability_level,name")

	if err := c.doRequest(ctx, usersUrl, &res, params); err != nil {
		return Group{}, fmt.Errorf("failed to get group: %w", err)
	}

	return res, nil
}

// GetUserByLogin fetches a single user by exact login (email) match via GET /2.0/users?filter_term=.
// Returns nil, nil when no user with that login exists.
func (c *Client) GetUserByLogin(ctx context.Context, login string) (*User, error) {
	usersURL := fmt.Sprint(c.baseURL, "/2.0/users")
	q := paginationQuery(defaultOffset, defaultLimit)
	q.Set("filter_term", login)
	q.Set("fields", "id,role,name,login,status")

	var res struct {
		paginationData
		Users []User `json:"entries"`
	}

	if err := c.doRequest(ctx, usersURL, &res, q); err != nil {
		return nil, fmt.Errorf("failed to get user by login: %w", err)
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
	userURL := fmt.Sprint(c.baseURL, "/2.0/users")
	var res User
	if err := c.doWrite(ctx, http.MethodPost, userURL, input, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// UpdateUser updates a Box user's properties via PUT /2.0/users/{user_id}.
func (c *Client) UpdateUser(ctx context.Context, userID string, updates map[string]interface{}) (*User, error) {
	userURL := fmt.Sprintf("%s/2.0/users/%s", c.baseURL, userID)
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
	userURL := fmt.Sprintf("%s/2.0/users/%s", c.baseURL, userID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, userURL, nil)
	if err != nil {
		return err
	}

	q := url.Values{}
	q.Set("force", "true")
	q.Set("notify", "false")
	req.URL.RawQuery = q.Encode()

	req.Header.Set("accept", "application/json")
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
			return fmt.Errorf("delete request failed with status %d", resp.StatusCode)
		}
		return &APIError{Message: errResp.Message, Code: errResp.Code, Status: resp.StatusCode}
	}

	return nil
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
			return fmt.Errorf("request failed with status %d", resp.StatusCode)
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
			return fmt.Errorf("request failed with status %d", resp.StatusCode)
		}
		return &APIError{Message: errResp.Message, Code: errResp.Code, Status: resp.StatusCode}
	}

	return json.NewDecoder(resp.Body).Decode(res)
}
