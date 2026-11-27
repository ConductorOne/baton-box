package box

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/uhttp"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
)

type Client struct {
	httpClient *uhttp.BaseHttpClient
	// transferHTTPClient sends the transfer request, which Box holds open while it
	// moves content. See NewTransferHTTPClient.
	transferHTTPClient *uhttp.BaseHttpClient
	token              string
	baseURL            string
}

// ErrTransferNoAnswer reports that Box received the whole transfer request but no
// answer from Box arrived before the client stopped waiting: the wait timed out, or
// Box's gateway gave up with a 504. Box keeps a transfer running once it has the
// request, so the caller can report it as still in progress.
var ErrTransferNoAnswer = errors.New("box received the transfer request but did not answer in time")

const (
	defaultBaseURL = "https://api.box.com"
	defaultLimit   = 200

	pathUsers       = "/2.0/users"
	pathGroups      = "/2.0/groups"
	pathCurrentUser = "/2.0/users/me"
	pathRootFolder  = "folders/0"
)

// APIError carries a structured Box API error so callers can check error codes with errors.As.
type APIError struct {
	Message string
	Code    string
	Status  int
}

// Error returns only Box's error code: the status error it is joined with already
// carries the HTTP status and Box's message.
func (e *APIError) Error() string {
	if e.Code == "" {
		return "box error without a code"
	}
	return "box error code: " + e.Code
}

// ClientOption configures a Client.
type ClientOption func(*Client)

// WithTransferHTTPClient sets the HTTP client used for the transfer request. Without
// it, the transfer request uses the client passed to NewClient.
func WithTransferHTTPClient(httpClient *http.Client) ClientOption {
	return func(c *Client) {
		c.transferHTTPClient = uhttp.NewBaseHttpClient(httpClient)
	}
}

func NewClient(httpClient *http.Client, token string, baseURL string, opts ...ClientOption) (*Client, error) {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	if _, err := url.Parse(baseURL); err != nil {
		return nil, fmt.Errorf("invalid base URL %q: %w", baseURL, err)
	}
	base := uhttp.NewBaseHttpClient(httpClient)
	c := &Client{
		httpClient:         base,
		transferHTTPClient: base,
		token:              token,
		baseURL:            strings.TrimRight(baseURL, "/"),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// NewTransferHTTPClient returns an HTTP client for the transfer request. The client
// from uhttp.NewClient stops waiting for response headers after 60 seconds, while Box
// holds the transfer request open until it finishes (up to 10 minutes). This client
// has no header timeout, so the caller's context alone bounds the wait. It keeps the
// proxy and HTTP/2 keepalive settings of the uhttp transport, and opens a new
// connection per request so a connection left idle across a Lambda freeze is never
// reused.
func NewTransferHTTPClient() *http.Client {
	t := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:   true,
		TLSHandshakeTimeout: 10 * time.Second,
		DisableKeepAlives:   true,
		HTTP2: &http.HTTP2Config{
			SendPingTimeout: 15 * time.Second,
			PingTimeout:     15 * time.Second,
		},
	}
	return &http.Client{Transport: t}
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
		return "", fmt.Errorf("building auth url: %w", err)
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
		return nil, "", nil, fmt.Errorf("building users url: %w", err)
	}

	var res struct {
		markerPage
		Users []User `json:"entries"`
	}
	q := markerQuery(marker)
	q.Set("fields", "role,name,login,status")

	annos, err := c.doRequest(ctx, usersURL, &res, q)
	if err != nil {
		return nil, "", nil, fmt.Errorf("failed to get users: %w", err)
	}

	return res.Users, res.NextMarker, annos, nil
}

// GetGroups returns one page of groups from the Box enterprise.
func (c *Client) GetGroups(ctx context.Context, marker string) ([]Group, string, annotations.Annotations, error) {
	groupsURL, err := url.JoinPath(c.baseURL, pathGroups)
	if err != nil {
		return nil, "", nil, fmt.Errorf("building groups url: %w", err)
	}

	var res struct {
		markerPage
		Groups []Group `json:"entries"`
	}
	q := markerQuery(marker)
	q.Set("fields", "invitability_level,member_viewability_level,name")

	annos, err := c.doRequest(ctx, groupsURL, &res, q)
	if err != nil {
		return nil, "", nil, fmt.Errorf("failed to get groups: %w", err)
	}

	return res.Groups, res.NextMarker, annos, nil
}

