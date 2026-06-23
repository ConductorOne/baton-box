package connector

import (
	"context"
	"fmt"
	"strings"

	"errors"

	"github.com/conductorone/baton-box/pkg/box"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type userResourceType struct {
	resourceType *v2.ResourceType
	client       *box.Client
}

func (o *userResourceType) ResourceType(_ context.Context) *v2.ResourceType {
	return o.resourceType
}

// Create a new connector resource for a Box user.
func userResource(user *box.User, parentResourceID *v2.ResourceId) (*v2.Resource, error) {
	names := strings.SplitN(user.Name, " ", 2)
	var firstName, lastName string
	switch len(names) {
	case 1:
		firstName = names[0]
	case 2:
		firstName = names[0]
		lastName = names[1]
	}

	profile := map[string]interface{}{
		"first_name": firstName,
		"last_name":  lastName,
		fieldLogin:   user.Login,
		fieldUserID:  user.ID,
	}

	var statusOpt rs.UserTraitOption
	switch user.Status {
	case "active", "cannot_delete_edit", "cannot_delete_edit_upload":
		statusOpt = rs.WithStatus(v2.UserTrait_Status_STATUS_ENABLED)
	case "inactive":
		statusOpt = rs.WithStatus(v2.UserTrait_Status_STATUS_DISABLED)
	case "pending":
		statusOpt = rs.WithDetailedStatus(v2.UserTrait_Status_STATUS_DISABLED, "pending")
	default:
		statusOpt = rs.WithStatus(v2.UserTrait_Status_STATUS_UNSPECIFIED)
	}

	userTraitOptions := []rs.UserTraitOption{
		rs.WithUserProfile(profile),
		rs.WithEmail(user.Login, true),
		statusOpt,
	}

	ret, err := rs.NewUserResource(
		user.Name,
		resourceTypeUser,
		user.ID,
		userTraitOptions,
		rs.WithParentResourceID(parentResourceID),
	)
	if err != nil {
		return nil, err
	}

	return ret, nil
}

func (o *userResourceType) List(ctx context.Context, parentId *v2.ResourceId, attrs rs.SyncOpAttrs) ([]*v2.Resource, *rs.SyncOpResults, error) {
	if parentId == nil {
		return nil, &rs.SyncOpResults{}, nil
	}

	users, nextMarker, annos, err := o.client.GetUsers(ctx, attrs.PageToken.Token)
	if err != nil {
		return nil, nil, fmt.Errorf("baton-box: failed to list users: %w", err)
	}

	var rv []*v2.Resource
	for _, baseUser := range users {
		baseUserCopy := baseUser
		ur, err := userResource(&baseUserCopy, parentId)
		if err != nil {
			return nil, nil, err
		}
		rv = append(rv, ur)
	}

	return rv, &rs.SyncOpResults{NextPageToken: nextMarker, Annotations: annos}, nil
}

func (o *userResourceType) Entitlements(_ context.Context, _ *v2.Resource, _ rs.SyncOpAttrs) ([]*v2.Entitlement, *rs.SyncOpResults, error) {
	return nil, &rs.SyncOpResults{}, nil
}

func (o *userResourceType) Grants(_ context.Context, _ *v2.Resource, _ rs.SyncOpAttrs) ([]*v2.Grant, *rs.SyncOpResults, error) {
	return nil, &rs.SyncOpResults{}, nil
}

func userBuilder(client *box.Client) *userResourceType {
	return &userResourceType{
		resourceType: resourceTypeUser,
		client:       client,
	}
}

// Compile-time interface assertions.
var _ connectorbuilder.AccountManagerV2 = (*userResourceType)(nil)
var _ connectorbuilder.ResourceDeleterV2 = (*userResourceType)(nil)

func (o *userResourceType) CreateAccountCapabilityDetails(_ context.Context) (*v2.CredentialDetailsAccountProvisioning, annotations.Annotations, error) {
	return &v2.CredentialDetailsAccountProvisioning{
		SupportedCredentialOptions: []v2.CapabilityDetailCredentialOption{
			v2.CapabilityDetailCredentialOption_CAPABILITY_DETAIL_CREDENTIAL_OPTION_NO_PASSWORD,
		},
		PreferredCredentialOption: v2.CapabilityDetailCredentialOption_CAPABILITY_DETAIL_CREDENTIAL_OPTION_NO_PASSWORD,
	}, nil, nil
}

// CreateAccount creates a new Box managed user and returns the resulting resource.
func (o *userResourceType) CreateAccount(
	ctx context.Context,
	accountInfo *v2.AccountInfo,
	_ *v2.LocalCredentialOptions,
) (connectorbuilder.CreateAccountResponse, []*v2.PlaintextData, annotations.Annotations, error) {
	pMap := accountInfo.GetProfile().AsMap()

	login, _ := pMap[fieldLogin].(string)
	if login == "" {
		return nil, nil, nil, status.Error(codes.InvalidArgument, "baton-box: create account requires a login email")
	}

	name, _ := pMap[fieldName].(string)
	if name == "" {
		return nil, nil, nil, status.Error(codes.InvalidArgument, "baton-box: create account requires a name")
	}

	var (
		boxUser       *box.User
		alreadyExisted bool
	)

	created, createErr := o.client.CreateUser(ctx, box.CreateUserInput{
		Login:  login,
		Name:   name,
		Role:   user,
		Status: "active",
	})
	if createErr != nil {
		var apiErr *box.APIError
		if !errors.As(createErr, &apiErr) || apiErr.Code != "user_login_already_used" {
			return nil, nil, nil, fmt.Errorf("baton-box: failed to create user: %w", createErr)
		}

		alreadyExisted = true
		existing, fetchErr := o.client.GetUserByLogin(ctx, login)
		if fetchErr != nil {
			return nil, nil, nil, fmt.Errorf("baton-box: login already in use, failed to fetch existing user: %w", fetchErr)
		}
		if existing == nil {
			return nil, nil, nil, fmt.Errorf("baton-box: login already in use but user not found: %s", login)
		}
		boxUser = existing
	} else {
		boxUser = created
	}

	res, err := userResource(boxUser, nil)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("baton-box: failed to build user resource: %w", err)
	}

	if alreadyExisted {
		return v2.CreateAccountResponse_AlreadyExistsResult_builder{
			Resource: res,
		}.Build(), nil, nil, nil
	}

	return v2.CreateAccountResponse_SuccessResult_builder{
		Resource:              res,
		IsCreateAccountResult: true,
	}.Build(), nil, nil, nil
}

// Delete permanently removes a Box user via DELETE /2.0/users/{id}?force=true.
// Returns nil when the user no longer exists so the operation is idempotent.
func (o *userResourceType) Delete(ctx context.Context, resourceID *v2.ResourceId, _ *v2.ResourceId) (annotations.Annotations, error) {
	userID := resourceID.GetResource()
	if err := o.client.DeleteUser(ctx, userID); err != nil {
		var apiErr *box.APIError
		if errors.As(err, &apiErr) && apiErr.Code == "not_found" {
			return nil, nil
		}
		return nil, fmt.Errorf("baton-box: failed to delete user %s: %w", userID, err)
	}
	return nil, nil
}
