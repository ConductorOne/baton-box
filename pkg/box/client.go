package box

import (
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
}

const (
	baseUrl       = "https://api.box.com"
	defaultOffset = 0
	defaultLimit  = 200
	errorType     = "error"
)

type paginationData struct {
	Limit      int `json:"limit"`
	Offset     int `json:"offset"`
	TotalCount int `json:"total_count"`
}

var ErrorResponse struct {
	Type        string `json:"type"`
	Code        string `json:"code"`
	ContextInfo struct {
		Message string `json:"message"`
	} `json:"context_info"`
	HelpURL   string `json:"help_url"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
	Status    int64  `json:"status"`
}

func NewClient(httpClient *http.Client, token string) *Client {
	return &Client{
		httpClient: httpClient,
		token:      token,
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
func RequestAccessToken(ctx context.Context, clientID string, clientSecret string, enterpriseId string) (string, error) {
	httpClient, err := uhttp.NewClient(ctx, uhttp.WithLogger(true, ctxzap.Extract(ctx)))
	if err != nil {
		return "", err
	}
	authUrl := fmt.Sprint(baseUrl, "/oauth2/token")
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

	if resp.StatusCode != http.StatusOK {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", err
		}
		return "", fmt.Errorf("failed to get access token: %s status: %s", string(body), resp.Status)
	}

	defer resp.Body.Close()

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
	usersUrl := fmt.Sprint(baseUrl, "/2.0/users")

	var res struct {
		paginationData
		Users []User `json:"entries"`
	}

	for {
		q := paginationQuery(offset, defaultLimit)
		q.Set("fields", "role,name,login,status")

		if err := c.doRequest(ctx, usersUrl, &res, q); err != nil {
			if ErrorResponse.Type == errorType {
				return nil, fmt.Errorf("failed to get users: %s", ErrorResponse.Message)
			}
			return nil, err
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
	usersUrl := fmt.Sprint(baseUrl, "/2.0/groups")

	var res struct {
		paginationData
		Groups []Group `json:"entries"`
	}

	for {
		q := paginationQuery(offset, defaultLimit)
		q.Set("fields", "invitability_level,member_viewability_level,name")

		if err := c.doRequest(ctx, usersUrl, &res, q); err != nil {
			if ErrorResponse.Type == errorType {
				return nil, fmt.Errorf("failed to get groups: %s", ErrorResponse.Message)
			}
			return nil, err
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
	usersUrl := fmt.Sprintf("%s/2.0/groups/%s/memberships", baseUrl, groupId)

	var res struct {
		paginationData
		GroupMembership []GroupMembership `json:"entries"`
	}

	for {
		q := paginationQuery(offset, defaultLimit)
		if err := c.doRequest(ctx, usersUrl, &res, q); err != nil {
			if ErrorResponse.Type == errorType {
				return nil, fmt.Errorf("failed to get group memberships: %s", ErrorResponse.Message)
			}
			return nil, err
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
	usersUrl := fmt.Sprint(baseUrl, "/2.0/users/me")
	params := url.Values{}
	params.Set("fields", "enterprise,role,name")

	var res User
	if err := c.doRequest(ctx, usersUrl, &res, params); err != nil {
		if ErrorResponse.Type == errorType {
			return User{}, fmt.Errorf("failed to get current user: %s", ErrorResponse.Message)
		}
		return User{}, err
	}

	return res, nil
}

// GetGroup returns Box group details.
func (c *Client) GetGroup(ctx context.Context, groupId string) (Group, error) {
	usersUrl := fmt.Sprint(baseUrl, "/2.0/groups/", groupId)

	var res Group
	params := url.Values{}
	params.Set("fields", "invitability_level,member_viewability_level,name")

	if err := c.doRequest(ctx, usersUrl, &res, params); err != nil {
		if ErrorResponse.Type == errorType {
			return Group{}, fmt.Errorf("failed to get group: %s", ErrorResponse.Message)
		}
		return Group{}, err
	}

	return res, nil
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

	// all GET requests in Box API return 200 status code if sucessful.
	if resp.StatusCode != http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&ErrorResponse); err != nil {
			return err
		}
	}

	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return err
	}

	return nil
}