// GetGroupMemberships returns one page of memberships for the given group.
func (c *Client) GetGroupMemberships(ctx context.Context, groupId string, marker string) ([]GroupMembership, string, annotations.Annotations, error) {
	membershipsURL, err := url.JoinPath(c.baseURL, pathGroups, url.PathEscape(groupId), "memberships")
	if err != nil {
		return nil, "", nil, fmt.Errorf("building memberships url: %w", err)
	}

	var res struct {
		markerPage
		GroupMembership []GroupMembership `json:"entries"`
	}

	annos, err := c.doRequest(ctx, membershipsURL, &res, markerQuery(marker))
	if err != nil {
		return nil, "", nil, fmt.Errorf("failed to get group memberships: %w", err)
	}

	return res.GroupMembership, res.NextMarker, annos, nil
}

// GetCurrentUserWithEnterprise returns current user with enterprise data.
func (c *Client) GetCurrentUserWithEnterprise(ctx context.Context) (User, error) {
	usersURL, err := url.JoinPath(c.baseURL, pathCurrentUser)
	if err != nil {
		return User{}, fmt.Errorf("building current user url: %w", err)
	}
	params := url.Values{}
	params.Set("fields", "enterprise,role,name")

	var res User
	if _, err := c.doRequest(ctx, usersURL, &res, params); err != nil {
		return User{}, fmt.Errorf("failed to get current user: %w", err)
	}

	return res, nil
}

// GetUser fetches a single Box user by ID.
func (c *Client) GetUser(ctx context.Context, userID string) (*User, error) {
	userURL, err := url.JoinPath(c.baseURL, pathUsers, url.PathEscape(userID))
	if err != nil {
		return nil, fmt.Errorf("building user url: %w", err)
	}
	q := url.Values{}
	q.Set("fields", "id,role,name,login,status")
	var res User
	if _, err := c.doRequest(ctx, userURL, &res, q); err != nil {
		return nil, fmt.Errorf("failed to get user %s: %w", userID, err)
	}
	return &res, nil
}

// CheckAdminAccess verifies the token has manage-users admin scope by requesting a
// single user entry. Box CCG Service Accounts are not assigned a role designation,
// so validating via the role field is incorrect; a 403 here means insufficient scope.
func (c *Client) CheckAdminAccess(ctx context.Context) error {
	usersURL, err := url.JoinPath(c.baseURL, pathUsers)
	if err != nil {
		return fmt.Errorf("building users url: %w", err)
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
	groupURL, err := url.JoinPath(c.baseURL, pathGroups, url.PathEscape(groupId))
	if err != nil {
		return Group{}, fmt.Errorf("building group url: %w", err)
	}

	var res Group
	params := url.Values{}
	params.Set("fields", "invitability_level,member_viewability_level,name")

	if _, err := c.doRequest(ctx, groupURL, &res, params); err != nil {
		return Group{}, fmt.Errorf("failed to get group: %w", err)
	}

	return res, nil
}

// GetUserByLogin fetches a single user by exact login (email) match via GET /2.0/users?filter_term=.
// Returns nil, nil when no user with that login exists.
func (c *Client) GetUserByLogin(ctx context.Context, login string) (*User, error) {
	usersURL, err := url.JoinPath(c.baseURL, pathUsers)
	if err != nil {
		return nil, fmt.Errorf("building users url: %w", err)
	}
	q := markerQuery("")
	q.Set("filter_term", login)
	q.Set("fields", "id,role,name,login,status")

	var res struct {
		markerPage
		Users []User `json:"entries"`
	}

	if _, err := c.doRequest(ctx, usersURL, &res, q); err != nil {
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
	userURL, err := url.JoinPath(c.baseURL, pathUsers)
	if err != nil {
		return nil, fmt.Errorf("building users url: %w", err)
	}
	var res User
	if err := c.doWrite(ctx, http.MethodPost, userURL, input, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// UpdateUser updates a Box user's properties via PUT /2.0/users/{user_id}.
func (c *Client) UpdateUser(ctx context.Context, userID string, updates map[string]interface{}) (*User, error) {
	userURL, err := url.JoinPath(c.baseURL, pathUsers, url.PathEscape(userID))
	if err != nil {
		return nil, fmt.Errorf("building user url: %w", err)
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
// With force=true Box removes the user even when they still own content, and deletes
// that content; with force=false Box refuses to delete a user who still owns content.
// notify=false suppresses the email notification sent to the user.
func (c *Client) DeleteUser(ctx context.Context, userID string, force bool) error {
	userURL, err := url.JoinPath(c.baseURL, pathUsers, url.PathEscape(userID))
	if err != nil {
		return fmt.Errorf("building user url: %w", err)
	}
	q := url.Values{}
	q.Set("force", strconv.FormatBool(force))
	q.Set("notify", "false")
	return c.doWrite(ctx, http.MethodDelete, userURL+"?"+q.Encode(), nil, nil)
}

// TransferOwnedItems moves every item owned by sourceUserID into a new folder in
// destUserID's account via PUT /2.0/users/{id}/folders/0, and returns that folder.
// Box names it "{User}'s Files and Folders". Box holds the request open while it moves
// content; only a transfer that runs past 10 minutes keeps going after Box answers, and
// Box offers no endpoint to check completion.
//
// When Box has received the whole request but the wait ends first (the context
// deadline, a response-header timeout, or a 504 from Box's gateway), the error wraps
// ErrTransferNoAnswer.
func (c *Client) TransferOwnedItems(ctx context.Context, sourceUserID, destUserID string, notify bool) (*Folder, error) {
	folderURL, err := url.JoinPath(c.baseURL, pathUsers, url.PathEscape(sourceUserID), pathRootFolder)
	if err != nil {
		return nil, fmt.Errorf("building transfer url: %w", err)
	}
	q := url.Values{}
	q.Set("notify", strconv.FormatBool(notify))

	var requestSent atomic.Bool
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			if info.Err == nil {
				requestSent.Store(true)
			}
		},
	})

	body := transferOwnedFolderInput{OwnedBy: BaseType{ID: destUserID, Type: "user"}}
	var res Folder
	if err := c.doWriteWith(ctx, c.transferHTTPClient, http.MethodPut, folderURL+"?"+q.Encode(), body, &res); err != nil {
		if requestSent.Load() && isNoAnswer(err) {
			return nil, fmt.Errorf("%w: %w", ErrTransferNoAnswer, err)
		}
		return nil, err
	}
	return &res, nil
}

// isNoAnswer reports whether err means the server did not answer in time: a timeout
// (context deadline, response-header or client timeout) or a 504 from a gateway.
func isNoAnswer(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Status == http.StatusGatewayTimeout
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
		return nil, fmt.Errorf("parsing url: %w", err)
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
			return annos, fmt.Errorf("decoding response: %w", decErr)
		}
	}

	return annos, nil
}

// doWrite performs a POST, PUT, or DELETE request through uhttp, sending body as JSON
// when non-nil and decoding the response into res when non-nil. On an error status
// the returned error carries the gRPC code uhttp derives from the HTTP status, plus
// rate-limit details, joined with an *APIError holding Box's error code (e.g.
// "user_login_already_used") so callers can inspect it with errors.As.
func (c *Client) doWrite(ctx context.Context, method, rawURL string, body interface{}, res interface{}) error {
	return c.doWriteWith(ctx, c.httpClient, method, rawURL, body, res)
}

// doWriteWith is doWrite through the given HTTP client.
func (c *Client) doWriteWith(ctx context.Context, hc *uhttp.BaseHttpClient, method, rawURL string, body interface{}, res interface{}) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parsing url: %w", err)
	}

	reqOpts := []uhttp.RequestOption{uhttp.WithBearerToken(c.token), uhttp.WithAcceptJSONHeader()}
	if body != nil {
		reqOpts = append(reqOpts, uhttp.WithJSONBody(body))
	}
	req, err := hc.NewRequest(ctx, method, u, reqOpts...)
	if err != nil {
		return err
	}

	var errBody errorResponse
	doOpts := []uhttp.DoOption{uhttp.WithErrorResponse(&errBody)}
	if res != nil {
		// Only when there is a body to decode: uhttp rejects a response without a
		// JSON content type, such as the empty 204 Box returns for a delete.
		doOpts = append(doOpts, uhttp.WithJSONResponse(res))
	}
	resp, err := hc.Do(req, doOpts...)
	if resp != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		if resp != nil && resp.StatusCode >= http.StatusBadRequest {
			return errors.Join(err, &APIError{Message: errBody.ErrMessage, Code: errBody.Code, Status: resp.StatusCode})
		}
		return err
	}
	return nil
}
